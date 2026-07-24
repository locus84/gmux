package session

import (
	"encoding/json"
	"testing"

	"github.com/gmuxapp/gmux/packages/adapter"
)

func TestNewState(t *testing.T) {
	s := New(Config{
		ID:         "sess-test",
		Command:    []string{"echo", "hello"},
		Cwd:        "/tmp",
		Kind:       "generic",
		SocketPath: "/tmp/gmux-sessions/sess-test.sock",
	})

	if s.ID != "sess-test" {
		t.Fatalf("expected 'sess-test', got %q", s.ID)
	}
	if s.Alive {
		t.Fatal("new state should not be alive")
	}
	if s.Title() != "echo hello" {
		t.Fatalf("expected 'echo hello', got %q", s.Title())
	}
}

func TestMetaAlwaysIncludesAuthoritativeExplicitLayers(t *testing.T) {
	s := New(Config{ID: "s", Kind: "shell"})
	data, err := s.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if value, ok := got["explicit_title"]; !ok || value != "" {
		t.Fatalf("explicit_title = %#v, present=%v", value, ok)
	}
	if value, ok := got["agent_title"]; !ok || value != "" {
		t.Fatalf("agent_title = %#v, present=%v", value, ok)
	}
}

func TestNewStateIncludesGitLayout(t *testing.T) {
	s := New(Config{ID: "s", Kind: "shell", GitLayout: "worktree"})
	data, err := s.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got["git_layout"] != "worktree" {
		t.Fatalf("git_layout = %v, want worktree", got["git_layout"])
	}
}

func TestNewStateRestoresInitialTitleLayers(t *testing.T) {
	s := New(Config{ID: "s", Command: []string{"pi"}, ExplicitTitle: "restored name", AgentTitle: "pi name"})
	if s.Title() != "restored name" {
		t.Fatalf("title = %q, want restored name", s.Title())
	}
	s.SetExplicitTitle("")
	if s.Title() != "pi name" {
		t.Fatalf("title = %q, want restored agent name", s.Title())
	}
}

func TestTitleFallsBackToCommandBasename(t *testing.T) {
	s := New(Config{ID: "s", Command: []string{"/usr/bin/pi"}, Kind: "pi"})
	if s.Title() != "pi" {
		t.Fatalf("expected 'pi', got %q", s.Title())
	}
}

func TestTerminalTitleOverridesAdapterTitle(t *testing.T) {
	s := New(Config{ID: "s", Command: []string{"pi"}, Kind: "pi"})

	s.SetAdapterTitle("adapter fallback")
	s.SetShellTitle("π - renamed pane - project")
	if s.Title() != "π - renamed pane - project" {
		t.Fatalf("expected terminal title to win, got %q", s.Title())
	}

	s.SetAdapterTitle("updated fallback")
	if s.Title() != "π - renamed pane - project" {
		t.Fatalf("adapter title hid terminal title: %q", s.Title())
	}
}

func TestExplicitTitlePrecedenceAndClearing(t *testing.T) {
	s := New(Config{ID: "s", Command: []string{"pi"}, Kind: "pi"})

	s.SetAdapterTitle("adapter fallback")
	s.SetShellTitle("application pane title")
	s.SetAgentTitle("에이전트 이름")
	s.SetExplicitTitle("사용자 이름")
	if s.Title() != "사용자 이름" {
		t.Fatalf("expected explicit title to win, got %q", s.Title())
	}

	s.SetShellTitle("changed application title")
	s.SetAdapterTitle("changed adapter fallback")
	if s.Title() != "사용자 이름" {
		t.Fatalf("lower-priority title replaced explicit title: %q", s.Title())
	}

	s.SetExplicitTitle("")
	if s.Title() != "에이전트 이름" {
		t.Fatalf("expected agent title after clearing explicit title, got %q", s.Title())
	}
	s.SetAgentTitle("")
	if s.Title() != "changed application title" {
		t.Fatalf("expected terminal title after clearing agent title, got %q", s.Title())
	}
	s.SetShellTitle("")
	if s.Title() != "changed adapter fallback" {
		t.Fatalf("expected adapter title after clearing terminal title, got %q", s.Title())
	}
	s.SetAdapterTitle("")
	if s.Title() != "pi" {
		t.Fatalf("expected command fallback after clearing adapter title, got %q", s.Title())
	}
}

