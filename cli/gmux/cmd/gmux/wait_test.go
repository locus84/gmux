package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

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
