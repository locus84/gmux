package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gmuxapp/gmux/packages/adapter/adapters"
)

func TestClaudeHookSocketCLI(t *testing.T) {
	for _, args := range [][]string{
		{"__claude-hook", "--socket"},
		{"__claude-hook", "--socket", ""},
		{"__claude-hook", "--other", "/tmp/a"},
		{"__claude-hook", "--socket", "/tmp/a", "extra"},
	} {
		if _, err := parseCLI(args); err == nil {
			t.Fatalf("accepted malformed arguments: %q", args)
		}
	}
	t.Setenv("GMUX_SESSION_SOCK", "/tmp/stale.sock")
	c, err := parseCLI([]string{"__claude-hook", "--socket", "/tmp/right.sock"})
	if err != nil {
		t.Fatal(err)
	}
	if got := claudeHookSocket(c.claudeHookSocket); got != "/tmp/right.sock" {
		t.Fatal(got)
	}
	legacy, err := parseCLI([]string{"__claude-hook"})
	if err != nil {
		t.Fatal(err)
	}
	if got := claudeHookSocket(legacy.claudeHookSocket); got != "/tmp/stale.sock" {
		t.Fatal(got)
	}
}

// Exercise the actual shell-interpreted adapter command and CLI entry point,
// not just a direct HTTP helper call. No real Claude process is started.
func TestClaudeHookSubprocess(t *testing.T) {
	if os.Getenv("GMUX_TEST_CLAUDE_HOOK") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			c, err := parseCLI(os.Args[i+1:])
			if err != nil {
				os.Exit(2)
			}
			os.Exit(claudeHook(c.claudeHookSocket))
		}
	}
	os.Exit(3)
}

func hookTestServer(t *testing.T, sock string, status int) *atomic.Int32 {
	t.Helper()
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	count := new(atomic.Int32)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		if r.Method == "POST" && r.URL.Path == "/hook/event" {
			count.Add(1)
		}
		w.WriteHeader(status)
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return count
}

func TestClaudeHookLaunchBindingSurvivesStaleEnvironment(t *testing.T) {
	dir := t.TempDir()
	// Quotes, spaces and shell expansion characters must remain literal.
	socket := filepath.Join(dir, "right '$x.sock")
	stale := filepath.Join(dir, "stale.sock")
	received := hookTestServer(t, socket, http.StatusOK)
	misrouted := hookTestServer(t, stale, http.StatusOK)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(dir, "gmux ' shim")
	if err := os.WriteFile(shim, []byte("#!/bin/sh\nexec \"$GMUX_TEST_BINARY\" -test.run=TestClaudeHookSubprocess -- \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	argv, ok := (&adapters.Claude{}).HookCommand([]string{"claude"}, shim, socket)
	if !ok {
		t.Fatal("hook not injected")
	}
	var settings struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(argv[2]), &settings); err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{"SessionStart", "SessionEnd", "UserPromptSubmit", "Stop", "StopFailure"} {
		entries := settings.Hooks[event]
		if len(entries) != 1 || len(entries[0].Hooks) != 1 {
			t.Fatalf("missing %s", event)
		}
		cmd := exec.Command("/bin/sh", "-c", entries[0].Hooks[0].Command)
		cmd.Env = append(os.Environ(), "GMUX_TEST_CLAUDE_HOOK=1", "GMUX_TEST_BINARY="+binary, "GMUX_SESSION_SOCK="+stale)
		// Same turn-start payload for each registration tests every command's routing.
		cmd.Stdin = strings.NewReader(`{"hook_event_name":"UserPromptSubmit"}`)
		out, err := cmd.CombinedOutput()
		if err != nil || len(out) != 0 {
			t.Fatalf("%s: err=%v output=%s", event, err, out)
		}
	}
	if received.Load() != 5 || misrouted.Load() != 0 {
		t.Fatalf("right=%d stale=%d", received.Load(), misrouted.Load())
	}
}

func TestClaudeHookDeliveryErrorsAreDiagnostic(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusInternalServerError} {
		sock := filepath.Join(t.TempDir(), "s.sock")
		hookTestServer(t, sock, status)
		if err := postClaudeHookEvent(sock, []byte(`{}`)); err == nil {
			t.Fatalf("HTTP %d ignored", status)
		}
	}
	missing := filepath.Join(t.TempDir(), "missing.sock")
	if err := postClaudeHookEvent(missing, []byte(`{}`)); err == nil {
		t.Fatal("transport error ignored")
	}
	// A dead explicit destination must not fall back to a live stale socket.
	stale := filepath.Join(t.TempDir(), "stale.sock")
	misrouted := hookTestServer(t, stale, http.StatusOK)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "-test.run=TestClaudeHookSubprocess", "--", "__claude-hook", "--socket", missing)
	cmd.Env = append(os.Environ(), "GMUX_TEST_CLAUDE_HOOK=1", "GMUX_SESSION_SOCK="+stale)
	cmd.Stdin = strings.NewReader(`{"hook_event_name":"UserPromptSubmit"}`)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("hook must not block Claude: %v", err)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "hook delivery failed") {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if misrouted.Load() != 0 {
		t.Fatal("failed explicit binding fell back to stale environment")
	}
}
