package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gmuxapp/gmux/services/gmuxd/internal/store"
)

const (
	maxExplicitTitleBytes        = 1024
	maxExplicitTitleRequestBytes = maxExplicitTitleBytes*6 + 256
)

type renameSessionRequest struct {
	Title *string `json:"title"`
}

func decodeExplicitTitle(r *http.Request) (string, error) {
	var req renameSessionRequest
	dec := json.NewDecoder(io.LimitReader(r.Body, maxExplicitTitleRequestBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return "", fmt.Errorf("invalid JSON: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return "", errors.New("invalid JSON: trailing content")
	}
	if req.Title == nil {
		return "", errors.New("title is required")
	}
	return normalizeExplicitTitle(*req.Title)
}

type liveTitleUpdater func(context.Context, string, string) error

func handleSessionRename(
	w http.ResponseWriter,
	r *http.Request,
	sessionID string,
	sessions *store.Store,
	updateLive liveTitleUpdater,
	persistDead func(store.Session),
) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "bad_request", "method not allowed")
		return
	}
	sess, ok := sessions.Get(sessionID)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "session not found")
		return
	}
	title, err := decodeExplicitTitle(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if sess.Alive {
		if sess.SocketPath == "" {
			writeError(w, http.StatusConflict, "no_socket", "alive session missing socket")
			return
		}
		if err := updateLive(r.Context(), sess.SocketPath, title); err != nil {
			writeError(w, http.StatusBadGateway, "runner_unreachable", err.Error())
			return
		}
	}
	sessions.Update(sessionID, func(current *store.Session) {
		current.ExplicitTitle = title
	})
	// Re-check after the runner round-trip: the session may have exited while
	// the rename was in flight, in which case this mutation belongs in its
	// dead snapshot.
	if updated, ok := sessions.Get(sessionID); ok && !updated.Alive && persistDead != nil {
		persistDead(updated)
	}
	writeJSON(w, map[string]any{"ok": true, "data": map[string]any{"title": title}})
}

func normalizeExplicitTitle(raw string) (string, error) {
	title := strings.TrimSpace(raw)
	if !utf8.ValidString(title) {
		return "", errors.New("title must be valid UTF-8")
	}
	if len(title) > maxExplicitTitleBytes {
		return "", fmt.Errorf("title exceeds %d bytes", maxExplicitTitleBytes)
	}
	for _, r := range title {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return "", errors.New("title must be a single line without control characters")
		}
	}
	return title, nil
}
