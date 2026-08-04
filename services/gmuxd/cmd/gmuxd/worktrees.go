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
	projectspkg "github.com/gmuxapp/gmux/services/gmuxd/internal/projects"
	"github.com/gmuxapp/gmux/services/gmuxd/internal/store"
)

const projectWorktreeTimeout = 5 * time.Second
const projectWorktreeRemoveTimeout = 30 * time.Second
const projectWorktreeRequestLimit = 16 * 1024

// worktreeLifecycleMu prevents local launch/resume registration from racing a
// worktree safety check and removal. Peer launches are guarded by their owning
// daemon after forwarding.
var worktreeLifecycleMu sync.RWMutex

type projectWorktree struct {
	workspace.Worktree
	Primary bool `json:"primary"`
}

type projectWorktrees struct {
	ProjectSlug string            `json:"project_slug"`
	PrimaryPath string            `json:"primary_path"`
	Worktrees   []projectWorktree `json:"worktrees"`
}

func projectWorktreesHandler(w http.ResponseWriter, r *http.Request, slug string, projectMgr *projectspkg.Manager, sessions *store.Store) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "bad_request", "method not allowed")
		return
	}

	root, err := projectWorktreeRoot(slug, projectMgr, sessions)
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

func projectWorktreeDeleteHandler(w http.ResponseWriter, r *http.Request, slug string, projectMgr *projectspkg.Manager, sessions *store.Store) {
	var req struct {
		Path string `json:"path"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, projectWorktreeRequestLimit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil || strings.TrimSpace(req.Path) == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "path is required")
		return
	}

	worktreeLifecycleMu.Lock()
	defer worktreeLifecycleMu.Unlock()

	root, err := projectWorktreeRoot(slug, projectMgr, sessions)
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
	for _, session := range sessions.List() {
		if session.Peer != "" || (!session.Alive && !session.Resumable) {
			continue
		}
		if pathInsideWorktree(target, paths.NormalizePath(session.Cwd)) ||
			pathInsideWorktree(target, paths.NormalizePath(session.WorkspaceRoot)) {
			writeError(w, http.StatusConflict, "conflict", "worktree has a live or resumable session; dismiss it first")
			return
		}
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

func projectWorktreeRoot(slug string, projectMgr *projectspkg.Manager, sessions *store.Store) (string, error) {
	state, err := projectMgr.Load()
	if err != nil {
		return "", workspacePathError{http.StatusInternalServerError, "internal", "failed to load projects"}
	}
	for _, item := range state.Items {
		if item.Slug != slug || item.Peer != "" {
			continue
		}
		var roots []string
		for _, rule := range item.Match {
			if strings.TrimSpace(rule.Path) != "" {
				roots = append(roots, rule.Path)
			}
		}
		if len(roots) > 1 {
			return "", workspacePathError{http.StatusBadRequest, "bad_request", "projects with multiple path roots do not have a single worktree inventory"}
		}
		if len(roots) == 1 {
			return roots[0], nil
		}
		break
	}
	return workspaceRootForProject(slug, projectMgr, sessions)
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
