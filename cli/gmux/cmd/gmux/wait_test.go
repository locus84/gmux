package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func captureWaitOutput(t *testing.T, run func() int) (int, string, string) {
	t.Helper()
	oldOut, oldErr := os.Stdout, os.Stderr
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout, os.Stderr = outW, errW
	code := run()
	_ = outW.Close()
	_ = errW.Close()
	os.Stdout, os.Stderr = oldOut, oldErr
	stdout, _ := io.ReadAll(outR)
	stderr, _ := io.ReadAll(errR)
	_ = outR.Close()
	_ = errR.Close()
	return code, string(stdout), string(stderr)
}

func TestCmdWaitManyAllPreservesInputOrder(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sessions", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": []map[string]any{
			{"id": "sess-a", "kind": "pi", "alive": true},
			{"id": "sess-b", "kind": "pi", "alive": true},
		}})
	})
	mux.HandleFunc("/v1/sessions/sess-a/wait", func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(40 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": map[string]any{"reason": "idle", "message": map[string]any{"value": "a"}}})
	})
	mux.HandleFunc("/v1/sessions/sess-b/wait", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": map[string]any{"reason": "idle", "message": map[string]any{"value": "b"}}})
	})
	startActionsTestDaemon(t, mux)

	code, stdout, stderr := captureWaitOutput(t, func() int {
		return cmdWaitMany([]string{"a", "b"}, true, 0, true, "")
	})
	if code != 0 || stderr != "" {
		t.Fatalf("exit=%d stderr=%q", code, stderr)
	}
	var results []waitResult
	if err := json.Unmarshal([]byte(stdout), &results); err != nil {
		t.Fatalf("decode output %q: %v", stdout, err)
	}
	if len(results) != 2 || results[0].SessionID != "sess-a" || results[1].SessionID != "sess-b" {
		t.Fatalf("results out of input order: %#v", results)
	}
}

func TestCmdWaitManyAnyCancelsLosingRequest(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sessions", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": []map[string]any{
			{"id": "sess-slow", "kind": "pi", "alive": true},
			{"id": "sess-fast", "kind": "pi", "alive": true},
		}})
	})
	cancelled := make(chan struct{}, 1)
	mux.HandleFunc("/v1/sessions/sess-slow/wait", func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		cancelled <- struct{}{}
	})
	mux.HandleFunc("/v1/sessions/sess-fast/wait", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": map[string]any{"reason": "idle"}})
	})
	mux.HandleFunc("/v1/sessions/sess-fast/message", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": map[string]any{"value": "fast"}})
	})
	startActionsTestDaemon(t, mux)

	code, stdout, stderr := captureWaitOutput(t, func() int {
		return cmdWaitMany([]string{"slow", "fast"}, false, 0, true, "")
	})
	if code != 0 || stderr != "" {
		t.Fatalf("exit=%d stderr=%q", code, stderr)
	}
	var result waitResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("decode output %q: %v", stdout, err)
	}
	if result.SessionID != "sess-fast" {
		t.Fatalf("winner=%q, want sess-fast", result.SessionID)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("losing wait request was not cancelled")
	}
}

func TestCmdWaitManyUsesOneSharedTimeout(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sessions", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": []map[string]any{
			{"id": "sess-a", "kind": "pi", "alive": true},
			{"id": "sess-b", "kind": "pi", "alive": true},
		}})
	})
	block := func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }
	mux.HandleFunc("/v1/sessions/sess-a/wait", block)
	mux.HandleFunc("/v1/sessions/sess-b/wait", block)
	startActionsTestDaemon(t, mux)

	started := time.Now()
	code, stdout, stderr := captureWaitOutput(t, func() int {
		return cmdWaitMany([]string{"a", "b"}, true, 1, true, "")
	})
	if elapsed := time.Since(started); elapsed > 1800*time.Millisecond {
		t.Fatalf("shared one-second timeout took %s", elapsed)
	}
	if code != waitExitTimeout || !strings.Contains(stderr, "timed out after 1s") {
		t.Fatalf("exit=%d stderr=%q", code, stderr)
	}
	var results []waitResult
	if err := json.Unmarshal([]byte(stdout), &results); err != nil {
		t.Fatalf("decode output %q: %v", stdout, err)
	}
	if len(results) != 2 || results[0].Reason != "timeout" || results[1].Reason != "timeout" {
		t.Fatalf("results=%#v", results)
	}
}

func TestCmdWaitManyRejectsDuplicateResolvedTargetBeforeWaiting(t *testing.T) {
	var waits atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sessions", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": []map[string]any{{"id": "sess-abcdef12", "kind": "pi", "alive": true}}})
	})
	mux.HandleFunc("/v1/sessions/sess-abcdef12/wait", func(w http.ResponseWriter, _ *http.Request) {
		waits.Add(1)
	})
	startActionsTestDaemon(t, mux)

	code, _, stderr := captureWaitOutput(t, func() int {
		return cmdWaitMany([]string{"abcdef12", "sess-abcdef12"}, true, 0, false, "")
	})
	if code != 1 || !strings.Contains(stderr, "duplicate wait target") || waits.Load() != 0 {
		t.Fatalf("exit=%d stderr=%q waits=%d", code, stderr, waits.Load())
	}
}

func TestCanonicalSessionIDDoesNotDuplicatePeerSuffix(t *testing.T) {
	sess := cliSession{ID: "sess-abc123@laptop", Peer: "laptop"}
	if got := canonicalSessionID(sess); got != "sess-abc123@laptop" {
		t.Fatalf("canonicalSessionID=%q", got)
	}
}

func TestReportWaitResultsExitPrecedence(t *testing.T) {
	code, _, _ := captureWaitOutput(t, func() int {
		return reportWaitResults([]waitResult{
			{Reason: "died", ExitCode: waitExitDied},
			{Reason: "failed", ExitCode: waitExitFailed},
			{Reason: "timeout", ExitCode: waitExitTimeout},
		}, 10)
	})
	if code != waitExitTimeout {
		t.Fatalf("exit=%d, want timeout", code)
	}
}

func TestCmdWaitJSONPreservesFailedAndInDoubtRecords(t *testing.T) {
	for _, tc := range []struct {
		name, reason, state string
		wantExit            int
	}{
		{name: "failed", reason: "failed", state: "failed", wantExit: waitExitFailed},
		{name: "in doubt", reason: "died", state: "in_doubt", wantExit: waitExitDied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/v1/sessions", func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": []map[string]any{{"id": "sess-pi", "kind": "pi", "alive": true}}})
			})
			mux.HandleFunc("/v1/sessions/sess-pi/wait", func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": map[string]any{
					"reason":  tc.reason,
					"message": map[string]any{"request_id": "req", "state": tc.state, "error": "test error"},
				}})
			})
			startActionsTestDaemon(t, mux)

			old := os.Stdout
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			os.Stdout = w
			code := cmdWait("pi", 0, true, "req")
			_ = w.Close()
			os.Stdout = old
			body, _ := io.ReadAll(r)
			_ = r.Close()
			if code != tc.wantExit || !strings.Contains(string(body), `"state":"`+tc.state+`"`) {
				t.Fatalf("exit=%d body=%q", code, body)
			}
		})
	}
}
