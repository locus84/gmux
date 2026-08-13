package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"
)

// Exit codes from cmdWait. Distinct codes let scripts dispatch on the
// reason a wait ended without parsing strings.
const (
	waitExitIdle    = 0
	waitExitDied    = 2
	waitExitTimeout = 3
	waitExitFailed  = 4
)

type waitResult struct {
	SessionID string          `json:"session_id"`
	Reason    string          `json:"reason"`
	ExitCode  int             `json:"exit_code"`
	Result    json.RawMessage `json:"result,omitempty"`

	session cliSession
	err     error
	index   int
}

// cmdWait preserves the original single-target behavior and JSON shape.
func cmdWait(ref string, timeoutSecs int, jsonOut bool, requestID string) int {
	sess, err := resolveSession(ref)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gmux:", err)
		return 1
	}
	if requestID != "" && sess.Kind != "pi" {
		fmt.Fprintln(os.Stderr, "gmux: --request-id is only supported for Pi sessions")
		return 1
	}

	ctx := context.Background()
	cancel := func() {}
	if timeoutSecs > 0 {
		ctx, cancel = context.WithTimeout(ctx, time.Duration(timeoutSecs)*time.Second)
	}
	defer cancel()

	client := waitHTTPClient()
	result := waitForSession(ctx, client, sess, timeoutSecs, requestID, jsonOut)
	if result.err != nil {
		fmt.Fprintln(os.Stderr, "gmux:", result.err)
		return 1
	}

	if jsonOut && len(result.Result) > 0 {
		if _, err := os.Stdout.Write(append(result.Result, '\n')); err != nil {
			fmt.Fprintln(os.Stderr, "gmux:", err)
			return 1
		}
	}

	switch result.Reason {
	case "idle":
		return waitExitIdle
	case "failed":
		fmt.Fprintf(os.Stderr, "gmux: Pi message failed in session %s\n", displayID(sess))
		return waitExitFailed
	case "died":
		fmt.Fprintf(os.Stderr, "gmux: session %s died before becoming idle\n", displayID(sess))
		return waitExitDied
	case "timeout":
		fmt.Fprintf(os.Stderr, "gmux: wait timed out after %ds\n", timeoutSecs)
		return waitExitTimeout
	default:
		fmt.Fprintf(os.Stderr, "gmux: unexpected wait reason %q\n", result.Reason)
		return 1
	}
}

// cmdWaitMany concurrently waits for explicit targets. --all returns results
// in input order; --any cancels the losing HTTP requests after the first
// observed terminal result.
func cmdWaitMany(refs []string, waitAll bool, timeoutSecs int, jsonOut bool, requestID string) int {
	sessions, err := resolveWaitSessions(refs)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gmux:", err)
		return 1
	}
	if len(sessions) > 1 && requestID != "" {
		fmt.Fprintln(os.Stderr, "gmux: --request-id is only supported for a single session")
		return 1
	}
	for _, sess := range sessions {
		if requestID != "" && sess.Kind != "pi" {
			fmt.Fprintln(os.Stderr, "gmux: --request-id is only supported for Pi sessions")
			return 1
		}
	}

	ctx := context.Background()
	cancel := func() {}
	if timeoutSecs > 0 {
		ctx, cancel = context.WithTimeout(ctx, time.Duration(timeoutSecs)*time.Second)
	} else {
		ctx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	client := waitHTTPClient()
	resultsCh := make(chan waitResult, len(sessions))
	for i, sess := range sessions {
		go func(index int, target cliSession) {
			result := waitForSession(ctx, client, target, timeoutSecs, requestID, jsonOut)
			result.index = index
			resultsCh <- result
		}(i, sess)
	}

	var results []waitResult
	if waitAll {
		results = make([]waitResult, len(sessions))
		for range sessions {
			result := <-resultsCh
			results[result.index] = result
		}
	} else {
		results = []waitResult{<-resultsCh}
		cancel()
	}

	if jsonOut {
		var encodeErr error
		if waitAll {
			encodeErr = json.NewEncoder(os.Stdout).Encode(results)
		} else {
			encodeErr = json.NewEncoder(os.Stdout).Encode(results[0])
		}
		if encodeErr != nil {
			fmt.Fprintln(os.Stderr, "gmux:", encodeErr)
			return 1
		}
	} else if !waitAll {
		fmt.Fprintln(os.Stdout, results[0].SessionID)
	}

	return reportWaitResults(results, timeoutSecs)
}

func resolveWaitSessions(refs []string) ([]cliSession, error) {
	all, err := fetchSessions()
	if err != nil {
		return nil, err
	}
	resolved := make([]cliSession, 0, len(refs))
	seen := make(map[string]bool, len(refs))
	for _, ref := range refs {
		sess, err := matchSession(all, ref)
		if err != nil {
			return nil, err
		}
		key := sess.Peer + "\x00" + sess.ID
		if seen[key] {
			return nil, fmt.Errorf("duplicate wait target %s", canonicalSessionID(sess))
		}
		seen[key] = true
		resolved = append(resolved, sess)
	}
	return resolved, nil
}

func canonicalSessionID(sess cliSession) string {
	// gmuxd namespaces peer-owned IDs before exposing them in /v1/sessions
	// (for example sess-abc@laptop), so the stored ID is already the full,
	// copy-pasteable address for both local and peer sessions.
	return sess.ID
}

