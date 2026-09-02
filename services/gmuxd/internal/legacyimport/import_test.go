package legacyimport

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gmuxapp/gmux/services/gmuxd/internal/conversations"
)

func TestLoadRestoresV1MetadataConversationFallbacksAndOrder(t *testing.T) {
	dir := t.TempDir()
	projects := `{
  "version": 3,
  "items": [
    {"slug":"repo","match":[{"path":"/repo"}],"sessions":["meta-title","conversation-title","stale"]},
    {"slug":"remote","peer":"laptop","node_id":"node-1"}
  ]
}`
	if err := os.WriteFile(filepath.Join(dir, legacyProjectsFile), []byte(projects), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, legacyPeersFile), []byte(`[{"name":"laptop","url":"https://laptop.example","token":"secret","node_id":"node-1"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	metaDir := filepath.Join(dir, "sessions", "sess-0e456a30")
	if err := os.MkdirAll(metaDir, 0o700); err != nil {
		t.Fatal(err)
	}
	meta := `{
  "id":"sess-0e456a30","created_at":"2026-08-07T03:47:41Z","command":["pi","--session","ref-meta"],
  "cwd":"/repo","kind":"pi","alive":false,"exit_code":0,"exited_at":"2026-08-07T03:51:03Z",
  "title":"Meta title","status":null,"unread":true,"last_activity_at":"2026-08-07T03:50:00Z",
  "slug":"meta-title","session_file":"ref-meta","adapter_title":"Meta title"
}`
	if err := os.WriteFile(filepath.Join(metaDir, "meta.json"), []byte(meta), 0o600); err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 8, 8, 1, 2, 3, 0, time.UTC)
	input, report, err := Load(dir, []conversations.Info{
		{ConversationID: "conv-meta", Key: "meta-title", Slug: "meta-title", Adapter: "pi", Ref: "ref-meta", Title: "Meta title", Cwd: "/repo", Created: created},
		{ConversationID: "conv-only", Key: "conversation-title", Slug: "conversation-title", Adapter: "pi", Ref: "ref-only", Title: "Conversation title", Cwd: "/repo", ResumeCommand: []string{"pi", "--resume"}, Created: created},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.MetaSessions != 1 || report.ConversationSessions != 1 || report.UnresolvedSlots != 1 {
		t.Fatalf("report: %+v", report)
	}
	if len(input.Sessions) != 2 || len(input.Projects) != 2 || len(input.Placements) != 2 || len(input.Peers) != 1 {
		t.Fatalf("import: %+v", input)
	}
	if input.Sessions[0].ID != "sess-0e456a30" || input.Sessions[0].Adapter != "pi" || input.Sessions[0].ConversationRef != "ref-meta" {
		t.Fatalf("meta session: %+v", input.Sessions[0])
	}
	wantActivity := time.Date(2026, 8, 7, 3, 50, 0, 0, time.UTC).UnixMilli()
	if input.Sessions[0].LastActivityAt == nil || int64(*input.Sessions[0].LastActivityAt) != wantActivity {
		t.Fatalf("legacy activity not preserved: %+v", input.Sessions[0].LastActivityAt)
	}
	if input.Sessions[1].ID != "conv-only" || input.Sessions[1].ConversationRef != "ref-only" {
		t.Fatalf("conversation session: %+v", input.Sessions[1])
	}
	if input.Placements[0].SessionID != "sess-0e456a30" || input.Placements[1].SessionID != "conv-only" {
		t.Fatalf("placement order: %+v", input.Placements)
	}
	if input.Projects[1].Reference == nil || input.Projects[1].Reference.NodeID != "node-1" {
		t.Fatalf("reference: %+v", input.Projects[1])
	}
	if input.Peers[0].Name != "laptop" || input.Peers[0].Token != "secret" || input.Peers[0].NodeID != "node-1" {
		t.Fatalf("peer: %+v", input.Peers[0])
	}
}

func TestExistsDoesNotTreatMissingLegacyStateAsImport(t *testing.T) {
	exists, err := Exists(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("missing state reported as present")
	}
}
