package adapters

import (
	"testing"

	"github.com/gmuxapp/gmux/packages/adapter"
)

func TestHermesName(t *testing.T) {
	if NewHermes().Name() != "hermes" {
		t.Fatal("expected 'hermes'")
	}
}

func TestHermesMatchDirect(t *testing.T) {
	if !NewHermes().Match([]string{"hermes"}) {
		t.Fatal("should match 'hermes'")
	}
}

func TestHermesMatchFullPath(t *testing.T) {
	h := NewHermes()
	if !h.Match([]string{"/usr/bin/hermes"}) {
		t.Fatal("should match full path")
	}
	if !h.Match([]string{"/home/user/.local/bin/hermes"}) {
		t.Fatal("should match ~/.local/bin path")
	}
}

func TestHermesMatchWrapped(t *testing.T) {
	h := NewHermes()
	if !h.Match([]string{"env", "hermes"}) {
		t.Fatal("should match 'env hermes'")
	}
	if !h.Match([]string{"uvx", "hermes", "--flag"}) {
		t.Fatal("should match 'uvx hermes --flag'")
	}
}

func TestHermesMatchStopsAtDoubleDash(t *testing.T) {
	if NewHermes().Match([]string{"echo", "--", "hermes"}) {
		t.Fatal("should not match 'hermes' after '--'")
	}
}

func TestHermesNoMatchOther(t *testing.T) {
	h := NewHermes()
	if h.Match([]string{"pi"}) {
		t.Fatal("should not match pi")
	}
	if h.Match([]string{"hermes-agent"}) {
		t.Fatal("should not match hermes-agent")
	}
}

func TestHermesEnvNil(t *testing.T) {
	if env := NewHermes().Env(adapter.EnvContext{}); env != nil {
		t.Fatalf("expected nil, got %v", env)
	}
}

func TestHermesMonitorNoOp(t *testing.T) {
	if NewHermes().Monitor([]byte("some output")) != nil {
		t.Fatal("should return nil")
	}
}

func TestHermesImplementsLaunchable(t *testing.T) {
	var a adapter.Adapter = NewHermes()
	if _, ok := a.(adapter.Launchable); !ok {
		t.Fatal("should implement Launchable")
	}
}

func TestHermesLaunchers(t *testing.T) {
	launchers := NewHermes().Launchers()
	if len(launchers) != 1 {
		t.Fatalf("expected 1 launcher, got %d", len(launchers))
	}
	l := launchers[0]
	if l.ID != "hermes" {
		t.Errorf("expected id 'hermes', got %q", l.ID)
	}
	if l.Label != "Hermes" {
		t.Errorf("expected label 'Hermes', got %q", l.Label)
	}
	if len(l.Command) != 1 || l.Command[0] != "hermes" {
		t.Errorf("unexpected command: %v", l.Command)
	}
}
