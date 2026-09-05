package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gmuxapp/gmux/services/gmuxd/internal/centralstore"
)

func TestProjectWorktreesHandlerListsPrimaryAndLinked(t *testing.T) {
	repo := initProjectWorktreeRepo(t)
	linked := filepath.Join(t.TempDir(), "fix-auth")
	runProjectGit(t, repo, "worktree", "add", "-b", "fix/auth", linked)

	sessions := projectStoreForRoot(t, "gmux", repo)

	req := httptest.NewRequest(http.MethodGet, "/v1/projects/gmux/worktrees", nil)
	rr := httptest.NewRecorder()
	projectWorktreesHandler(rr, req, "gmux", sessions)
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

func TestProjectWorktreeCreateHandlerCreatesManagedCheckout(t *testing.T) {
	repo := initProjectWorktreeRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "local-only.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	sessions := projectStoreForRoot(t, "gmux", repo)
	rr := createProjectWorktree(t, sessions, map[string]string{"branch": "fix/auth", "base": "HEAD"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var body struct {
		Data struct {
			ProjectSlug string          `json:"project_slug"`
			Worktree    projectWorktree `json:"worktree"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Data.ProjectSlug != "gmux" || body.Data.Worktree.Branch != "fix/auth" || body.Data.Worktree.Primary {
		t.Fatalf("unexpected response: %#v", body)
	}
	if _, err := os.Stat(filepath.Join(body.Data.Worktree.Path, "local-only.txt")); !os.IsNotExist(err) {
		t.Fatalf("dirty source file was copied: %v", err)
	}
	if duplicate := createProjectWorktree(t, sessions, map[string]string{"branch": "fix/auth"}); duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate status = %d body=%s", duplicate.Code, duplicate.Body.String())
	}
}

func TestProjectWorktreeCreateHandlerRejectsInvalidRequests(t *testing.T) {
	repo := initProjectWorktreeRepo(t)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	sessions := projectStoreForRoot(t, "gmux", repo)
	for _, tc := range []struct {
		name string
		body map[string]string
	}{
		{name: "missing branch", body: map[string]string{}},
		{name: "invalid branch", body: map[string]string{"branch": "-bad"}},
		{name: "invalid base", body: map[string]string{"branch": "bad-base", "base": "--missing"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := createProjectWorktree(t, sessions, tc.body)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
			}
		})
	}

	plain := projectStoreForRoot(t, "gmux", t.TempDir())
	if rr := createProjectWorktree(t, plain, map[string]string{"branch": "feature"}); rr.Code != http.StatusBadRequest {
		t.Fatalf("non-Git status = %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestProjectWorktreeDeleteHandlerRemovesCheckoutAndPreservesBranch(t *testing.T) {
	repo := initProjectWorktreeRepo(t)
	linked := filepath.Join(t.TempDir(), "feature")
	runProjectGit(t, repo, "worktree", "add", "-b", "feature", linked)
	sessions := projectStoreForRoot(t, "gmux", repo)

	rr := deleteProjectWorktree(t, sessions, linked)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(linked); !os.IsNotExist(err) {
		t.Fatalf("linked checkout still exists: %v", err)
	}
	cmd := exec.Command("git", "-C", repo, "branch", "--list", "feature")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "feature") {
		t.Fatalf("branch missing: err=%v out=%s", err, out)
	}
}

func TestProjectWorktreeDeleteHandlerRejectsUnsafeTargets(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, linked string, sessions *centralstore.Store)
		path  func(repo, linked string) string
		want  string
	}{
		{
			name: "primary",
			path: func(repo, linked string) string { return repo },
			want: "primary checkout",
		},
		{
			name: "dirty",
			setup: func(t *testing.T, linked string, sessions *centralstore.Store) {
				if err := os.WriteFile(filepath.Join(linked, "new.txt"), []byte("dirty\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			want: "uncommitted, untracked, or ignored",
		},
		{
			name: "session",
			setup: func(t *testing.T, linked string, sessions *centralstore.Store) {
				cwd := filepath.Join(linked, "src")
				if err := os.Mkdir(cwd, 0o755); err != nil {
					t.Fatal(err)
				}
				if _, _, err := sessions.InsertSession(context.Background(), centralstore.NewSession{ID: "worktree", Adapter: "shell", Command: []string{"sh"}, CWD: cwd, CreatedAt: 1}); err != nil {
					t.Fatal(err)
				}
			},
			want: "live or resumable session",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := initProjectWorktreeRepo(t)
			linked := filepath.Join(t.TempDir(), "feature")
			runProjectGit(t, repo, "worktree", "add", "-b", "feature", linked)
			sessions := projectStoreForRoot(t, "gmux", repo)
			if tc.setup != nil {
				tc.setup(t, linked, sessions)
			}
			target := linked
			if tc.path != nil {
				target = tc.path(repo, linked)
			}
			rr := deleteProjectWorktree(t, sessions, target)
			if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), tc.want) {
				t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
			}
			if _, err := os.Stat(linked); err != nil {
				t.Fatalf("linked checkout removed: %v", err)
			}
		})
	}
}

func TestProjectWorktreeDeleteHandlerRejectsConfiguredLinkedCheckout(t *testing.T) {
	repo := initProjectWorktreeRepo(t)
	linked := filepath.Join(t.TempDir(), "configured")
	runProjectGit(t, repo, "worktree", "add", "-b", "configured", linked)

	rr := deleteProjectWorktree(t, projectStoreForRoot(t, "gmux", linked), linked)
	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "configured project checkout") {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(linked); err != nil {
		t.Fatalf("configured checkout removed: %v", err)
	}
}

func TestProjectWorktreesHandlerReturnsPrimaryForNonGitProject(t *testing.T) {
	root := t.TempDir()
	sessions := projectStoreForRoot(t, "plain", root)
	req := httptest.NewRequest(http.MethodGet, "/v1/projects/plain/worktrees", nil)
	rr := httptest.NewRecorder()
	projectWorktreesHandler(rr, req, "plain", sessions)
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
	sessions := projectStoreForRoots(t, "multi", t.TempDir(), t.TempDir())
	req := httptest.NewRequest(http.MethodGet, "/v1/projects/multi/worktrees", nil)
	rr := httptest.NewRecorder()
	projectWorktreesHandler(rr, req, "multi", sessions)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestPendingWorktreeLaunchReservationTracksContainingCheckout(t *testing.T) {
	worktreeLifecycleMu.Lock()
	token := reserveWorktreeLaunch("/repo/linked/src")
	defer func() {
		delete(pendingWorktreeLaunches, token)
		worktreeLifecycleMu.Unlock()
	}()
	if !hasPendingWorktreeLaunch("/repo/linked") {
		t.Fatal("containing worktree did not see pending launch")
	}
	if hasPendingWorktreeLaunch("/repo/other") {
		t.Fatal("unrelated worktree saw pending launch")
	}
}

func projectStoreForRoot(t *testing.T, slug, root string) *centralstore.Store {
	t.Helper()
	return projectStoreForRoots(t, slug, root)
}

func projectStoreForRoots(t *testing.T, slug string, roots ...string) *centralstore.Store {
	t.Helper()
	store, err := centralstore.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	rules := make([]centralstore.MatchRule, 0, len(roots))
	for _, root := range roots {
		rules = append(rules, centralstore.MatchRule{Path: root})
	}
	if _, _, err := store.ReplaceProjectCatalog(context.Background(), []centralstore.ProjectEntrySpec{{Owned: &centralstore.OwnedProjectSpec{Slug: slug, Rules: rules}}}, 1); err != nil {
		t.Fatal(err)
	}
	return store
}

func createProjectWorktree(t *testing.T, sessions *centralstore.Store, body map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/projects/gmux/worktrees", bytes.NewReader(payload))
	rr := httptest.NewRecorder()
	projectWorktreeCreateHandler(rr, req, "gmux", sessions)
	return rr
}

func deleteProjectWorktree(t *testing.T, sessions *centralstore.Store, path string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]string{"path": path})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodDelete, "/v1/projects/gmux/worktrees", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	projectWorktreeDeleteHandler(rr, req, "gmux", sessions)
	return rr
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
