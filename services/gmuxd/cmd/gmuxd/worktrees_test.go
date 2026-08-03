package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	projectspkg "github.com/gmuxapp/gmux/services/gmuxd/internal/projects"
	"github.com/gmuxapp/gmux/services/gmuxd/internal/store"
)

func TestProjectWorktreesHandlerListsPrimaryAndLinked(t *testing.T) {
	repo := initProjectWorktreeRepo(t)
	linked := filepath.Join(t.TempDir(), "fix-auth")
	runProjectGit(t, repo, "worktree", "add", "-b", "fix/auth", linked)

	mgr := projectspkg.NewManager(t.TempDir())
	if _, err := mgr.AddProject("gmux", []projectspkg.MatchRule{{Path: repo}}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/projects/gmux/worktrees", nil)
	rr := httptest.NewRecorder()
	projectWorktreesHandler(rr, req, "gmux", mgr, store.New())
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var body struct {
		OK   bool             `json:"ok"`
		Data projectWorktrees `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.OK || body.Data.ProjectSlug != "gmux" || len(body.Data.Worktrees) != 2 {
		t.Fatalf("unexpected response: %#v", body)
	}
	if !body.Data.Worktrees[0].Primary || body.Data.Worktrees[0].Path != body.Data.PrimaryPath {
		t.Fatalf("primary checkout not explicit: %#v", body.Data)
	}
	if body.Data.Worktrees[1].Primary || body.Data.Worktrees[1].Branch != "fix/auth" {
		t.Fatalf("linked checkout mismatch: %#v", body.Data.Worktrees[1])
	}
}

func TestProjectWorktreesHandlerReturnsPrimaryForNonGitProject(t *testing.T) {
	root := t.TempDir()
	mgr := projectspkg.NewManager(t.TempDir())
	if _, err := mgr.AddProject("plain", []projectspkg.MatchRule{{Path: root}}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/projects/plain/worktrees", nil)
	rr := httptest.NewRecorder()
	projectWorktreesHandler(rr, req, "plain", mgr, store.New())
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var body struct {
		Data projectWorktrees `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Data.PrimaryPath == "" || len(body.Data.Worktrees) != 0 {
		t.Fatalf("unexpected non-Git response: %#v", body.Data)
	}
}

func TestProjectWorktreesHandlerRejectsMultipleProjectRoots(t *testing.T) {
	mgr := projectspkg.NewManager(t.TempDir())
	if _, err := mgr.AddProject("multi", []projectspkg.MatchRule{{Path: t.TempDir()}, {Path: t.TempDir()}}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/projects/multi/worktrees", nil)
	rr := httptest.NewRecorder()
	projectWorktreesHandler(rr, req, "multi", mgr, store.New())
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
}

func initProjectWorktreeRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runProjectGit(t, repo, "init")
	runProjectGit(t, repo, "config", "user.email", "test@example.com")
	runProjectGit(t, repo, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runProjectGit(t, repo, "add", "README.md")
	runProjectGit(t, repo, "commit", "-m", "init")
	return repo
}

func runProjectGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
