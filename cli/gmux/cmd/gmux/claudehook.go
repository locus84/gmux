package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/gmuxapp/gmux/packages/adapter/adapters"
)

// claudeHook implements the hidden `gmux __claude-hook` subcommand that Claude
// Code invokes as a command hook (injected per-launch by the claude adapter's
// HookCommand via `--settings`). Claude writes the event payload as JSON to
// stdin (carrying hook_event_name); we translate it to the tool-neutral
// /hook/event protocol (docs/runner-hook-protocol.md) and POST it to the runner
// socket pinned by --socket. Legacy hooks fall back to GMUX_SESSION_SOCK.
//
// Fire-and-forget and always exits 0: a hook must never block or fail Claude.
// Without an explicit socket or GMUX_SESSION_SOCK the hook is a no-op.
// It writes nothing to stdout — Claude folds a hook's stdout into
// the model context for some events (UserPromptSubmit), so emitting anything
// would pollute the conversation.
func claudeHook(socket *string) int {
	runClaudeHook(os.Stdin, claudeHookSocket(socket))
	return 0
}

// An explicit launch binding must never fall back to another session's env,
// even when delivery to that socket fails.
func claudeHookSocket(socket *string) string {
	if socket != nil {
		return *socket
	}
	return os.Getenv("GMUX_SESSION_SOCK")
}

// runClaudeHook is the testable core. When sock is empty it only drains stdin.
func runClaudeHook(in io.Reader, sock string) {
	// Drain stdin (Claude pipes the event JSON and can block on the write if we
	// don't read it), bounded so a pathological payload can't exhaust memory.
	input, _ := io.ReadAll(io.LimitReader(in, 1<<20))
	if sock == "" {
		return
	}
	for _, body := range adapters.ClaudeHookBodies(input) {
		if err := postClaudeHookEvent(sock, body); err != nil {
			// Keep stdout empty and exit successfully: diagnostics must not
			// become model context or prevent Claude from continuing.
			fmt.Fprintf(os.Stderr, "gmux: Claude hook delivery failed: %v\n", err)
		}
	}
}

// postClaudeHookEvent POSTs one event body to the runner's /hook/event over the
// Unix socket. Transport and HTTP errors are diagnostic only.
func postClaudeHookEvent(sock string, body []byte) error {
	client := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", sock)
			},
		},
	}
	resp, err := client.Post("http://unix/hook/event", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("runner at %s returned HTTP %d", sock, resp.StatusCode)
	}
	return nil
}
