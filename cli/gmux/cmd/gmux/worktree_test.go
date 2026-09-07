package main

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gmuxapp/gmux/packages/paths"
	"github.com/gmuxapp/gmux/packages/workspace"
)

func TestBuildWorktreeRowsGroupsLiveLocalSessionsByDeepestCwd(t *testing.T) {
	root := t.TempDir()
	mainPath := filepath.Join(root, "app")
	featurePath := filepath.Join(root, "app-wt", "feature")
	items := []workspace.Worktree{
		{Path: mainPath, Branch: "main", Head: "aaa"},
		{Path: featurePath, Branch: "feature", Head: "bbb"},
	}
	sessions := []cliSession{
		{ID: "sess-main", Cwd: filepath.Join(mainPath, "src"), Alive: true},
		{ID: "sess-feature", Cwd: filepath.Join(featurePath, "pkg"), Alive: true},
		{ID: "sess-dead", Cwd: featurePath, Alive: false},
		{ID: "sess-peer", Cwd: featurePath, Alive: true, Peer: "remote"},
		{ID: "sess-prefix", Cwd: filepath.Join(root, "app-wt-other"), Alive: true},
	}
	rows, err := buildWorktreeRows(items, sessions, featurePath, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("len = %d", len(rows))
	}
	byBranch := map[string]worktreeRow{}
	for _, row := range rows {
		byBranch[row.Branch] = row
	}
	if len(byBranch["main"].Sessions) != 1 || byBranch["main"].Sessions[0].ID != "sess-main" {
		t.Fatalf("main sessions = %#v", byBranch["main"].Sessions)
	}
	if len(byBranch["feature"].Sessions) != 1 || byBranch["feature"].Sessions[0].ID != "sess-feature" || !byBranch["feature"].Current {
		t.Fatalf("feature row = %#v", byBranch["feature"])
	}
}

func TestEncodeWorktreeRecoveryJSONPreservesSessionProvenance(t *testing.T) {
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	encodeErr := encodeWorktreeRecoveryJSON(worktreeCreateResult{
		Worktree:  workspace.Worktree{Path: "/tmp/worktree", Branch: "fix/test"},
		SessionID: "sess-recover", PID: 42,
	}, os.ErrClosed)
	_ = w.Close()
	os.Stdout = old
	body, _ := io.ReadAll(r)
	_ = r.Close()
	if encodeErr != nil {
		t.Fatal(encodeErr)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	if got["path"] != "/tmp/worktree" || got["session_id"] != "sess-recover" || got["delivery_error"] == "" {
		t.Fatalf("recovery payload=%v", got)
	}
}

func TestCreateWorktreeDefaultPathAndAgentLaunch(t *testing.T) {
	repo := initCLIWorktreeRepo(t)
	var launchedPath, sentPrompt string
	deps := worktreeCreateDeps{
		validateLauncher: func(agent string) error {
			if agent != "pi" {
				t.Fatalf("agent = %q", agent)
			}
			return nil
		},
		launch: func(path, agent string) (string, int, error) { launchedPath = path; return "sess-test", 42, nil },
		sendPrompt: func(id, agent, prompt string) error {
			if id != "sess-test" || agent != "pi" {
				t.Fatalf("id=%q agent=%q", id, agent)
			}
			sentPrompt = prompt
			return nil
		},
		kill: func(string) error { return nil },
	}
	got, err := createWorktree(worktreeCreateRequest{Repo: repo, Name: "fix/login", Base: "HEAD", Agent: "pi", Prompt: "fix it"}, deps)
	if err != nil {
		t.Fatal(err)
	}
	repoPath, err := mirroredWorktreeRepoPath(repo)
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(paths.WorktreesDir(), repoPath, "fix-login")
	if got.Path != cleanWorktreePath(wantPath) || launchedPath != got.Path || sentPrompt != "fix it" || got.SessionID != "sess-test" {
		t.Fatalf("result=%#v launched=%q prompt=%q", got, launchedPath, sentPrompt)
	}
	if _, err := os.Stat(filepath.Join(got.Path, ".git")); err != nil {
		t.Fatalf("created .git: %v", err)
	}
}

func TestCreateWorktreeLaunchFailurePreservesCheckout(t *testing.T) {
	repo := initCLIWorktreeRepo(t)
	deps := worktreeCreateDeps{
		validateLauncher: func(string) error { return nil },
		launch:           func(string, string) (string, int, error) { return "", 0, os.ErrPermission },
		sendPrompt:       func(string, string, string) error { return nil },
		kill:             func(string) error { return nil },
	}
	_, err := createWorktree(worktreeCreateRequest{Repo: repo, Name: "failed", Base: "HEAD", Agent: "pi"}, deps)
	if err == nil || !strings.Contains(err.Error(), "worktree preserved") {
		t.Fatalf("launch error = %v", err)
	}
	repoPath, pathErr := mirroredWorktreeRepoPath(repo)
	if pathErr != nil {
		t.Fatal(pathErr)
	}
	path := filepath.Join(paths.WorktreesDir(), repoPath, "failed")
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("worktree was not preserved: %v", statErr)
	}
}

