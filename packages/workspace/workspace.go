// Package workspace detects VCS workspace roots for jj and git repositories.
// Used to group sessions that belong to the same repository.
package workspace

import (
	"os"
	"path/filepath"
	"strings"
)

// GitLayout describes the Git marker found for a workspace.
type GitLayout string

const (
	GitLayoutRepository GitLayout = "repository"
	GitLayoutWorktree   GitLayout = "worktree"
)

// Detection is the VCS metadata derived from a session's starting directory.
// GitLayout is empty for jj-only workspaces and directories outside Git.
type Detection struct {
	Root      string
	GitLayout GitLayout
}

// Detect walks up from dir looking for jj or git repository markers.
// Root preserves the existing jj-first grouping behavior, while GitLayout
// independently records whether a colocated Git marker is a repository or a
// linked worktree. Returns the zero value if no VCS root is found.
func Detect(dir string) Detection {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return Detection{}
	}

	cur := dir
	for {
		// Check jj first for the grouping root, but retain a colocated Git
		// marker so the UI can distinguish repositories from worktrees.
		jjRoot := checkJJ(cur)
		gitRoot, gitLayout := checkGit(cur)
		if jjRoot != "" {
			return Detection{Root: jjRoot, GitLayout: gitLayout}
		}
		if gitRoot != "" {
			return Detection{Root: gitRoot, GitLayout: gitLayout}
		}

		parent := filepath.Dir(cur)
		if parent == cur {
			break // reached filesystem root
		}
		cur = parent
	}
	return Detection{}
}

// DetectRoot returns the workspace grouping root. Kept as a compatibility
// wrapper for callers that do not need Git layout metadata.
func DetectRoot(dir string) string {
	return Detect(dir).Root
}

// checkJJ checks for a .jj directory at dir and resolves the workspace root.
//
// jj workspace layout:
//   - Main workspace: .jj/repo is a directory (the actual store).
//   - Secondary workspace: .jj/repo is a regular file containing a relative
//     path to the main workspace's .jj/repo directory (e.g. "../../../.jj/repo").
func checkJJ(dir string) string {
	jjDir := filepath.Join(dir, ".jj")
	info, err := os.Lstat(jjDir)
	if err != nil || !info.IsDir() {
		return ""
	}

	repoPath := filepath.Join(jjDir, "repo")
	repoInfo, err := os.Lstat(repoPath)
	if err != nil {
		// .jj exists but no repo entry; still a jj directory.
		return dir
	}

	if repoInfo.IsDir() {
		// Main workspace: .jj/repo is the store directory.
		return dir
	}

	// Secondary workspace: .jj/repo is a file containing a path to the
	// main workspace's .jj/repo. Read and resolve it.
	data, err := os.ReadFile(repoPath)
	if err != nil {
		return dir
	}
	target := strings.TrimSpace(string(data))
	if target == "" {
		return dir
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(jjDir, target)
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return dir
	}
	// target is something like /path/to/main-workspace/.jj/repo
	// The main workspace root is two levels up from the target.
	mainJJ := filepath.Dir(target)   // .jj
	mainRoot := filepath.Dir(mainJJ) // workspace root
	return mainRoot
}

// checkGit checks for a .git entry at dir and resolves the workspace root.
// Handles both regular repos (.git is a directory) and worktrees (.git is a
// file containing "gitdir: /path/to/.git/worktrees/<name>").
func checkGit(dir string) (string, GitLayout) {
	gitPath := filepath.Join(dir, ".git")
	info, err := os.Lstat(gitPath)
	if err != nil {
		return "", ""
	}

	if info.IsDir() {
		// Regular git repo: .git is a directory, dir is the root.
		return dir, GitLayoutRepository
	}

	// Preserve DetectRoot's historical fallback for unreadable or malformed
	// .git files, but leave the layout unknown rather than mislabeling them.
	data, err := os.ReadFile(gitPath)
	if err != nil {
		return dir, ""
	}

	line := strings.TrimSpace(string(data))
	if !strings.HasPrefix(line, "gitdir: ") {
		return dir, ""
	}

	gitdir := strings.TrimPrefix(line, "gitdir: ")
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(dir, gitdir)
	}
	gitdir, err = filepath.Abs(gitdir)
	if err != nil {
		return dir, ""
	}
	gitdirInfo, err := os.Stat(gitdir)
	if err != nil || !gitdirInfo.IsDir() {
		return dir, ""
	}

	// gitdir is something like /path/to/main-repo/.git/worktrees/<name>
	// Walk up to find the main .git directory.
	// Standard layout: .git/worktrees/<name> → 2 levels up is .git
	mainGitDir := resolveMainGitDir(gitdir)
	if mainGitDir == "" {
		// Submodules and repositories created with --separate-git-dir also
		// use a .git file, but are not linked worktrees.
		return dir, GitLayoutRepository
	}

	// The main repo root is the parent of .git/
	return filepath.Dir(mainGitDir), GitLayoutWorktree
}

// resolveMainGitDir walks up from a worktree gitdir path to find the main
// .git directory. Returns "" if the structure doesn't match expectations.
func resolveMainGitDir(gitdir string) string {
	// Typical: /repo/.git/worktrees/name → parent is "worktrees" → parent is ".git"
	// But also handle commondir: read commondir file if it exists.
	commondir := filepath.Join(gitdir, "commondir")
	data, err := os.ReadFile(commondir)
	if err == nil {
		target := strings.TrimSpace(string(data))
		if !filepath.IsAbs(target) {
			target = filepath.Join(gitdir, target)
		}
		target, err = filepath.Abs(target)
		if err == nil {
			return target
		}
	}

	// Fallback: walk up assuming standard .git/worktrees/<name> layout.
	parent := filepath.Dir(gitdir) // .git/worktrees
	if filepath.Base(parent) == "worktrees" {
		return filepath.Dir(parent) // .git
	}
	return ""
}