func waitHTTPClient() *http.Client {
	client := gmuxdClient()
	// Wait lifetime is controlled by the shared context and optional daemon
	// timeout, not the management client's default five-second timeout.
	client.Timeout = 0
	return client
}

func waitForSession(ctx context.Context, client *http.Client, sess cliSession, timeoutSecs int, requestID string, includeResult bool) waitResult {
	result := waitResult{SessionID: canonicalSessionID(sess), session: sess}
	endpoint := gmuxdBaseURL() + "/v1/sessions/" + url.PathEscape(sess.ID) + "/wait"
	query := url.Values{}
	if timeoutSecs > 0 {
		query.Set("timeout", strconv.Itoa(timeoutSecs))
	}
	if requestID != "" {
		query.Set("request_id", requestID)
	}
	if encoded := query.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, http.NoBody)
	if err != nil {
		result.Reason = "error"
		result.err = err
		result.ExitCode = 1
		return result
	}
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			result.Reason = "timeout"
			result.ExitCode = waitExitTimeout
			return result
		}
		result.Reason = "error"
		result.err = err
		result.ExitCode = 1
		return result
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusRequestTimeout {
		result.Reason = "timeout"
		result.ExitCode = waitExitTimeout
		return result
	}
	if resp.StatusCode != http.StatusOK {
		result.Reason = "error"
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		switch resp.StatusCode {
		case http.StatusUnprocessableEntity:
			result.err = fmt.Errorf("wait not supported for session %s: %s", displayID(sess), extractMessage(body))
		case http.StatusNotFound:
			result.err = fmt.Errorf("wait target not found for %s: %s", displayID(sess), extractMessage(body))
		default:
			result.err = fmt.Errorf("wait failed for %s: %s: %s", displayID(sess), resp.Status, extractMessage(body))
		}
		result.ExitCode = 1
		return result
	}

	var envelope struct {
		Data struct {
			Reason  string          `json:"reason"`
			Message json.RawMessage `json:"message"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		result.Reason = "error"
		result.err = fmt.Errorf("decode wait response for %s: %w", displayID(sess), err)
		result.ExitCode = 1
		return result
	}

	result.Reason = envelope.Data.Reason
	switch result.Reason {
	case "idle":
		result.ExitCode = waitExitIdle
	case "died":
		result.ExitCode = waitExitDied
	case "failed":
		result.ExitCode = waitExitFailed
	default:
		result.err = fmt.Errorf("unexpected wait reason %q for %s", result.Reason, displayID(sess))
		result.ExitCode = 1
		return result
	}

	if includeResult {
		if len(envelope.Data.Message) > 0 && string(envelope.Data.Message) != "null" {
			result.Result = envelope.Data.Message
		} else if result.Reason == "idle" {
			message, err := readWaitResult(ctx, client, sess)
			if err != nil {
				result.Reason = "error"
				result.err = err
				result.ExitCode = 1
				return result
			}
			result.Result = message
		}
	}
	return result
}

func readWaitResult(ctx context.Context, client *http.Client, sess cliSession) (json.RawMessage, error) {
	if sess.Kind != "pi" {
		return json.RawMessage(`{"reason":"idle"}`), nil
	}
	endpoint := gmuxdBaseURL() + "/v1/sessions/" + url.PathEscape(sess.ID) + "/message"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("read Pi result: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return json.RawMessage(`{"reason":"idle"}`), nil
	}
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("read Pi result: %s: %s", resp.Status, extractMessage(body))
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decode Pi result: %w", err)
	}
	if len(envelope.Data) == 0 {
		return nil, fmt.Errorf("Pi result response was empty")
	}
	return envelope.Data, nil
}

func reportWaitResults(results []waitResult, timeoutSecs int) int {
	for _, result := range results {
		if result.err != nil {
			fmt.Fprintln(os.Stderr, "gmux:", result.err)
			return 1
		}
	}

	exitCode := waitExitIdle
	for _, result := range results {
		if result.Reason == "timeout" {
			exitCode = waitExitTimeout
		}
	}
	if exitCode != waitExitTimeout {
		for _, result := range results {
			if result.Reason == "failed" {
				exitCode = waitExitFailed
				break
			}
		}
	}
	if exitCode == waitExitIdle {
		for _, result := range results {
			if result.Reason == "died" {
				exitCode = waitExitDied
				break
			}
		}
	}

	for _, result := range results {
		switch result.Reason {
		case "failed":
			fmt.Fprintf(os.Stderr, "gmux: Pi message failed in session %s\n", displayID(result.session))
		case "died":
			fmt.Fprintf(os.Stderr, "gmux: session %s died before becoming idle\n", displayID(result.session))
		}
	}
	if exitCode == waitExitTimeout {
		fmt.Fprintf(os.Stderr, "gmux: wait timed out after %ds\n", timeoutSecs)
	}
	return exitCode
}

// extractMessage pulls the .error.message field out of gmuxd's standard error
// envelope, falling back to the raw body if the shape doesn't match.
func extractMessage(body []byte) string {
	var env struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err == nil && env.Error.Message != "" {
		return env.Error.Message
	}
	return string(body)
}
