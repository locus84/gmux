package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/gmuxapp/gmux/services/gmuxd/internal/centralstore"
	pushpkg "github.com/gmuxapp/gmux/services/gmuxd/internal/push"
)

func readPushJSON(r *http.Request, limit int64, dst any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, limit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("request must contain one JSON object")
	}
	return nil
}

func registerPushRoutes(mux *http.ServeMux, manager *pushpkg.Manager, store *centralstore.Store) {
	available := func(w http.ResponseWriter) bool {
		if manager == nil {
			writeError(w, http.StatusServiceUnavailable, "push_unavailable", "web push is unavailable")
			return false
		}
		return true
	}
	mux.HandleFunc("GET /v1/push/vapid-public-key", func(w http.ResponseWriter, r *http.Request) {
		if !available(w) {
			return
		}
		key, err := manager.PublicKey()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "failed to load push key")
			return
		}
		writeJSON(w, map[string]any{"ok": true, "data": map[string]string{"public_key": key}})
	})
	mux.HandleFunc("POST /v1/push/lookup", func(w http.ResponseWriter, r *http.Request) {
		if !available(w) {
			return
		}
		var req struct {
			Endpoint string `json:"endpoint"`
		}
		if err := readPushJSON(r, 4096, &req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		sub, found, err := manager.Lookup(req.Endpoint)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "failed to load push subscription")
			return
		}
		writeJSON(w, map[string]any{"ok": true, "data": map[string]any{"found": found, "subscription": sub}})
	})
	mux.HandleFunc("POST /v1/push/subscribe", func(w http.ResponseWriter, r *http.Request) {
		if !available(w) {
			return
		}
		var req struct {
			Subscription struct {
				Endpoint string       `json:"endpoint"`
				Keys     pushpkg.Keys `json:"keys"`
			} `json:"subscription"`
			Projects    []string `json:"projects"`
			DeviceLabel string   `json:"device_label"`
		}
		if err := readPushJSON(r, 64*1024, &req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		sub, err := manager.Upsert(pushpkg.Subscription{Endpoint: req.Subscription.Endpoint, Keys: req.Subscription.Keys, Projects: localPushProjects(r, store, req.Projects), DeviceLabel: req.DeviceLabel, UserAgent: r.UserAgent()})
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true, "data": sub})
	})
	mux.HandleFunc("PATCH /v1/push/subscription", func(w http.ResponseWriter, r *http.Request) {
		if !available(w) {
			return
		}
		var req struct {
			Endpoint string   `json:"endpoint"`
			Projects []string `json:"projects"`
		}
		if err := readPushJSON(r, 64*1024, &req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		sub, found, err := manager.UpdateProjects(req.Endpoint, localPushProjects(r, store, req.Projects))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "failed to update push subscription")
			return
		}
		if !found {
			writeError(w, http.StatusNotFound, "not_found", "push subscription not found")
			return
		}
		writeJSON(w, map[string]any{"ok": true, "data": sub})
	})
	mux.HandleFunc("DELETE /v1/push/subscription", func(w http.ResponseWriter, r *http.Request) {
		if !available(w) {
			return
		}
		var req struct {
			Endpoint string `json:"endpoint"`
		}
		if err := readPushJSON(r, 4096, &req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		if err := manager.Delete(req.Endpoint); err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "failed to remove push subscription")
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})
}

func localPushProjects(r *http.Request, store *centralstore.Store, requested []string) []string {
	catalog, err := store.ListProjectCatalog(r.Context())
	if err != nil {
		log.Printf("push: projects load: %v", err)
		return nil
	}
	owned := make(map[string]bool)
	for _, project := range catalog {
		if project.Kind == centralstore.ProjectEntryOwned {
			owned[project.Slug] = true
		}
	}
	seen := make(map[string]bool)
	out := make([]string, 0, len(requested))
	for _, slug := range requested {
		slug = strings.TrimSpace(slug)
		if owned[slug] && !seen[slug] {
			seen[slug] = true
			out = append(out, slug)
		}
	}
	return out
}
