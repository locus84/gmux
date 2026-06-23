package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	projectspkg "github.com/gmuxapp/gmux/services/gmuxd/internal/projects"
	"github.com/gmuxapp/gmux/services/gmuxd/internal/store"
)

func testSessionStore(root string) *store.Store {
	s := store.New()
	s.Upsert(store.Session{ID: "sess-files", Cwd: root, WorkspaceRoot: root, Alive: true})
	return s
}

func TestWorkspaceFilesListAndContent(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	sessions := testSessionStore(root)

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/sess-files/files", nil)
	rr := httptest.NewRecorder()
	workspaceSessionFilesListHandler(rr, req, "sess-files", sessions)
	if rr.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", rr.Code, rr.Body.String())
	}
	var list struct {
		OK   bool              `json:"ok"`
		Data workspaceFileList `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if !list.OK || len(list.Data.Entries) != 2 || list.Data.Entries[0].Name != "src" || list.Data.Entries[0].Type != "dir" {
		t.Fatalf("unexpected list: %#v", list)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/sessions/sess-files/file?path=README.md", nil)
	rr = httptest.NewRecorder()
	workspaceSessionFilesContentHandler(rr, req, "sess-files", sessions)
	if rr.Code != http.StatusOK {
		t.Fatalf("content status = %d body=%s", rr.Code, rr.Body.String())
	}
	var body struct {
		OK   bool                 `json:"ok"`
		Data workspaceFileContent `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Content != "# hello\n" || body.Data.Path != "README.md" {
		t.Fatalf("unexpected content: %#v", body.Data)
	}
}

func TestWorkspaceProjectFilesUseConfiguredPathWithoutSessions(t *testing.T) {
	root := t.TempDir()
	stateDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.gd"), []byte("extends Node\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mgr := projectspkg.NewManager(stateDir)
	if _, err := mgr.AddProject("gd-idle", []projectspkg.MatchRule{{Path: root}}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/projects/gd-idle/files", nil)
	rr := httptest.NewRecorder()
	workspaceProjectFilesListHandler(rr, req, "gd-idle", mgr, store.New())
	if rr.Code != http.StatusOK {
		t.Fatalf("project list status = %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "main.gd") {
		t.Fatalf("project listing missing file: %s", rr.Body.String())
	}
}

func TestWorkspaceFilesRejectTraversalAndSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "secret-link")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	sessions := testSessionStore(root)

	cases := []string{
		"/v1/sessions/sess-files/file?path=../secret.txt",
		"/v1/sessions/sess-files/file?path=secret-link",
	}
	for _, path := range cases {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		workspaceSessionFilesContentHandler(rr, req, "sess-files", sessions)
		if rr.Code != http.StatusForbidden && rr.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d body=%s", path, rr.Code, rr.Body.String())
		}
		if strings.Contains(rr.Body.String(), "nope") {
			t.Fatalf("leaked file content: %s", rr.Body.String())
		}
	}
}

func TestWorkspaceFilesPreviewImageAndServeRaw(t *testing.T) {
	root := t.TempDir()
	png := []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
		0x89, 0x00, 0x00, 0x00, 0x0a, 0x49, 0x44, 0x41,
		0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00,
		0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00,
		0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae,
		0x42, 0x60, 0x82,
	}
	if err := os.WriteFile(filepath.Join(root, "pixel.png"), png, 0o644); err != nil {
		t.Fatal(err)
	}
	sessions := testSessionStore(root)

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/sess-files/file?path=pixel.png", nil)
	rr := httptest.NewRecorder()
	workspaceSessionFilesContentHandler(rr, req, "sess-files", sessions)
	if rr.Code != http.StatusOK {
		t.Fatalf("image metadata status = %d body=%s", rr.Code, rr.Body.String())
	}
	var body struct {
		OK   bool                 `json:"ok"`
		Data workspaceFileContent `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Kind != "image" || body.Data.Mime != "image/png" || body.Data.Content != "" {
		t.Fatalf("unexpected image metadata: %#v", body.Data)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/sessions/sess-files/file?path=pixel.png&raw=1", nil)
	rr = httptest.NewRecorder()
	workspaceSessionFilesContentHandler(rr, req, "sess-files", sessions)
	if rr.Code != http.StatusOK {
		t.Fatalf("raw image status = %d body=%s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "image/png" {
		t.Fatalf("content-type = %q, want image/png", ct)
	}
	got, err := io.ReadAll(rr.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(png) {
		t.Fatalf("raw image bytes changed: got %d want %d", len(got), len(png))
	}
}

func TestWorkspaceFilesRejectBinaryAndTooLarge(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "bin.dat"), []byte{'a', 0, 'b'}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "fake.png"), []byte("<html>not an image</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	large := strings.Repeat("x", workspaceFilePreviewLimit+1)
	if err := os.WriteFile(filepath.Join(root, "large.txt"), []byte(large), 0o644); err != nil {
		t.Fatal(err)
	}
	sessions := testSessionStore(root)

	for name, want := range map[string]int{"bin.dat": http.StatusUnsupportedMediaType, "large.txt": http.StatusRequestEntityTooLarge} {
		req := httptest.NewRequest(http.MethodGet, "/v1/sessions/sess-files/file?path="+name, nil)
		rr := httptest.NewRecorder()
		workspaceSessionFilesContentHandler(rr, req, "sess-files", sessions)
		if rr.Code != want {
			t.Fatalf("%s status = %d want %d body=%s", name, rr.Code, want, rr.Body.String())
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/sess-files/file?path=fake.png&raw=1", nil)
	rr := httptest.NewRecorder()
	workspaceSessionFilesContentHandler(rr, req, "sess-files", sessions)
	if rr.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("fake raw image status = %d want %d body=%s", rr.Code, http.StatusUnsupportedMediaType, rr.Body.String())
	}
}