func TestCreateWorktreePiPromptFailureLeavesInDoubtSessionRunning(t *testing.T) {
	repo := initCLIWorktreeRepo(t)
	killed := ""
	deps := worktreeCreateDeps{
		validateLauncher: func(string) error { return nil },
		launch:           func(string, string) (string, int, error) { return "sess-prompt", 7, nil },
		sendPrompt:       func(string, string, string) error { return os.ErrClosed },
		kill:             func(id string) error { killed = id; return nil },
	}
	_, err := createWorktree(worktreeCreateRequest{Repo: repo, Name: "prompt-failed", Agent: "pi", Prompt: "fix"}, deps)
	if err == nil || !strings.Contains(err.Error(), "delivery may be in doubt") || !strings.Contains(err.Error(), "worktree preserved") {
		t.Fatalf("prompt error = %v", err)
	}
	if killed != "" {
		t.Fatalf("in-doubt Pi session was killed: %q", killed)
	}
	repoPath, pathErr := mirroredWorktreeRepoPath(repo)
	if pathErr != nil {
		t.Fatal(pathErr)
	}
	path := filepath.Join(paths.WorktreesDir(), repoPath, "prompt-failed")
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("worktree was not preserved: %v", statErr)
	}
}

func TestCreateWorktreeNonPiPromptFailureReportsKillFailure(t *testing.T) {
	repo := initCLIWorktreeRepo(t)
	deps := worktreeCreateDeps{
		validateLauncher: func(string) error { return nil },
		launch:           func(string, string) (string, int, error) { return "sess-live", 7, nil },
		sendPrompt:       func(string, string, string) error { return os.ErrClosed },
		kill:             func(string) error { return os.ErrPermission },
	}
	_, err := createWorktree(worktreeCreateRequest{Repo: repo, Name: "kill-failed", Agent: "claude", Prompt: "fix"}, deps)
	if err == nil || !strings.Contains(err.Error(), "could not stop session sess-live") {
		t.Fatalf("prompt error = %v", err)
	}
}

func TestCreateWorktreeMirrorsCanonicalRepoPath(t *testing.T) {
	repo := initCLIWorktreeRepo(t)
	link := filepath.Join(filepath.Dir(repo), "repo-alias")
	if err := os.Symlink(repo, link); err != nil {
		t.Fatal(err)
	}
	got, err := createWorktree(worktreeCreateRequest{Repo: link, Name: "via/link"}, worktreeCreateDeps{})
	if err != nil {
		t.Fatal(err)
	}
	repoPath, err := mirroredWorktreeRepoPath(repo)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(paths.WorktreesDir(), repoPath, "via-link")
	if got.Path != cleanWorktreePath(want) {
		t.Fatalf("path = %q, want %q", got.Path, cleanWorktreePath(want))
	}
}

