package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizePath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}

	tests := []struct {
		input string
		want  string
	}{
		{"~", home},
		{"~/dev/gmux", home + "/dev/gmux"},
		{"/opt/data", "/opt/data"},
		{"", ""},
		// Already absolute: unchanged.
		{home + "/dev/gmux", home + "/dev/gmux"},
	}
	for _, tt := range tests {
		got := NormalizePath(tt.input)
		if got != tt.want {
			t.Errorf("NormalizePath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestCanonicalizePath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	tmpWant := filepath.Clean("/tmp")
	if resolved, err := filepath.EvalSymlinks(tmpWant); err == nil {
		tmpWant = resolved
	}

	tests := []struct {
		input string
		want  string
	}{
		{home, "~"},
		{home + "/dev/gmux", "~/dev/gmux"},
		{home + "/", "~"},
		{"/opt/data", "/opt/data"},
		{"/tmp/../tmp", tmpWant},
		{"", ""},
		// Already canonical: passes through unchanged.
		{"~/dev/gmux", "~/dev/gmux"},
		{"~", "~"},
	}
	for _, tt := range tests {
		got := CanonicalizePath(tt.input)
		if got != tt.want {
			t.Errorf("CanonicalizePath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
func TestDataDir(t *testing.T) {
	t.Run("uses XDG_DATA_HOME", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", "/tmp/xdg-data")
		if got := DataDir(); got != filepath.Join("/tmp/xdg-data", "gmux") {
			t.Errorf("DataDir() = %q", got)
		}
		if got := WorktreesDir(); got != filepath.Join("/tmp/xdg-data", "gmux", "worktrees") {
			t.Errorf("WorktreesDir() = %q", got)
		}
	})

	t.Run("falls back to home data directory", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", "")
		t.Setenv("HOME", "/tmp/gmux-home")
		if got := DataDir(); got != filepath.Join("/tmp/gmux-home", ".local", "share", "gmux") {
			t.Errorf("DataDir() = %q", got)
		}
	})

	t.Run("ignores relative XDG_DATA_HOME", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", "relative/data")
		t.Setenv("HOME", "/tmp/gmux-home")
		if got := DataDir(); got != filepath.Join("/tmp/gmux-home", ".local", "share", "gmux") {
			t.Errorf("DataDir() = %q", got)
		}
	})
}

func TestStateDir(t *testing.T) {
	t.Run("GMUX_STATE_DIR overrides XDG_STATE_HOME", func(t *testing.T) {
		t.Setenv("GMUX_STATE_DIR", "/tmp/gmux-state")
		t.Setenv("XDG_STATE_HOME", "/tmp/xdg-state")
		if got := StateDir(); got != "/tmp/gmux-state" {
			t.Errorf("StateDir() = %q, want /tmp/gmux-state", got)
		}
		if got := SocketPath(); got != filepath.Join("/tmp/gmux-state", "gmuxd.sock") {
			t.Errorf("SocketPath() = %q", got)
		}
	})

	t.Run("falls back to XDG_STATE_HOME", func(t *testing.T) {
		t.Setenv("GMUX_STATE_DIR", "")
		t.Setenv("XDG_STATE_HOME", "/tmp/xdg-state")
		if got := StateDir(); got != filepath.Join("/tmp/xdg-state", "gmux") {
			t.Errorf("StateDir() = %q", got)
		}
	})
}

func TestSessionSocketDir(t *testing.T) {
	t.Run("GMUX_SOCKET_DIR overrides everything", func(t *testing.T) {
		t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
		t.Setenv("GMUX_SOCKET_DIR", "/tmp/custom-sockets")
		if got := SessionSocketDir(); got != "/tmp/custom-sockets" {
			t.Errorf("SessionSocketDir() = %q, want %q", got, "/tmp/custom-sockets")
		}
	})

	t.Run("falls back to XDG_RUNTIME_DIR", func(t *testing.T) {
		t.Setenv("GMUX_SOCKET_DIR", "")
		t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
		want := "/run/user/1000/gmux/sessions"
		if got := SessionSocketDir(); got != want {
			t.Errorf("SessionSocketDir() = %q, want %q", got, want)
		}
	})

	t.Run("per-uid temp dir when no XDG_RUNTIME_DIR", func(t *testing.T) {
		t.Setenv("GMUX_SOCKET_DIR", "")
		t.Setenv("XDG_RUNTIME_DIR", "")
		got := SessionSocketDir()
		want := filepath.Join(os.TempDir(), fmt.Sprintf("gmux-sessions-%d", os.Getuid()))
		if got != want {
			t.Errorf("SessionSocketDir() = %q, want %q", got, want)
		}
		// Must not be the old world-shared path.
		if got == "/tmp/gmux-sessions" {
			t.Errorf("SessionSocketDir() must not default to the shared /tmp/gmux-sessions")
		}
	})
}

func TestIsValidSessionID(t *testing.T) {
	valid := []string{
		"sess-abcd1234",
		"sess-0",
		"sess-claude",
		"sess-resume_1",
		"sess-codex-2",
	}
	for _, id := range valid {
		if !IsValidSessionID(id) {
			t.Errorf("IsValidSessionID(%q) = false, want true", id)
		}
	}

	invalid := []string{
		"",
		"abcd1234",       // missing prefix
		"sess-",          // empty suffix
		"sess-../escape", // path traversal
		"sess-..",        // parent dir
		"../sess-abcd",   // leading traversal
		"sess-a/b",       // separator
		`sess-a\b`,       // backslash separator
		"sess-a::b",      // folder-key separator
		"sess-a b",       // space
		"sess-a\n",       // newline
	}
	for _, id := range invalid {
		if IsValidSessionID(id) {
			t.Errorf("IsValidSessionID(%q) = true, want false", id)
		}
	}
}