func TestSetRunning(t *testing.T) {
	s := New(Config{ID: "s", Command: []string{"echo"}, Kind: "generic"})
	s.SetRunning(12345)

	if !s.Alive {
		t.Fatal("should be alive")
	}
	if s.Pid != 12345 {
		t.Fatalf("expected pid 12345, got %d", s.Pid)
	}
}

func TestSetExited(t *testing.T) {
	s := New(Config{ID: "s", Command: []string{"echo"}, Kind: "generic"})
	s.SetRunning(12345)
	s.SetExited(42)

	if s.Alive {
		t.Fatal("should not be alive")
	}
	if s.ExitCode == nil || *s.ExitCode != 42 {
		t.Fatalf("expected exit code 42, got %v", s.ExitCode)
	}
}

func TestSetStatus(t *testing.T) {
	s := New(Config{ID: "s", Command: []string{"echo"}, Kind: "generic"})
	s.SetStatus(&adapter.Status{Label: "thinking", Working: true})

	if s.Status == nil || s.Status.Label != "thinking" {
		t.Fatalf("expected 'thinking', got %v", s.Status)
	}
}

func TestJSONIncludesComputedTitle(t *testing.T) {
	s := New(Config{
		ID:      "sess-json",
		Command: []string{"pi"},
		Cwd:     "/home/user",
		Kind:    "pi",
	})
	s.SetShellTitle("~/dev/gmux")
	s.SetAgentTitle("agent named session")
	s.SetExplicitTitle("named session")

	data, err := s.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON error: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if parsed["title"] != "named session" {
		t.Fatalf("expected computed explicit title, got %v", parsed["title"])
	}
	if parsed["explicit_title"] != "named session" {
		t.Fatalf("expected explicit_title, got %v", parsed["explicit_title"])
	}
	if parsed["agent_title"] != "agent named session" {
		t.Fatalf("expected agent_title, got %v", parsed["agent_title"])
	}
	if parsed["shell_title"] != "~/dev/gmux" {
		t.Fatalf("expected shell_title, got %v", parsed["shell_title"])
	}
}

func TestSubscribeEvents(t *testing.T) {
	s := New(Config{ID: "s", Command: []string{"echo"}, Kind: "generic"})
	ch := s.Subscribe()
	defer s.Unsubscribe(ch)

	s.SetStatus(&adapter.Status{Label: "test"})

	evt := <-ch
	if evt.Type != "status" {
		t.Fatalf("expected 'status', got %q", evt.Type)
	}
}

func TestUnsubscribe(t *testing.T) {
	s := New(Config{ID: "s", Command: []string{"echo"}, Kind: "generic"})
	ch := s.Subscribe()
	s.Unsubscribe(ch)

	_, ok := <-ch
	if ok {
		t.Fatal("channel should be closed")
	}
}

func TestEmitActivityThrottles(t *testing.T) {
	s := New(Config{ID: "s", Command: []string{"echo"}, Kind: "generic"})
	ch := s.Subscribe()
	defer s.Unsubscribe(ch)

	// First call should emit.
	s.EmitActivity()
	select {
	case evt := <-ch:
		if evt.Type != "activity" {
			t.Fatalf("expected 'activity' event, got %q", evt.Type)
		}
	default:
		t.Fatal("expected activity event from first call")
	}

	// Immediate second call should be throttled (no event).
	s.EmitActivity()
	select {
	case evt := <-ch:
		t.Fatalf("expected no event (throttled), got %q", evt.Type)
	default:
		// good, throttled
	}
}
