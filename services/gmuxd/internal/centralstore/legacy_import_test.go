package centralstore

import (
	"context"
	"errors"
	"testing"
)

func TestImportLegacyRestoresCatalogSessionsAndFamilyOrderAtomically(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	parent := SessionID("parent")
	input := LegacyImport{
		Sessions: []NewSession{
			{ID: parent, Adapter: "pi", CWD: "/repo", Slug: "parent", CreatedAt: 1},
			// CreatedAt is deliberately opposite the legacy placement order.
			{ID: "child-a", Adapter: "pi", CWD: "/repo", Slug: "child-a", CreatedAt: 3, ParentSessionID: &parent},
			{ID: "child-b", Adapter: "pi", CWD: "/repo", Slug: "child-b", CreatedAt: 2, ParentSessionID: &parent},
		},
		Projects:   []ProjectEntrySpec{{Owned: &OwnedProjectSpec{Slug: "repo", Rules: []MatchRule{{Path: "/repo"}}}}},
		Placements: []LegacyPlacement{{ProjectIndex: 0, SessionID: "parent"}, {ProjectIndex: 0, SessionID: "child-a"}, {ProjectIndex: 0, SessionID: "child-b"}},
		Peers:      []ManualPeerSpec{{Name: "laptop", URL: "https://laptop.example", Token: "secret", NodeID: "node-1"}},
	}
	result, err := store.ImportLegacy(ctx, input, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Imported || result.Sessions != 3 || result.Projects != 1 || result.Placements != 3 || result.Peers != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	snapshot, err := store.ReadSnapshot(ctx, SnapshotQuery{IncludeSessions: true, IncludeProjects: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Sessions) != 3 || len(snapshot.Projects) != 1 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	peers, err := store.ListManualPeers(ctx)
	if err != nil || len(peers) != 1 || peers[0].Name != "laptop" || peers[0].Token != "secret" {
		t.Fatalf("imported peers: %+v, %v", peers, err)
	}
	byID := map[SessionID]SessionView{}
	for _, session := range snapshot.Sessions {
		byID[session.ID] = session
	}
	if got := byID[parent].Placement; got == nil || got.SiblingScope != "r" || got.Position != 0 {
		t.Fatalf("parent placement: %+v", got)
	}
	if got := byID["child-a"].Placement; got == nil || got.SiblingScope != "c:l:parent" || got.Position != 0 {
		t.Fatalf("first child placement: %+v", got)
	}
	if got := byID["child-b"].Placement; got == nil || got.SiblingScope != "c:l:parent" || got.Position != 1 {
		t.Fatalf("second child placement: %+v", got)
	}
	eligible, err := store.LegacyImportEligible(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if eligible {
		t.Fatal("completed import remained eligible")
	}
	if _, err := store.ImportLegacy(ctx, input, 11); !errors.Is(err, ErrLegacyImportNotEmpty) {
		t.Fatalf("second import: %v", err)
	}
}

func TestImportLegacyValidationLeavesStoreEmpty(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_, err = store.ImportLegacy(ctx, LegacyImport{
		Sessions:   []NewSession{{ID: "one", Adapter: "shell", CreatedAt: 1}},
		Projects:   []ProjectEntrySpec{{Owned: &OwnedProjectSpec{Slug: "repo", Rules: []MatchRule{{Path: "/repo"}}}}},
		Placements: []LegacyPlacement{{ProjectIndex: 0, SessionID: "missing"}},
	}, 10)
	if err == nil {
		t.Fatal("expected validation failure")
	}
	eligible, checkErr := store.LegacyImportEligible(ctx)
	if checkErr != nil {
		t.Fatal(checkErr)
	}
	if !eligible {
		t.Fatal("failed import changed the store")
	}
}
