package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gmuxapp/gmux/services/gmuxd/internal/centralstore"
	"github.com/gmuxapp/gmux/services/gmuxd/internal/config"
	"github.com/gmuxapp/gmux/services/gmuxd/internal/peering"
	"github.com/gmuxapp/gmux/services/gmuxd/internal/sessioncoord"
	"github.com/gmuxapp/gmux/services/gmuxd/internal/sessionmeta"
	central "github.com/gmuxapp/gmux/services/gmuxd/internal/snapshot/central"
)

func launchedFromExport(t *testing.T, store *centralstore.Store, id centralstore.SessionID) string {
	t.Helper()
	state, err := store.ExportState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range state.Sessions {
		if session.ID == id && session.LaunchedFromSessionID != nil {
			return string(*session.LaunchedFromSessionID)
		}
	}
	return ""
}

func TestPeerSessionDismissRefusesEveryLeafParameterSpelling(t *testing.T) {
	pm := peering.NewProjectionManager([]config.PeerConfig{{Name: "box", URL: "http://127.0.0.1:1"}}, "self", nil, peering.EventHooks{})
	for _, query := range []string{"?leaf=1", "?leaf=0", "?leaf=true", "?leaf"} {
		recorder := httptest.NewRecorder()
		handleCentralSessionAction(recorder, httptest.NewRequest(http.MethodPost, "/v1/sessions/abc@box/dismiss"+query, nil), nil, newSSEFanout(), nil, pm, nil, "", nil)
		if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), codeLocalOnly) {
			t.Fatalf("query=%q response=%d %s", query, recorder.Code, recorder.Body.String())
		}
	}
}

func TestSessionDismissHTTPLeafScope(t *testing.T) {
	for _, tc := range []struct {
		name, query string
		wantStatus  int
		wantCode    string
	}{
		{name: "recursive default", wantStatus: http.StatusOK},
		{name: "leaf refuses descendants", query: "?leaf=1", wantStatus: http.StatusConflict, wantCode: "has_children"},
		{name: "invalid leaf", query: "?leaf=0", wantStatus: http.StatusBadRequest, wantCode: "bad_request"},
		{name: "unknown query", query: "?other=1", wantStatus: http.StatusBadRequest, wantCode: "bad_request"},
		{name: "duplicate leaf", query: "?leaf=1&leaf=1", wantStatus: http.StatusBadRequest, wantCode: "bad_request"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store, err := centralstore.Open(ctx, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if _, _, err := store.InsertSession(ctx, centralstore.NewSession{ID: "parent", Adapter: "shell", Command: []string{"sh"}, CreatedAt: 1}); err != nil {
				t.Fatal(err)
			}
			parent := centralstore.SessionID("parent")
			if _, _, err := store.InsertSession(ctx, centralstore.NewSession{ID: "child", ParentSessionID: &parent, Adapter: "shell", Command: []string{"sh"}, CreatedAt: 2}); err != nil {
				t.Fatal(err)
			}
			registry := sessioncoord.NewRegistry()
			coord := sessioncoord.New(registry, nil, store, nil, nil)
			boot := &Bootstrap{Store: store, Registry: registry, Coordinator: coord, Composer: central.New(store, nil, nil)}
			recorder := httptest.NewRecorder()
			handleCentralSessionAction(recorder, httptest.NewRequest(http.MethodPost, "/v1/sessions/parent/dismiss"+tc.query, nil), boot, newSSEFanout(), nil, nil, sessionmeta.New(t.TempDir()), "", nil)
			if recorder.Code != tc.wantStatus || tc.wantCode != "" && !strings.Contains(recorder.Body.String(), tc.wantCode) {
				t.Fatalf("response=%d %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestSessionReparentHTTPValidation(t *testing.T) {
	ctx := context.Background()
	store, err := centralstore.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, id := range []centralstore.SessionID{"a", "b"} {
		if _, _, err := store.InsertSession(ctx, centralstore.NewSession{ID: id, Adapter: "shell", Command: []string{"sh"}, CreatedAt: 1}); err != nil {
			t.Fatal(err)
		}
	}
	coord := sessioncoord.New(nil, nil, store, nil, nil)
	boot := &Bootstrap{Store: store, Coordinator: coord, Composer: central.New(store, nil, nil)}
	request := func(id, body string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		handleCentralSessionAction(recorder, httptest.NewRequest(http.MethodPost, "/v1/sessions/"+id+"/reparent", strings.NewReader(body)), boot, newSSEFanout(), nil, nil, nil, "", nil)
		return recorder
	}
	if response := request("a", `{"parent_session_id":"a"}`); response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "self_parent") {
		t.Fatalf("self response=%d %s", response.Code, response.Body.String())
	}
	if response := request("a", `{"parent_session_id":"missing"}`); response.Code != http.StatusNotFound {
		t.Fatalf("missing response=%d %s", response.Code, response.Body.String())
	}
	if response := request("a", `{}`); response.Code != http.StatusBadRequest {
		t.Fatalf("missing field response=%d %s", response.Code, response.Body.String())
	}
	if response := request("a", `{"parent_session_id":"b"}`); response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	if response := request("b", `{"parent_session_id":"a"}`); response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "parent_cycle") {
		t.Fatalf("cycle response=%d %s", response.Code, response.Body.String())
	}
}
