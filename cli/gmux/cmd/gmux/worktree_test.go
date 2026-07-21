package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
		sendPrompt: func(id, prompt string) error {
			if id != "sess-test" {
				t.Fatalf("id = %q", id)
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
	wantPath := filepath.Join(filepath.Dir(repo), filepath.Base(repo)+"-wt", "fix-login")
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
		sendPrompt:       func(string, string) error { return nil },
		kill:             func(string) error { return nil },
	}
	_, err := createWorktree(worktreeCreateRequest{Repo: repo, Name: "failed", Base: "HEAD", Agent: "pi"}, deps)
	if err == nil || !strings.Contains(err.Error(), "worktree preserved") {
		t.Fatalf("launch error = %v", err)
	}
	path := filepath.Join(filepath.Dir(repo), filepath.Base(repo)+"-wt", "failed")
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("worktree was not preserved: %v", statErr)
	}
}

func TestCreateWorktreePromptFailureKillsSessionAndPreservesCheckout(t *testing.T) {
	repo := initCLIWorktreeRepo(t)
	killed := ""
	deps := worktreeCreateDeps{
		validateLauncher: func(string) error { return nil },
		launch:           func(string, string) (string, int, error) { return "sess-prompt", 7, nil },
		sendPrompt:       func(string, string) error { return os.ErrClosed },
		kill:             func(id string) error { killed = id; return nil },
	}
	_, err := createWorktree(worktreeCreateRequest{Repo: repo, Name: "prompt-failed", Agent: "pi", Prompt: "fix"}, deps)
	if err == nil || !strings.Contains(err.Error(), "worktree preserved") {
		t.Fatalf("prompt error = %v", err)
	}
	if killed != "sess-prompt" {
		t.Fatalf("killed = %q", killed)
	}
	path := filepath.Join(filepath.Dir(repo), filepath.Base(repo)+"-wt", "prompt-failed")
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("worktree was not preserved: %v", statErr)
	}
}

func TestCreateWorktreePromptFailureReportsKillFailure(t *testing.T) {
	repo := initCLIWorktreeRepo(t)
	deps := worktreeCreateDeps{
		validateLauncher: func(string) error { return nil },
		launch:           func(string, string) (string, int, error) { return "sess-live", 7, nil },
		sendPrompt:       func(string, string) error { return os.ErrClosed },
		kill:             func(string) error { return os.ErrPermission },
	}
	_, err := createWorktree(worktreeCreateRequest{Repo: repo, Name: "kill-failed", Agent: "pi", Prompt: "fix"}, deps)
	if err == nil || !strings.Contains(err.Error(), "could not stop session sess-live") {
		t.Fatalf("prompt error = %v", err)
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

func TestCreateWorktreeReservesEnterByteInPromptLimit(t *testing.T) {
	repo := initCLIWorktreeRepo(t)
	deps := worktreeCreateDeps{validateLauncher: func(string) error { return nil }}
	prompt := strings.Repeat("x", maxSendBytes)
	if _, err := createWorktree(worktreeCreateRequest{Repo: repo, Name: "large-prompt", Agent: "pi", Prompt: prompt}, deps); err == nil || !strings.Contains(err.Error(), "including Enter") {
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
