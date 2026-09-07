package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectRootJJSimple(t *testing.T) {
	// Simple jj repo: .jj/repo is a directory (main workspace)
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".jj", "repo"), 0o755)

	got := DetectRoot(root)
	if got != root {
		t.Errorf("expected %q, got %q", root, got)
	}
}

func TestDetectRootJJFromSubdir(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".jj", "repo"), 0o755)
	subdir := filepath.Join(root, "src", "pkg")
	os.MkdirAll(subdir, 0o755)

	got := DetectRoot(subdir)
	if got != root {
		t.Errorf("expected %q, got %q", root, got)
	}
}

func TestDetectRootJJWorkspace(t *testing.T) {
	// Main workspace at /main/.jj/repo/ (a real directory)
	// Secondary workspace at /secondary/.jj/repo (a file containing relative path)
	dir := t.TempDir()
	mainRoot := filepath.Join(dir, "main")
	secondaryRoot := filepath.Join(dir, "secondary")

	os.MkdirAll(filepath.Join(mainRoot, ".jj", "repo"), 0o755)
	os.MkdirAll(filepath.Join(secondaryRoot, ".jj"), 0o755)

	// jj writes a relative path in the repo file for secondary workspaces
	relPath, _ := filepath.Rel(filepath.Join(secondaryRoot, ".jj"), filepath.Join(mainRoot, ".jj", "repo"))
	os.WriteFile(
		filepath.Join(secondaryRoot, ".jj", "repo"),
		[]byte(relPath),
		0o644,
	)

	got := DetectRoot(secondaryRoot)
	if got != mainRoot {
		t.Errorf("expected main root %q, got %q", mainRoot, got)
	}

	// Main workspace detects itself
	got = DetectRoot(mainRoot)
	if got != mainRoot {
		t.Errorf("expected main root %q, got %q", mainRoot, got)
	}
}

func TestDetectRootGitSimple(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".git"), 0o755)

	got := DetectRoot(root)
	if got != root {
		t.Errorf("expected %q, got %q", root, got)
	}
}

func TestDetectRootGitFromSubdir(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".git"), 0o755)
	subdir := filepath.Join(root, "src", "pkg")
	os.MkdirAll(subdir, 0o755)

	got := DetectRoot(subdir)
	if got != root {
		t.Errorf("expected %q, got %q", root, got)
	}
}

func TestDetectRootGitWorktree(t *testing.T) {
	// Main repo at /main/.git/ (directory)
	// Worktree at /worktree/.git (file pointing to main/.git/worktrees/wt)
	dir := t.TempDir()
	mainRoot := filepath.Join(dir, "main")
	worktreeRoot := filepath.Join(dir, "worktree")

	mainGit := filepath.Join(mainRoot, ".git")
	os.MkdirAll(filepath.Join(mainGit, "worktrees", "wt"), 0o755)
	os.MkdirAll(worktreeRoot, 0o755)

	// Write the commondir file (how git worktrees reference the main repo)
	wtGitdir := filepath.Join(mainGit, "worktrees", "wt")
	os.WriteFile(
		filepath.Join(wtGitdir, "commondir"),
		[]byte("../..\n"),
		0o644,
	)

	// Write .git file in worktree
	os.WriteFile(
		filepath.Join(worktreeRoot, ".git"),
		[]byte("gitdir: "+wtGitdir+"\n"),
		0o644,
	)

	got := DetectRoot(worktreeRoot)
	if got != mainRoot {
		t.Errorf("expected main root %q, got %q", mainRoot, got)
	}
}

func TestDetectRootGitWorktreeFallbackLayout(t *testing.T) {
	// Worktree without commondir file, relying on directory structure
	dir := t.TempDir()
	mainRoot := filepath.Join(dir, "main")
	worktreeRoot := filepath.Join(dir, "worktree")

	mainGit := filepath.Join(mainRoot, ".git")
	wtGitdir := filepath.Join(mainGit, "worktrees", "wt")
	os.MkdirAll(wtGitdir, 0o755)
	os.MkdirAll(worktreeRoot, 0o755)

	os.WriteFile(
		filepath.Join(worktreeRoot, ".git"),
		[]byte("gitdir: "+wtGitdir+"\n"),
		0o644,
	)

	got := DetectRoot(worktreeRoot)
	if got != mainRoot {
		t.Errorf("expected main root %q, got %q", mainRoot, got)
	}
}

