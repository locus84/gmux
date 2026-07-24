package main

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func startWorkspaceTestDaemon(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	// Keep the Unix socket path below macOS's short sockaddr_un limit even
	// when the system test temp directory lives under /var/folders/....
	stateDir, err := os.MkdirTemp("/tmp", "gmux-ws-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(stateDir) })
	t.Setenv("XDG_STATE_HOME", stateDir)
	sockDir := filepath.Join(stateDir, "gmux")
	t.Setenv("GMUX_STATE_DIR", sockDir)
	if err := os.MkdirAll(sockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", filepath.Join(sockDir, "gmuxd.sock"))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/health", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": map[string]any{"version": version}})
	})
	mux.HandleFunc("/v1/projects/add", handler)
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	t.Cleanup(func() {
		srv.Close()
		ln.Close()
	})
}

func TestAddWorkspacePostsResolvedDirectory(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "project")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "project-link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}

	startWorkspaceTestDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		var request struct {
			Paths []string `json:"paths"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if len(request.Paths) != 1 || request.Paths[0] != resolved {
			t.Errorf("paths = %v, want [%s]", request.Paths, resolved)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"data": map[string]any{
				"slug":  "project",
				"match": []map[string]string{{"path": "~/code/project"}},
			},
		})
	})

	result, err := addWorkspace(link)
	if err != nil {
		t.Fatalf("addWorkspace: %v", err)
	}
	if result.Slug != "project" || result.Path != "~/code/project" {
		t.Errorf("result = %#v", result)
	}
}

func TestAddWorkspaceRejectsInvalidLocalPath(t *testing.T) {
	if _, err := addWorkspace(filepath.Join(t.TempDir(), "missing")); err == nil || !strings.Contains(err.Error(), "no such file") {
		t.Fatalf("missing path error = %v", err)
	}
	file := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := addWorkspace(file); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("file path error = %v", err)
	}
}

func TestAddWorkspaceSurfacesDaemonConflict(t *testing.T) {
	dir := t.TempDir()
	startWorkspaceTestDaemon(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]any{
			"ok":    false,
			"error": map[string]string{"code": "validation_error", "message": "path is already used by workspace gmux"},
		})
	})

	_, err := addWorkspace(dir)
	if err == nil || !strings.Contains(err.Error(), "already used") {
		t.Fatalf("error = %v", err)
	}
}

func TestAddWorkspaceRejectsMalformedSuccess(t *testing.T) {
	dir := t.TempDir()
	startWorkspaceTestDaemon(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("not json"))
	})

	_, err := addWorkspace(dir)
	if err == nil || !strings.Contains(err.Error(), "invalid response") {
		t.Fatalf("error = %v", err)
	}
}
