package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Worktree is one record from `git worktree list --porcelain -z`.
type Worktree struct {
	Path       string `json:"path"`
	Head       string `json:"head,omitempty"`
	Branch     string `json:"branch,omitempty"`
	Detached   bool   `json:"detached,omitempty"`
	Bare       bool   `json:"bare,omitempty"`
	Locked     bool   `json:"locked,omitempty"`
	LockReason string `json:"lock_reason,omitempty"`
	Prunable   bool   `json:"prunable,omitempty"`
}

// ParseWorktreePorcelainZ parses the stable, NUL-delimited Git worktree format.
func ParseWorktreePorcelainZ(data []byte) ([]Worktree, error) {
	records := bytes.Split(data, []byte{0, 0})
	out := make([]Worktree, 0, len(records))
	for _, record := range records {
		if len(bytes.Trim(record, "\x00")) == 0 {
			continue
		}
		var wt Worktree
		for _, raw := range bytes.Split(record, []byte{0}) {
			line := string(raw)
			switch {
			case strings.HasPrefix(line, "worktree "):
				wt.Path = strings.TrimPrefix(line, "worktree ")
			case strings.HasPrefix(line, "HEAD "):
				wt.Head = strings.TrimPrefix(line, "HEAD ")
			case strings.HasPrefix(line, "branch "):
				wt.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
			case line == "detached":
				wt.Detached = true
			case line == "bare":
				wt.Bare = true
			case line == "locked":
				wt.Locked = true
			case strings.HasPrefix(line, "locked "):
				wt.Locked = true
				wt.LockReason = strings.TrimPrefix(line, "locked ")
			case line == "prunable" || strings.HasPrefix(line, "prunable "):
				wt.Prunable = true
			}
		}
		if wt.Path == "" {
			return nil, errors.New("malformed git worktree record: missing path")
		}
		wt.Path = canonicalPath(wt.Path)
		out = append(out, wt)
	}
	return out, nil
}

// ListWorktrees asks Git for every checkout belonging to dir's repository.
func ListWorktrees(dir string) ([]Worktree, error) {
	return ListWorktreesContext(context.Background(), dir)
}

// ListWorktreesContext is ListWorktrees with caller-controlled cancellation.
func ListWorktreesContext(ctx context.Context, dir string) ([]Worktree, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "worktree", "list", "--porcelain", "-z")
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("list git worktrees: %s", msg)
	}
	items, err := ParseWorktreePorcelainZ(out)
	if err != nil {
		return nil, err
	}
	return items, nil
}

// CurrentWorktree returns the deepest checkout containing cwd.
func CurrentWorktree(items []Worktree, cwd string) (Worktree, error) {
	cwd = canonicalPath(cwd)
	best := -1
	for i := range items {
		if pathContains(items[i].Path, cwd) && (best < 0 || len(items[i].Path) > len(items[best].Path)) {
			best = i
		}
	}
	if best < 0 {
		return Worktree{}, fmt.Errorf("%q is not inside a listed worktree", cwd)
	}
	return items[best], nil
}

// ResolveWorktree resolves current or an explicit branch/path/name selector.
func ResolveWorktree(items []Worktree, selector, cwd string) (Worktree, error) {
	if selector == "" || selector == "current" {
		return CurrentWorktree(items, cwd)
	}
	var matches []Worktree
	add := func(wt Worktree) {
		for _, existing := range matches {
			if existing.Path == wt.Path {
				return
			}
		}
		matches = append(matches, wt)
	}

	kind, value := "bare", selector
	if i := strings.IndexByte(selector, ':'); i > 0 {
		kind, value = selector[:i], selector[i+1:]
	}
	for _, wt := range items {
		switch kind {
		case "branch":
			if wt.Branch == value {
				add(wt)
			}
		case "name":
			if filepath.Base(wt.Path) == value {
				add(wt)
			}
		case "path":
			path := value
			if !filepath.IsAbs(path) {
				path = filepath.Join(cwd, path)
			}
			if wt.Path == canonicalPath(path) {
				add(wt)
			}
		case "bare":
			path := value
			if !filepath.IsAbs(path) {
				path = filepath.Join(cwd, path)
			}
			if wt.Branch == value || filepath.Base(wt.Path) == value || wt.Path == canonicalPath(path) {
				add(wt)
			}
		default:
			return Worktree{}, fmt.Errorf("unknown worktree selector %q", kind)
		}
	}
	if len(matches) == 0 && kind == "bare" {
		for _, wt := range items {
			if strings.HasPrefix(wt.Branch, value) || strings.HasPrefix(filepath.Base(wt.Path), value) {
				add(wt)
			}
		}
	}
	switch len(matches) {
	case 0:
		return Worktree{}, fmt.Errorf("no worktree matches %q", selector)
	case 1:
		return matches[0], nil
	default:
		names := make([]string, 0, len(matches))
		for _, wt := range matches {
			names = append(names, wt.Branch+" ("+wt.Path+")")
		}
		sort.Strings(names)
		return Worktree{}, fmt.Errorf("ambiguous worktree %q matches: %s", selector, strings.Join(names, ", "))
	}
}

func canonicalPath(path string) string {
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	path = filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

func pathContains(root, path string) bool {
	rel, err := filepath.Rel(canonicalPath(root), canonicalPath(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
