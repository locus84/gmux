package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gmuxapp/gmux/services/gmuxd/internal/centralstore"
	pushpkg "github.com/gmuxapp/gmux/services/gmuxd/internal/push"
)

func TestPushSubscribeKeepsOnlyOwnedProjects(t *testing.T) {
	ctx := context.Background()
	store, err := centralstore.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, _, err := store.ReplaceProjectCatalog(ctx, []centralstore.ProjectEntrySpec{
		{Owned: &centralstore.OwnedProjectSpec{Slug: "local", Rules: []centralstore.MatchRule{{Path: "/repo"}}}},
		{Reference: &centralstore.ProjectReference{PeerKey: "peer", Slug: "remote"}},
	}, 1); err != nil {
		t.Fatal(err)
	}
	manager, err := pushpkg.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	registerPushRoutes(mux, manager, store)
	body, _ := json.Marshal(map[string]any{
		"subscription": map[string]any{"endpoint": "https://push.example/sub", "keys": map[string]string{"auth": "auth", "p256dh": "key"}},
		"projects":     []string{"local", "remote", "unknown", "local"},
	})
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/push/subscribe", bytes.NewReader(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	sub, found, err := manager.Lookup("https://push.example/sub")
	if err != nil || !found {
		t.Fatalf("lookup found=%v err=%v", found, err)
	}
	if len(sub.Projects) != 1 || sub.Projects[0] != "local" {
		t.Fatalf("projects=%v", sub.Projects)
	}
}
