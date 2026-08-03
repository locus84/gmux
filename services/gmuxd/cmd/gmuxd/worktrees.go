package main

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gmuxapp/gmux/packages/paths"
	"github.com/gmuxapp/gmux/packages/workspace"
	projectspkg "github.com/gmuxapp/gmux/services/gmuxd/internal/projects"
	"github.com/gmuxapp/gmux/services/gmuxd/internal/store"
)

const projectWorktreeTimeout = 5 * time.Second

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
