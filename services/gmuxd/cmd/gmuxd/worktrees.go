package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gmuxapp/gmux/packages/paths"
	"github.com/gmuxapp/gmux/packages/workspace"
	"github.com/gmuxapp/gmux/services/gmuxd/internal/centralstore"
)

const projectWorktreeTimeout = 5 * time.Second
const projectWorktreeRemoveTimeout = 30 * time.Second
const projectWorktreeCreateTimeout = 60 * time.Second
const projectWorktreeRequestLimit = 16 * 1024

// worktreeLifecycleMu prevents local launch/resume registration from racing a
// worktree safety check and removal. Peer launches are guarded by their owning
// daemon after forwarding.
var (
	// mutationMu serializes create/delete Git operations. lifecycleMu is
	// narrower: it closes only the registration/removal safety boundary, so
	// ordinary registration never waits behind worktree creation.
	worktreeMutationMu       sync.Mutex
	worktreeLifecycleMu      sync.RWMutex
	pendingWorktreeLaunchSeq uint64
	pendingWorktreeLaunches  = map[uint64]string{}
)

// Call with worktreeLifecycleMu held for writing.
func reserveWorktreeLaunch(cwd string) uint64 {
	pendingWorktreeLaunchSeq++
	pendingWorktreeLaunches[pendingWorktreeLaunchSeq] = canonicalFilesystemPath(cwd)
	return pendingWorktreeLaunchSeq
}

func releaseWorktreeLaunch(token uint64) {
	worktreeLifecycleMu.Lock()
	delete(pendingWorktreeLaunches, token)
	worktreeLifecycleMu.Unlock()
}

// Call with worktreeLifecycleMu held for reading or writing.
func hasPendingWorktreeLaunch(target string) bool {
	for _, cwd := range pendingWorktreeLaunches {
		if pathInsideWorktree(target, cwd) {
			return true
		}
	}
	return false
}

type projectWorktree struct {
	workspace.Worktree
	Primary bool `json:"primary"`
}

type projectWorktrees struct {
	ProjectSlug string            `json:"project_slug"`
	PrimaryPath string            `json:"primary_path"`
	Worktrees   []projectWorktree `json:"worktrees"`
}

func projectWorktreesHandler(w http.ResponseWriter, r *http.Request, slug string, sessions *centralstore.Store) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "bad_request", "method not allowed")
		return
	}

	root, err := projectFilesystemRoot(r.Context(), slug, sessions, true)
	if err != nil {
		writeWorkspaceFileError(w, err)
		return
	}
	root = paths.NormalizePath(strings.TrimSpace(root))
	primaryPath := paths.CanonicalizePath(root)
	if detected := workspace.Detect(root); detected.Root != "" {
		primaryPath = paths.CanonicalizePath(detected.Root)
		if detected.GitLayout == "" {
			writeProjectWorktrees(w, slug, primaryPath, nil)
			return
		}
	} else {
		// Projects do not have to be Git repositories. They still get a
		// primary checkout row, just no linked Git worktrees.
		writeProjectWorktrees(w, slug, primaryPath, nil)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), projectWorktreeTimeout)
	defer cancel()
	items, err := workspace.ListWorktreesContext(ctx, root)
	if err != nil {
		if ctx.Err() != nil {
			writeError(w, http.StatusGatewayTimeout, "unavailable", "timed out listing project worktrees")
			return
		}
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if len(items) == 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "Git returned no worktrees")
		return
	}

	result := make([]projectWorktree, len(items))
	for i, item := range items {
		item.Path = paths.CanonicalizePath(item.Path)
		result[i] = projectWorktree{Worktree: item, Primary: item.Path == primaryPath}
	}
	// Git documents the main worktree as the first record. Keep an explicit
	// primary even when an unusual repository layout defeats Detect.
	if !hasPrimaryWorktree(result) {
		result[0].Primary = true
		primaryPath = result[0].Path
	}

	writeProjectWorktrees(w, slug, primaryPath, result)
}

