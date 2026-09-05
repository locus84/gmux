package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAddProjectPostsResolvedDirectory(t *testing.T) {
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

	d := startStubDaemon(t, nil)
	d.on(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"data": map[string]any{
				"slug":     "project",
				"match":    []map[string]string{{"path": "~/code/project"}},
				"existing": true,
			},
		})
	})

	result, err := addProject(link)
	if err != nil {
		t.Fatalf("addProject: %v", err)
	}
	if result.Slug != "project" || result.Path != "~/code/project" || !result.Existing {
		t.Fatalf("result=%+v", result)
	}
	request := d.lastRequest(t)
	var payload struct {
		Paths []string `json:"paths"`
	}
	if err := json.Unmarshal([]byte(request.body), &payload); err != nil {
		t.Fatalf("decode recorded request: %v", err)
	}
	if request.path != "/v1/projects/add" || len(payload.Paths) != 1 || payload.Paths[0] != resolved {
		t.Fatalf("request=%+v paths=%v", request, payload.Paths)
	}
}

func TestAddProjectRejectsInvalidLocalPath(t *testing.T) {
	if _, err := addProject(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing path accepted")
	}
	file := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := addProject(file); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("file error=%v", err)
	}
}
