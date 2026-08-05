package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
)

// Exit codes from cmdWait. Distinct codes let scripts dispatch on the
// reason a wait ended without parsing strings.
//
//   - waitExitIdle (0): session reached a Working == false state
//   - waitExitDied (2): session crashed or was killed before going idle
//   - waitExitTimeout (3): --timeout elapsed
//
// Any other usage / network error returns 1, matching the rest of the
// CLI.
const (
	waitExitIdle    = 0
	waitExitDied    = 2
	waitExitTimeout = 3
	waitExitFailed  = 4
)

// cmdWait implements `gmux wait <id> [--timeout N]`.
//
// The wait itself happens server-side: gmuxd already subscribes to
// per-session events for its own bookkeeping, so we just hand it the
// session id and block on the HTTP response. That keeps the CLI free
// of SSE-parsing logic and ensures the idle-detection rules (which
// adapter kinds emit Status.Working, what counts as "died") live in
// one place.
//
// Remote sessions route through the owning daemon using the same action path.
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

	client := gmuxdClient()
	// The default 5s client timeout would cut off any wait that
	// outlasts a turn on a slow agent. With no client-side timeout
	// the only deadline is the optional server-side --timeout.
	client.Timeout = 0

	// No request body; pass http.NoBody so we don't advertise a
	// content-type for bytes that don't exist.
	resp, err := client.Post(endpoint, "", http.NoBody)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gmux:", err)
		return 1
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// Body is { ok: true, data: { reason: "idle" | "died" } }
		var env struct {
			Data struct {
				Reason  string          `json:"reason"`
				Message json.RawMessage `json:"message"`
			} `json:"data"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
			fmt.Fprintln(os.Stderr, "gmux: decode wait response:", err)
			return 1
		}
		switch env.Data.Reason {
		case "idle":
			if jsonOut {
				if len(env.Data.Message) > 0 && string(env.Data.Message) != "null" {
					if _, err := os.Stdout.Write(append(env.Data.Message, '\n')); err != nil {
						fmt.Fprintln(os.Stderr, "gmux:", err)
						return 1
					}
				} else if err := printWaitJSON(client, sess); err != nil {
					fmt.Fprintln(os.Stderr, "gmux:", err)
					return 1
				}
			}
			return waitExitIdle
		case "failed":
			if jsonOut && len(env.Data.Message) > 0 && string(env.Data.Message) != "null" {
				if _, err := os.Stdout.Write(append(env.Data.Message, '\n')); err != nil {
					fmt.Fprintln(os.Stderr, "gmux:", err)
					return 1
				}
			}
			fmt.Fprintf(os.Stderr, "gmux: Pi message failed in session %s\n", displayID(sess))
			return waitExitFailed
		case "died":
			if jsonOut && len(env.Data.Message) > 0 && string(env.Data.Message) != "null" {
				if _, err := os.Stdout.Write(append(env.Data.Message, '\n')); err != nil {
					fmt.Fprintln(os.Stderr, "gmux:", err)
					return 1
				}
			}
			fmt.Fprintf(os.Stderr, "gmux: session %s died before becoming idle\n", displayID(sess))
			return waitExitDied
		default:
			fmt.Fprintf(os.Stderr, "gmux: unexpected wait reason %q\n", env.Data.Reason)
			return 1
		}
	case http.StatusRequestTimeout:
		fmt.Fprintf(os.Stderr, "gmux: wait timed out after %ds\n", timeoutSecs)
		return waitExitTimeout
	case http.StatusUnprocessableEntity:
		// Adapter kind doesn't emit an idle signal. Surface the
		// daemon's message so the user knows which kind they hit.
		body, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "gmux: wait not supported for this session: %s\n",
			extractMessage(body))
		return 1
	case http.StatusNotFound:
		body, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "gmux: wait target not found for %s: %s\n", displayID(sess), extractMessage(body))
		return 1
	default:
		body, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "gmux: wait failed: %s: %s\n", resp.Status, extractMessage(body))
		return 1
	}
}

func printWaitJSON(client *http.Client, sess cliSession) error {
	if sess.Kind != "pi" {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"reason": "idle"})
	}
	endpoint := gmuxdBaseURL() + "/v1/sessions/" + url.PathEscape(sess.ID) + "/message"
	resp, err := client.Get(endpoint)
	if err != nil {
		return fmt.Errorf("read Pi result: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"reason": "idle"})
	}
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("read Pi result: %s: %s", resp.Status, extractMessage(body))
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("decode Pi result: %w", err)
	}
	if len(envelope.Data) == 0 {
		return fmt.Errorf("Pi result response was empty")
	}
	_, err = os.Stdout.Write(append(envelope.Data, '\n'))
	return err
}

// extractMessage pulls the .error.message field out of gmuxd's
// standard error envelope, falling back to the raw body if the
// shape doesn't match.
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
