package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gmuxapp/gmux/services/gmuxd/internal/store"
)

func TestHandleSessionRenameLiveAndDead(t *testing.T) {
	t.Run("live updates runner and cache", func(t *testing.T) {
		sessions := store.New()
		sessions.Upsert(store.Session{ID: "sess-live", Kind: "shell", Alive: true, SocketPath: "/tmp/live.sock", ShellTitle: "app"})
		var gotSocket, gotTitle string
		update := func(_ context.Context, socket, title string) error {
			gotSocket, gotTitle = socket, title
			return nil
		}
		r := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess-live/rename", strings.NewReader(`{"title":"named"}`))
		w := httptest.NewRecorder()
		handleSessionRename(w, r, "sess-live", sessions, update, nil)
		got, _ := sessions.Get("sess-live")
		if w.Code != http.StatusOK || gotSocket != "/tmp/live.sock" || gotTitle != "named" || got.Title != "named" {
			t.Fatalf("code=%d socket=%q update=%q resolved=%q", w.Code, gotSocket, gotTitle, got.Title)
		}
	})

	t.Run("dead persists and clear reveals fallback", func(t *testing.T) {
		sessions := store.New()
		sessions.Upsert(store.Session{ID: "sess-dead", Kind: "shell", ExplicitTitle: "old", ShellTitle: "app"})
		var persisted store.Session
		r := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess-dead/rename", strings.NewReader(`{"title":""}`))
		w := httptest.NewRecorder()
		handleSessionRename(w, r, "sess-dead", sessions, func(context.Context, string, string) error {
			t.Fatal("dead rename contacted runner")
			return nil
		}, func(sess store.Session) { persisted = sess })
		got, _ := sessions.Get("sess-dead")
		if w.Code != http.StatusOK || got.Title != "app" || persisted.Title != "app" {
			t.Fatalf("code=%d resolved=%q persisted=%q", w.Code, got.Title, persisted.Title)
		}
	})

	t.Run("runner failure leaves cache unchanged", func(t *testing.T) {
		sessions := store.New()
		sessions.Upsert(store.Session{ID: "sess-live", Kind: "shell", Alive: true, SocketPath: "/tmp/live.sock", ShellTitle: "app"})
		r := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess-live/rename", strings.NewReader(`{"title":"named"}`))
		w := httptest.NewRecorder()
		handleSessionRename(w, r, "sess-live", sessions, func(context.Context, string, string) error {
			return errors.New("offline")
		}, nil)
		got, _ := sessions.Get("sess-live")
		if w.Code != http.StatusBadGateway || got.ExplicitTitle != "" || got.Title != "app" {
			t.Fatalf("code=%d explicit=%q title=%q", w.Code, got.ExplicitTitle, got.Title)
		}
	})
}

func TestDecodeExplicitTitle(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		want    string
		wantErr bool
	}{
		{name: "unicode and trim", body: `{"title":"  작업 이름  "}`, want: "작업 이름"},
		{name: "clear", body: `{"title":""}`, want: ""},
		{name: "missing", body: `{}`, wantErr: true},
		{name: "control", body: `{"title":"bad\nname"}`, wantErr: true},
		{name: "unicode line separator", body: `{"title":"bad name"}`, wantErr: true},
		{name: "trailing JSON", body: `{"title":"ok"} {}`, wantErr: true},
		{name: "oversized", body: `{"title":"` + strings.Repeat("x", maxExplicitTitleBytes+1) + `"}`, wantErr: true},
		{name: "unknown field", body: `{"title":"ok","slug":"no"}`, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/v1/sessions/s/rename", strings.NewReader(tc.body))
			got, err := decodeExplicitTitle(r)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("title = %q, want %q", got, tc.want)
			}
		})
	}
}