func TestCreateWorktreeExplicitPathRemainsUnchanged(t *testing.T) {
	repo := initCLIWorktreeRepo(t)
	want := filepath.Join(t.TempDir(), "custom", "checkout")
	got, err := createWorktree(worktreeCreateRequest{Repo: repo, Name: "custom-path", Path: want}, worktreeCreateDeps{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != cleanWorktreePath(want) {
		t.Fatalf("path = %q, want %q", got.Path, cleanWorktreePath(want))
	}
}

func TestMirroredWorktreeRepoPathKeepsFullPath(t *testing.T) {
	root := t.TempDir()
	one, err := mirroredWorktreeRepoPath(filepath.Join(root, "one", "app"))
	if err != nil {
		t.Fatal(err)
	}
	two, err := mirroredWorktreeRepoPath(filepath.Join(root, "two", "app"))
	if err != nil {
		t.Fatal(err)
	}
	if one == two {
		t.Fatalf("same-named repositories collided at %q", one)
	}
	if filepath.Base(one) != "app" || filepath.Base(two) != "app" {
		t.Fatalf("mirrored paths = %q, %q", one, two)
	}
	if filepath.IsAbs(one) || filepath.IsAbs(two) {
		t.Fatalf("mirrored paths must be relative: %q, %q", one, two)
	}

	rootPath, err := mirroredWorktreeRepoPath(string(filepath.Separator))
	if err != nil {
		t.Fatal(err)
	}
	if rootPath != "_root" {
		t.Fatalf("root path = %q, want _root", rootPath)
	}
}

func TestMirroredWorktreeVolumeStripsWindowsDeviceSyntax(t *testing.T) {
	tests := []struct {
		volume string
		want   string
	}{
		{`C:`, "C"},
		{`\\server\share`, filepath.Join("server", "share")},
		{`\\?\C:`, "C"},
		{`\\?\UNC\server\share`, filepath.Join("server", "share")},
	}
	for _, tt := range tests {
		if got := mirroredWorktreeVolume(tt.volume); got != tt.want {
			t.Errorf("mirroredWorktreeVolume(%q) = %q, want %q", tt.volume, got, tt.want)
		}
	}
}

func TestCreateWorktreeRejectsNestedAndSymlinkedDestinations(t *testing.T) {
	repo := initCLIWorktreeRepo(t)
	deps := worktreeCreateDeps{}
	for _, path := range []string{filepath.Join(repo, ".worktrees", "nested")} {
		if _, err := createWorktree(worktreeCreateRequest{Repo: repo, Name: "nested", Path: path}, deps); err == nil || !strings.Contains(err.Error(), "outside existing worktrees") {
			t.Fatalf("nested destination error = %v", err)
		}
	}
	link := filepath.Join(filepath.Dir(repo), "repo-link")
	if err := os.Symlink(repo, link); err != nil {
		t.Fatal(err)
	}
	if _, err := createWorktree(worktreeCreateRequest{Repo: repo, Name: "symlinked", Path: filepath.Join(link, "nested")}, deps); err == nil || !strings.Contains(err.Error(), "outside existing worktrees") {
		t.Fatalf("symlinked destination error = %v", err)
	}
}

func TestCreateWorktreeRejectsOversizedSemanticPrompt(t *testing.T) {
	repo := initCLIWorktreeRepo(t)
	deps := worktreeCreateDeps{validateLauncher: func(string) error { return nil }}
	prompt := strings.Repeat("x", maxSendBytes+1)
	if _, err := createWorktree(worktreeCreateRequest{Repo: repo, Name: "large-prompt", Agent: "pi", Prompt: prompt}, deps); err == nil || !strings.Contains(err.Error(), "transport limit") {
		t.Fatalf("prompt limit error = %v", err)
	}
}

func TestCreateWorktreeRejectsInvalidBranchBeforeLauncher(t *testing.T) {
	repo := initCLIWorktreeRepo(t)
	called := false
	deps := worktreeCreateDeps{validateLauncher: func(string) error { called = true; return nil }}
	if _, err := createWorktree(worktreeCreateRequest{Repo: repo, Name: "-bad", Agent: "pi"}, deps); err == nil {
		t.Fatal("expected invalid branch error")
	}
	if called {
		t.Fatal("launcher validation should not run after invalid git input")
	}
}

func initCLIWorktreeRepo(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	// The trailing space catches accidental TrimSpace corruption of Git paths.
	repo := filepath.Join(t.TempDir(), "repo with spaces ")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-b", "main"}, {"config", "user.email", "test@example.com"}, {"config", "user.name", "test"}} {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "README.md"}, {"commit", "-m", "initial"}} {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	return repo
}

func TestBuildWorktreeRowsSelector(t *testing.T) {
	root := t.TempDir()
	items := []workspace.Worktree{{Path: filepath.Join(root, "main"), Branch: "main"}, {Path: filepath.Join(root, "feature"), Branch: "feature"}}
	rows, err := buildWorktreeRows(items, nil, filepath.Join(root, "main"), "branch:feature")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Branch != "feature" {
		t.Fatalf("rows = %#v", rows)
	}
}