func projectWorktreeCreateHandler(w http.ResponseWriter, r *http.Request, slug string, sessions *centralstore.Store) {
	var req struct {
		Branch string `json:"branch"`
		Base   string `json:"base,omitempty"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, projectWorktreeRequestLimit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid worktree request")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, "bad_request", "request must contain one JSON object")
		return
	}
	req.Branch = strings.TrimSpace(req.Branch)
	req.Base = strings.TrimSpace(req.Base)
	if req.Branch == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "branch is required")
		return
	}
	if len(req.Branch) > 255 || len(req.Base) > 1024 {
		writeError(w, http.StatusBadRequest, "bad_request", "branch or base is too long")
		return
	}

	worktreeMutationMu.Lock()
	defer worktreeMutationMu.Unlock()
	root, err := projectFilesystemRoot(r.Context(), slug, sessions, true)
	if err != nil {
		writeWorkspaceFileError(w, err)
		return
	}
	root = paths.NormalizePath(strings.TrimSpace(root))
	if detected := workspace.Detect(root); detected.Root == "" || detected.GitLayout == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "project is not a Git repository")
		return
	}

	// Once creation begins, finish independently of a browser disconnect. A
	// lost response can then be recovered by refreshing the inventory.
	ctx, cancel := context.WithTimeout(context.Background(), projectWorktreeCreateTimeout)
	defer cancel()
	created, err := workspace.CreateWorktreeContext(ctx, workspace.CreateWorktreeOptions{
		Repository:  root,
		Branch:      req.Branch,
		Base:        req.Base,
		ManagedRoot: paths.WorktreesDir(),
	})
	if err != nil {
		if ctx.Err() != nil {
			writeError(w, http.StatusGatewayTimeout, "unavailable", "worktree creation timed out; refresh before retrying")
			return
		}
		writeProjectWorktreeCreateError(w, err)
		return
	}
	created.Path = paths.CanonicalizePath(created.Path)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]any{"ok": true, "data": map[string]any{
		"project_slug": slug,
		"worktree":     projectWorktree{Worktree: created, Primary: false},
	}})
}

func projectWorktreeDeleteHandler(w http.ResponseWriter, r *http.Request, slug string, sessions *centralstore.Store) {
	var req struct {
		Path string `json:"path"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, projectWorktreeRequestLimit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil || strings.TrimSpace(req.Path) == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "path is required")
		return
	}

	worktreeMutationMu.Lock()
	defer worktreeMutationMu.Unlock()
	worktreeLifecycleMu.Lock()
	defer worktreeLifecycleMu.Unlock()

	root, err := projectFilesystemRoot(r.Context(), slug, sessions, true)
	if err != nil {
		writeWorkspaceFileError(w, err)
		return
	}
	root = paths.NormalizePath(strings.TrimSpace(root))
	target := canonicalFilesystemPath(strings.TrimSpace(req.Path))

	ctx, cancel := context.WithTimeout(r.Context(), projectWorktreeTimeout)
	defer cancel()
	items, err := workspace.ListWorktreesContext(ctx, root)
	if err != nil {
		writeProjectWorktreeCommandError(w, ctx, err)
		return
	}

	primaryPath := canonicalFilesystemPath(root)
	if detected := workspace.Detect(root); detected.Root != "" {
		primaryPath = canonicalFilesystemPath(detected.Root)
	}
	primaryListed := false
	var selected *workspace.Worktree
	for i := range items {
		itemPath := canonicalFilesystemPath(items[i].Path)
		if itemPath == primaryPath {
			primaryListed = true
		}
		if itemPath == target {
			selected = &items[i]
		}
	}
	if !primaryListed && len(items) > 0 {
		primaryPath = canonicalFilesystemPath(items[0].Path)
	}
	if selected == nil {
		writeError(w, http.StatusNotFound, "not_found", "worktree is not listed for this project")
		return
	}
	if target == primaryPath || target == canonicalFilesystemPath(root) {
		writeError(w, http.StatusConflict, "conflict", "the primary checkout or configured project checkout cannot be removed")
		return
	}
	if selected.Bare || selected.Detached {
		writeError(w, http.StatusConflict, "conflict", "bare or detached worktrees cannot be removed from gmux")
		return
	}
	if selected.Locked {
		message := "worktree is locked"
		if selected.LockReason != "" {
			message += ": " + selected.LockReason
		}
		writeError(w, http.StatusConflict, "conflict", message)
		return
	}
	if selected.Prunable {
		writeError(w, http.StatusConflict, "conflict", "worktree metadata is prunable; repair it with Git first")
		return
	}
	sessionRows, err := sessions.ListSessions(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "failed to list sessions")
		return
	}
	for _, session := range sessionRows {
		if session.DismissedAt != nil {
			continue
		}
		if pathInsideWorktree(target, paths.NormalizePath(session.CWD)) ||
			pathInsideWorktree(target, paths.NormalizePath(session.WorkspaceRoot)) {
			writeError(w, http.StatusConflict, "conflict", "worktree has a live or resumable session; dismiss it first")
			return
		}
	}
	// A web launch reservation begins before fork and lasts until the runner
	// exits. Normally the durable row above gives the actionable dismissal
	// error; this guard covers only the spawn-to-registration gap.
	if hasPendingWorktreeLaunch(target) {
		writeError(w, http.StatusConflict, "conflict", "worktree has a session launch in progress")
		return
	}
	dirty, err := workspace.WorktreeDirtyContext(ctx, target)
	if err != nil {
		writeProjectWorktreeCommandError(w, ctx, err)
		return
	}
	if dirty {
		writeError(w, http.StatusConflict, "conflict", "worktree has uncommitted, untracked, or ignored files")
		return
	}
	// Once Git starts removing files, finish independently of a browser
	// disconnect so a mobile navigation cannot leave a half-removed checkout.
	removeCtx, removeCancel := context.WithTimeout(context.Background(), projectWorktreeRemoveTimeout)
	defer removeCancel()
	if err := workspace.RemoveWorktreeContext(removeCtx, root, target); err != nil {
		writeProjectWorktreeCommandError(w, removeCtx, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "data": map[string]string{
		"project_slug": slug,
		"removed_path": paths.CanonicalizePath(target),
	}})
}

