package main

import (
	"context"
	"io"
	"net/http"

	"github.com/gmuxapp/gmux/services/gmuxd/internal/centralstore"
	"github.com/gmuxapp/gmux/services/gmuxd/internal/sessioncoord"
)

const terminalImageResponseLimit = (8 << 20) + 1024

func validTerminalImageHash(hash string) bool {
	if len(hash) != 64 {
		return false
	}
	for _, c := range hash {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

type terminalImageSessionLookup interface {
	Session(context.Context, centralstore.SessionID) (centralstore.Session, bool, error)
}

// terminalImageHandler proxies a content-addressed object from the currently
// owning runner. It never accepts a path and never consults the filesystem.
func terminalImageHandler(w http.ResponseWriter, r *http.Request, sessionID, hash string, sessions terminalImageSessionLookup, registry *sessioncoord.Registry) {
	if !validTerminalImageHash(hash) {
		writeError(w, http.StatusBadRequest, "invalid_hash", "image hash must be 64 lowercase hexadecimal characters")
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "bad_request", "terminal image endpoint does not accept query parameters")
		return
	}
	_, found, err := sessions.Session(r.Context(), centralstore.SessionID(sessionID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "failed to load session")
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "not_found", "session not found")
		return
	}
	runtime, live := registryRuntime(registry, centralstore.SessionID(sessionID))
	if !live {
		writeError(w, http.StatusGone, "image_expired", "session runner is no longer available")
		return
	}
	serveTerminalImageFromRunner(w, r, runtime.Endpoint, hash)
}

func serveTerminalImageFromRunner(w http.ResponseWriter, r *http.Request, endpoint, hash string) {
	resp, err := runnerRequestContext(r.Context(), endpoint, http.MethodGet, "/images/"+hash)
	if err != nil {
		writeError(w, http.StatusBadGateway, "runner_unreachable", "session runner is unavailable")
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, terminalImageResponseLimit+1))
	if err != nil {
		writeError(w, http.StatusBadGateway, "runner_unreachable", "failed to read terminal image")
		return
	}
	if len(body) > terminalImageResponseLimit {
		writeError(w, http.StatusBadGateway, "bad_runner_response", "runner image response exceeded the limit")
		return
	}
	for _, name := range []string{"Content-Type", "Content-Length", "Cache-Control", "X-Content-Type-Options"} {
		if value := resp.Header.Get(name); value != "" {
			w.Header().Set(name, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(body)
}
