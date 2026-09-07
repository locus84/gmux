package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
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
		return nil, gitCommandError("list git worktrees", out, err)
	}
	items, err := ParseWorktreePorcelainZ(out)
	if err != nil {
		return nil, err
	}
	return items, nil
}

// CreateWorktreeOptions describes a branch-backed linked checkout. Destination
// may be explicit for CLI compatibility; otherwise ManagedRoot is required and
// the destination is derived from the repository and branch.
type CreateWorktreeOptions struct {
	Repository  string
	Branch      string
	Base        string
	Destination string
	ManagedRoot string
}

// CreateWorktreeContext creates a new local branch and linked checkout without
// copying uncommitted, untracked, or ignored files from the source checkout.
func CreateWorktreeContext(ctx context.Context, opts CreateWorktreeOptions) (Worktree, error) {
	if strings.TrimSpace(opts.Repository) == "" {
		return Worktree{}, errors.New("repository path is required")
	}
	if opts.Base == "" {
		opts.Base = "HEAD"
	}
	repo, err := gitOutputContext(ctx, opts.Repository, "rev-parse", "--show-toplevel")
	if err != nil {
		return Worktree{}, fmt.Errorf("resolve repository: %w", err)
	}
	repo = canonicalPath(repo)
	if _, err := gitOutputContext(ctx, repo, "check-ref-format", "--branch", opts.Branch); err != nil {
		return Worktree{}, fmt.Errorf("invalid worktree branch %q: %w", opts.Branch, err)
	}
	baseHead, err := gitOutputContext(ctx, repo, "rev-parse", "--verify", "--end-of-options", opts.Base+"^{commit}")
	if err != nil {
		return Worktree{}, fmt.Errorf("resolve base %q: %w", opts.Base, err)
	}
	if gitOKContext(ctx, repo, "show-ref", "--verify", "--quiet", "refs/heads/"+opts.Branch) {
		return Worktree{}, fmt.Errorf("branch %q already exists", opts.Branch)
	}
	items, err := ListWorktreesContext(ctx, repo)
	if err != nil {
		return Worktree{}, fmt.Errorf("list repository worktrees: %w", err)
	}
	if len(items) == 0 {
		return Worktree{}, errors.New("list repository worktrees: Git returned no worktrees")
	}

	destination := opts.Destination
	if destination == "" {
		if strings.TrimSpace(opts.ManagedRoot) == "" {
			return Worktree{}, errors.New("managed worktree root is required")
		}
		repoPath, err := mirroredRepositoryPath(items[0].Path)
		if err != nil {
			return Worktree{}, fmt.Errorf("resolve managed worktree path: %w", err)
		}
		destination = filepath.Join(opts.ManagedRoot, repoPath, worktreePathName(opts.Branch))
	}
	destination = canonicalNewPath(destination)
	for _, existing := range items {
		if pathContains(existing.Path, destination) {
			return Worktree{}, fmt.Errorf("destination must be outside existing worktrees: %s", destination)
		}
	}
	if _, err := os.Lstat(destination); err == nil {
		return Worktree{}, fmt.Errorf("destination already exists: %s", destination)
	} else if !os.IsNotExist(err) {
		return Worktree{}, fmt.Errorf("inspect destination: %w", err)
	}

	// Use the already-resolved commit so a moving base ref cannot change which
	// commit is checked out between validation and creation.
	if _, err := gitOutputContext(ctx, repo, "worktree", "add", "-b", opts.Branch, "--", destination, baseHead); err != nil {
		return Worktree{}, fmt.Errorf("create worktree: %w", err)
	}
	return Worktree{Path: destination, Branch: opts.Branch, Head: baseHead}, nil
}

func gitOutputContext(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", gitCommandError("git "+strings.Join(args, " "), out, err)
	}
	return strings.TrimRight(string(out), "\r\n"), nil
}

func gitOKContext(ctx context.Context, dir string, args ...string) bool {
	return exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).Run() == nil
}

func worktreePathName(branch string) string {
	replacer := strings.NewReplacer("/", "-", "\\", "-", string(filepath.Separator), "-")
	return replacer.Replace(branch)
}

func mirroredRepositoryPath(repo string) (string, error) {
	repo = canonicalPath(repo)
	volume := filepath.VolumeName(repo)
	rel := strings.TrimLeft(strings.TrimPrefix(repo, volume), "/\\")
	if volume != "" {
		volume = strings.TrimPrefix(strings.TrimPrefix(volume, `\\?\`), `\\.\`)
		if len(volume) >= 4 && strings.EqualFold(volume[:4], `UNC\`) {
			volume = volume[4:]
		}
		volume = strings.Trim(volume, "/\\")
		volume = strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(volume, ":", ""), "\\", string(filepath.Separator)), "/", string(filepath.Separator))
		rel = filepath.Join(volume, rel)
	}
	rel = filepath.Clean(rel)
	if rel == "." || rel == "" {
		if filepath.IsAbs(repo) {
			return "_root", nil
		}
		return "", fmt.Errorf("repository path %q cannot be mirrored safely", repo)
	}
	if filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("repository path %q cannot be mirrored safely", repo)
	}
	return rel, nil
}

// WorktreeDirtyContext reports whether path has tracked, staged, untracked,
// ignored, conflicted, or submodule files. Ignored files are included because
// git worktree remove would otherwise silently delete them without --force.
// Callers should still use non-force Git removal because the worktree may
// change after this check.
func WorktreeDirtyContext(ctx context.Context, path string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", path, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignored=matching")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, gitCommandError("inspect git worktree", out, err)
	}
	return len(out) > 0, nil
}

// RemoveWorktreeContext removes an exact linked checkout without --force.
// Git remains the final safety guard against changes racing this operation.
// Removing a worktree does not delete its branch.
func RemoveWorktreeContext(ctx context.Context, repositoryRoot, path string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", repositoryRoot, "worktree", "remove", "--", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return gitCommandError("remove git worktree", out, err)
	}
	return nil
}

func gitCommandError(action string, out []byte, err error) error {
	msg := strings.TrimSpace(string(out))
	if msg == "" {
		msg = err.Error()
	}
	return fmt.Errorf("%s: %s", action, msg)
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
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	path = filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

// canonicalNewPath resolves the nearest existing ancestor so a destination
// below a symlink cannot bypass containment checks before it exists.
func canonicalNewPath(path string) string {
	path = canonicalPath(path)
	probe := path
	var suffix []string
	for {
		if resolved, err := filepath.EvalSymlinks(probe); err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return filepath.Clean(resolved)
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return path
		}
		suffix = append(suffix, filepath.Base(probe))
		probe = parent
	}
}

func pathContains(root, path string) bool {
	rel, err := filepath.Rel(canonicalPath(root), canonicalPath(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