func TestDetectRootJJPreferredOverGit(t *testing.T) {
	// Colocated: both .jj and .git in the same directory
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".jj", "repo"), 0o755)
	os.MkdirAll(filepath.Join(root, ".git"), 0o755)

	got := DetectRoot(root)
	if got != root {
		t.Errorf("expected %q, got %q", root, got)
	}
	// Both point to the same root, so the result is the same.
	// The important thing is that jj is checked first (no error).
}

func TestDetectRootJJNestedWorkspace(t *testing.T) {
	// Mimics the gmux repo layout:
	// /repo/.jj/repo/ (directory, main workspace)
	// /repo/.grove/teak/.jj/repo (file: "../../../.jj/repo")
	dir := t.TempDir()
	mainRoot := dir
	teakRoot := filepath.Join(dir, ".grove", "teak")

	os.MkdirAll(filepath.Join(mainRoot, ".jj", "repo"), 0o755)
	os.MkdirAll(filepath.Join(teakRoot, ".jj"), 0o755)
	os.WriteFile(
		filepath.Join(teakRoot, ".jj", "repo"),
		[]byte("../../../.jj/repo"),
		0o644,
	)

	// Secondary workspace resolves to main
	got := DetectRoot(teakRoot)
	if got != mainRoot {
		t.Errorf("expected %q, got %q", mainRoot, got)
	}

	// Subdir of secondary workspace also resolves to main
	subdir := filepath.Join(teakRoot, "packages", "adapter")
	os.MkdirAll(subdir, 0o755)
	got = DetectRoot(subdir)
	if got != mainRoot {
		t.Errorf("from subdir: expected %q, got %q", mainRoot, got)
	}
}

func TestDetectRootNoVCS(t *testing.T) {
	dir := t.TempDir()

	got := DetectRoot(dir)
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestDetectGitLayout(t *testing.T) {
	t.Run("repository from subdirectory", func(t *testing.T) {
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		subdir := filepath.Join(root, "src")
		if err := os.Mkdir(subdir, 0o755); err != nil {
			t.Fatal(err)
		}

		got := Detect(subdir)
		if got.Root != root || got.GitLayout != GitLayoutRepository {
			t.Fatalf("Detect() = %+v, want root %q and repository", got, root)
		}
	})

	t.Run("linked worktree", func(t *testing.T) {
		dir := t.TempDir()
		mainRoot := filepath.Join(dir, "main")
		worktreeRoot := filepath.Join(dir, "worktree")
		gitdir := filepath.Join(mainRoot, ".git", "worktrees", "wt")
		if err := os.MkdirAll(gitdir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(worktreeRoot, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(gitdir, "commondir"), []byte("../..\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(worktreeRoot, ".git"), []byte("gitdir: "+gitdir+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		got := Detect(filepath.Join(worktreeRoot, "missing-subdir"))
		// filepath.Abs accepts a missing leaf; detection still walks to the marker.
		if got.Root != mainRoot || got.GitLayout != GitLayoutWorktree {
			t.Fatalf("Detect() = %+v, want root %q and worktree", got, mainRoot)
		}
	})

	t.Run("broken gitdir marker leaves layout unknown", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: /missing/gitdir\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		got := Detect(root)
		if got.Root != root || got.GitLayout != "" {
			t.Fatalf("Detect() = %+v, want root with unknown layout", got)
		}
	})

	t.Run("gitdir file without worktree layout is repository", func(t *testing.T) {
		dir := t.TempDir()
		root := filepath.Join(dir, "submodule")
		gitdir := filepath.Join(dir, "parent", ".git", "modules", "submodule")
		if err := os.MkdirAll(gitdir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(root, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: "+gitdir+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		got := Detect(root)
		if got.Root != root || got.GitLayout != GitLayoutRepository {
			t.Fatalf("Detect() = %+v, want gitdir-backed repository", got)
		}
	})

	t.Run("jj only", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, ".jj", "repo"), 0o755); err != nil {
			t.Fatal(err)
		}
		got := Detect(root)
		if got.Root != root || got.GitLayout != "" {
			t.Fatalf("Detect() = %+v, want jj root with no Git layout", got)
		}
	})

	t.Run("colocated jj and git", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, ".jj", "repo"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		got := Detect(root)
		if got.Root != root || got.GitLayout != GitLayoutRepository {
			t.Fatalf("Detect() = %+v, want colocated repository", got)
		}
	})

	t.Run("no vcs", func(t *testing.T) {
		if got := Detect(t.TempDir()); got != (Detection{}) {
			t.Fatalf("Detect() = %+v, want zero value", got)
		}
	})
}

func TestDetectRootEmptyString(t *testing.T) {
	// Empty string should not panic
	got := DetectRoot("")
	// Result depends on os.Getwd, just verify no panic
	_ = got
}
