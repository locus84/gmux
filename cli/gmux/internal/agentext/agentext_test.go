package agentext

import (
	"os"
	"strings"
	"testing"
)

func TestPathMaterializesReadableExtension(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	p, err := Path()
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if !strings.HasSuffix(p, ".mjs") {
		t.Errorf("expected .mjs path, got %q", p)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read materialized ext: %v", err)
	}
	if !strings.Contains(string(data), "session_start") {
		t.Error("materialized extension missing session_start handler")
	}
	if !strings.Contains(string(data), "session_info_changed") {
		t.Error("materialized extension missing explicit-name handler")
	}
	if !strings.Contains(string(data), "agent_title") || !strings.Contains(string(data), "postQueue") {
		t.Error("materialized extension missing atomic bind title or ordered transport")
	}
	for _, required := range []string{"sendUserMessage", "before_agent_start", "agent_settled", "session_shutdown", "/hook/messages/next"} {
		if !strings.Contains(string(data), required) {
			t.Errorf("materialized extension missing semantic transport marker %q", required)
		}
	}

	// Idempotent: a second call returns the same path.
	p2, err := Path()
	if err != nil || p2 != p {
		t.Errorf("Path not idempotent: %q/%v vs %q", p2, err, p)
	}
}