func writeProjectWorktreeCreateError(w http.ResponseWriter, err error) {
	message := err.Error()
	switch {
	case strings.Contains(message, "invalid worktree branch"):
		writeError(w, http.StatusBadRequest, "bad_request", message)
	case strings.Contains(message, "resolve base"):
		writeError(w, http.StatusBadRequest, "bad_request", message)
	case strings.Contains(message, "branch \"") && strings.Contains(message, "already exists"):
		writeError(w, http.StatusConflict, "conflict", message)
	case strings.Contains(message, "destination already exists") || strings.Contains(message, "destination must be outside"):
		writeError(w, http.StatusConflict, "conflict", message)
	default:
		writeError(w, http.StatusInternalServerError, "internal", "failed to create worktree")
	}
}

func writeProjectWorktreeCommandError(w http.ResponseWriter, ctx context.Context, err error) {
	if ctx.Err() != nil {
		writeError(w, http.StatusGatewayTimeout, "unavailable", "worktree operation timed out")
		return
	}
	writeError(w, http.StatusConflict, "conflict", err.Error())
}

func pathInsideWorktree(root, candidate string) bool {
	if root == "" || candidate == "" {
		return false
	}
	root = canonicalFilesystemPath(root)
	candidate = canonicalFilesystemPath(candidate)
	rel, err := filepath.Rel(root, candidate)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func canonicalFilesystemPath(path string) string {
	return paths.NormalizePath(paths.CanonicalizePath(paths.NormalizePath(path)))
}

// projectFilesystemRoot returns the catalog's canonical first path for file
// browsing and launching. Worktree operations pass requireSingle because Git
// mutation is ambiguous when one project matches multiple repositories.
func projectFilesystemRoot(ctx context.Context, slug string, sessions *centralstore.Store, requireSingle bool) (string, error) {
	catalog, err := sessions.ListProjectCatalog(ctx)
	if err != nil {
		return "", workspacePathError{http.StatusInternalServerError, "internal", "failed to load projects"}
	}
	for _, project := range catalog {
		if project.Slug != slug {
			continue
		}
		if project.Kind != centralstore.ProjectEntryOwned {
			return "", workspacePathError{http.StatusBadRequest, "bad_request", "remote project filesystem actions must be routed to the owning host"}
		}
		roots := make(map[string]struct{})
		firstRoot := ""
		for _, rule := range project.Rules {
			if root := paths.NormalizePath(strings.TrimSpace(rule.Path)); root != "" {
				if firstRoot == "" {
					firstRoot = root
				}
				roots[root] = struct{}{}
			}
		}
		if len(roots) == 0 {
			return "", workspacePathError{http.StatusBadRequest, "no_workspace_root", "project has no path rule"}
		}
		if requireSingle && len(roots) != 1 {
			return "", workspacePathError{http.StatusBadRequest, "bad_request", "projects must have exactly one path root for worktree operations"}
		}
		return firstRoot, nil
	}
	return "", workspacePathError{http.StatusNotFound, "not_found", "project not found"}
}

func writeProjectWorktrees(w http.ResponseWriter, slug, primaryPath string, items []projectWorktree) {
	if items == nil {
		items = []projectWorktree{}
	}
	writeJSON(w, map[string]any{"ok": true, "data": projectWorktrees{
		ProjectSlug: slug,
		PrimaryPath: primaryPath,
		Worktrees:   items,
	}})
}

func hasPrimaryWorktree(items []projectWorktree) bool {
	for _, item := range items {
		if item.Primary {
			return true
		}
	}
	return false
}
