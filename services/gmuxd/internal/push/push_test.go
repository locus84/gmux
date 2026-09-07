package push

import (
	"os"
	"path/filepath"
	"testing"
)

func testSub(endpoint string, projects ...string) Subscription {
	return Subscription{
		Endpoint: endpoint,
		Keys: Keys{
			Auth:   "auth-key",
			P256dh: "p256dh-key",
		},
		Projects: projects,
	}
}

func TestOpenGeneratesAndPersistsVAPIDKeys(t *testing.T) {
	dir := t.TempDir()
	m, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	pub1, err := m.PublicKey()
	if err != nil {
		t.Fatalf("PublicKey: %v", err)
	}
	if pub1 == "" {
		t.Fatal("public key is empty")
	}
	if _, err := os.Stat(filepath.Join(dir, fileName)); err != nil {
		t.Fatalf("state file not written: %v", err)
	}

	m2, err := Open(dir)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	pub2, err := m2.PublicKey()
	if err != nil {
		t.Fatalf("second PublicKey: %v", err)
	}
	if pub2 != pub1 {
		t.Fatalf("public key changed across opens")
	}
}

func TestUpsertLookupAndProjectMatching(t *testing.T) {
	m, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	sub, err := m.Upsert(testSub("https://push.example/a", "web", "gmux", "web"))
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if sub.ID == "" {
		t.Fatal("missing generated id")
	}
	if got, want := sub.Projects, []string{"gmux", "web"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("normalized projects = %#v, want %#v", got, want)
	}

	looked, ok, err := m.Lookup("https://push.example/a")
	if err != nil || !ok {
		t.Fatalf("Lookup ok=%v err=%v", ok, err)
	}
	if looked.ID != sub.ID {
		t.Fatalf("lookup id = %q, want %q", looked.ID, sub.ID)
	}

	matches, err := m.Matching("gmux")
	if err != nil {
		t.Fatalf("Matching: %v", err)
	}
	if len(matches) != 1 || matches[0].Endpoint != sub.Endpoint {
		t.Fatalf("matches = %#v", matches)
	}
	matches, err = m.Matching("other")
	if err != nil {
		t.Fatalf("Matching other: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("other matches = %#v, want none", matches)
	}
}

func TestUpdateProjectsAndDelete(t *testing.T) {
	m, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := m.Upsert(testSub("https://push.example/a", "gmux")); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	updated, ok, err := m.UpdateProjects("https://push.example/a", []string{"mobile"})
	if err != nil || !ok {
		t.Fatalf("UpdateProjects ok=%v err=%v", ok, err)
	}
	if len(updated.Projects) != 1 || updated.Projects[0] != "mobile" {
		t.Fatalf("updated projects = %#v", updated.Projects)
	}

	matches, err := m.Matching("gmux")
	if err != nil {
		t.Fatalf("Matching gmux: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("gmux matches after update = %#v", matches)
	}

	if err := m.Delete("https://push.example/a"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, ok, err = m.Lookup("https://push.example/a")
	if err != nil {
		t.Fatalf("Lookup after delete: %v", err)
	}
	if ok {
		t.Fatal("subscription still exists after delete")
	}
}
