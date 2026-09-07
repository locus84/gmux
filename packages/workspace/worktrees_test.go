package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseWorktreePorcelainZ(t *testing.T) {
	data := []byte("worktree /tmp/main repo\x00HEAD abc123\x00branch refs/heads/main\x00\x00" +
		"worktree /tmp/feature\x00HEAD def456\x00detached\x00locked maintenance\x00prunable stale metadata\x00\x00")
	got, err := ParseWorktreePorcelainZ(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2: %#v", len(got), got)
	}
	if got[0].Path != "/tmp/main repo" || got[0].Branch != "main" || got[0].Head != "abc123" {
		t.Fatalf("main = %#v", got[0])
	}
	if !got[1].Detached || !got[1].Locked || got[1].LockReason != "maintenance" || !got[1].Prunable {
		t.Fatalf("feature = %#v", got[1])
	}
}

func TestParseWorktreePorcelainZRejectsMalformedRecord(t *testing.T) {
	if _, err := ParseWorktreePorcelainZ([]byte("HEAD abc\x00\x00")); err == nil {
		t.Fatal("expected malformed record error")
	}
}

func TestListWorktreesAndResolveCurrent(t *testing.T) {
	repo := initGitRepo(t)
	wt := filepath.Join(t.TempDir(), "feature with spaces")
	runGit(t, repo, "worktree", "add", "-b", "team/feature", wt, "HEAD")

	got, err := ListWorktrees(filepath.Join(wt, "nested"))
	if err == nil {
		// nested does not exist yet; ListWorktrees should report the bad cwd.
		t.Fatal("expected missing cwd error")
	}
	if err := os.Mkdir(filepath.Join(wt, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err = ListWorktrees(filepath.Join(wt, "nested"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	cur, err := CurrentWorktree(got, filepath.Join(wt, "nested"))
	if err != nil {
		t.Fatal(err)
	}
	if cur.Branch != "team/feature" || cur.Path != canonicalPath(wt) {
		t.Fatalf("current = %#v", cur)
	}
}

func TestCreateWorktreeContextManagedPathAndResolvedBase(t *testing.T) {
	repo := initGitRepo(t)
	base := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	if err := os.WriteFile(filepath.Join(repo, "local-only.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	managed := t.TempDir()
	created, err := CreateWorktreeContext(t.Context(), CreateWorktreeOptions{
		Repository: repo, Branch: "fix/login", Base: "HEAD", ManagedRoot: managed,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Branch != "fix/login" || created.Head != base || !pathContains(managed, created.Path) || filepath.Base(created.Path) != "fix-login" {
		t.Fatalf("created = %#v", created)
	}
	if _, err := os.Stat(filepath.Join(created.Path, "local-only.txt")); !os.IsNotExist(err) {
		t.Fatalf("dirty source file was copied: %v", err)
	}
	if got := strings.TrimSpace(runGit(t, created.Path, "rev-parse", "HEAD")); got != base {
		t.Fatalf("HEAD = %q, want %q", got, base)
	}
	if _, err := CreateWorktreeContext(t.Context(), CreateWorktreeOptions{
		Repository: repo, Branch: "fix/login", ManagedRoot: managed,
	}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate error = %v", err)
	}
}

func TestCreateWorktreeContextRejectsUnsafeInput(t *testing.T) {
	repo := initGitRepo(t)
	managed := t.TempDir()
	if _, err := CreateWorktreeContext(t.Context(), CreateWorktreeOptions{
		Repository: repo, Branch: "-bad", ManagedRoot: managed,
	}); err == nil || !strings.Contains(err.Error(), "invalid worktree branch") {
		t.Fatalf("invalid branch error = %v", err)
	}
	if _, err := CreateWorktreeContext(t.Context(), CreateWorktreeOptions{
		Repository: repo, Branch: "bad-base", Base: "--not-a-ref", ManagedRoot: managed,
	}); err == nil || !strings.Contains(err.Error(), "resolve base") {
		t.Fatalf("invalid base error = %v", err)
	}
	parent := t.TempDir()
	link := filepath.Join(parent, "repo-link")
	if err := os.Symlink(repo, link); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateWorktreeContext(t.Context(), CreateWorktreeOptions{
		Repository: repo, Branch: "nested", Destination: filepath.Join(link, "nested"), ManagedRoot: managed,
	}); err == nil || !strings.Contains(err.Error(), "outside existing worktrees") {
		t.Fatalf("symlinked destination error = %v", err)
	}
}

func TestWorktreeDirtyAndRemove(t *testing.T) {
	repo := initGitRepo(t)
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("ignored.secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", ".gitignore")
	runGit(t, repo, "commit", "-m", "ignore generated secret")
	wt := filepath.Join(t.TempDir(), "feature")
	runGit(t, repo, "worktree", "add", "-b", "feature", wt, "HEAD")

	dirty, err := WorktreeDirtyContext(t.Context(), wt)
	if err != nil || dirty {
		t.Fatalf("clean dirty=%v err=%v", dirty, err)
	}
	ignored := filepath.Join(wt, "ignored.secret")
	if err := os.WriteFile(ignored, []byte("valuable\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dirty, err = WorktreeDirtyContext(t.Context(), wt)
	if err != nil || !dirty {
		t.Fatalf("ignored dirty=%v err=%v", dirty, err)
	}
	if err := os.Remove(ignored); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "new.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty, err = WorktreeDirtyContext(t.Context(), wt)
	if err != nil || !dirty {
		t.Fatalf("changed dirty=%v err=%v", dirty, err)
	}
	if err := RemoveWorktreeContext(t.Context(), repo, wt); err == nil {
		t.Fatal("expected non-force removal to reject dirty worktree")
	}
	if err := os.Remove(filepath.Join(wt, "new.txt")); err != nil {
		t.Fatal(err)
	}
	if err := RemoveWorktreeContext(t.Context(), repo, wt); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("worktree still exists: %v", err)
	}
	if got := runGit(t, repo, "branch", "--list", "feature"); !strings.Contains(got, "feature") {
		t.Fatalf("branch was removed: %q", got)
	}
}

func TestResolveWorktreeSelectors(t *testing.T) {
	cwd := t.TempDir()
	items := []Worktree{
		{Path: filepath.Join(cwd, "main"), Branch: "main"},
		{Path: filepath.Join(cwd, "one", "feature"), Branch: "team/feature"},
		{Path: filepath.Join(cwd, "two", "feature"), Branch: "user/feature"},
	}
	for _, tc := range []struct {
		sel  string
		want string
	}{
		{"branch:team/feature", "team/feature"},
		{"path:" + filepath.Join(cwd, "main"), "main"},
		{"main", "main"},
	} {
		got, err := ResolveWorktree(items, tc.sel, cwd)
		if err != nil {
			t.Fatalf("ResolveWorktree(%q): %v", tc.sel, err)
		}
		if got.Branch != tc.want {
			t.Fatalf("ResolveWorktree(%q).Branch = %q, want %q", tc.sel, got.Branch, tc.want)
		}
	}
	if _, err := ResolveWorktree(items, "name:feature", cwd); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous name error = %v", err)
	}
	if _, err := ResolveWorktree(items, "branch:missing", cwd); err == nil {
		t.Fatal("expected missing selector error")
	}
}

func TestCurrentWorktreeUsesPathBoundaries(t *testing.T) {
	root := t.TempDir()
	items := []Worktree{{Path: filepath.Join(root, "app")}, {Path: filepath.Join(root, "app-two")}}
	got, err := CurrentWorktree(items, filepath.Join(root, "app-two", "src"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != filepath.Join(root, "app-two") {
		t.Fatalf("got %q", got.Path)
	}
}

func initGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	runGit(t, repo, "init", "-b", "main")
	runGit(t, repo, "config", "user.email", "gmux-test@example.com")
	runGit(t, repo, "config", "user.name", "gmux test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "initial")
	return repo
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}
