package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gmuxapp/gmux/services/gmuxd/internal/centralstore"
)

type fakeWorkspaceSessions map[centralstore.SessionID]centralstore.Session

func (s fakeWorkspaceSessions) Session(_ context.Context, id centralstore.SessionID) (centralstore.Session, bool, error) {
	session, ok := s[id]
	return session, ok, nil
}

func testSessionStore(root string) fakeWorkspaceSessions {
	return fakeWorkspaceSessions{"sess-files": {ID: "sess-files", CWD: root, WorkspaceRoot: root}}
}

func TestWorkspaceProjectFilesUseOwnedProjectRoot(t *testing.T) {
	ctx := context.Background()
	stateDir := t.TempDir()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("project root\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := centralstore.Open(ctx, stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, _, err := store.ReplaceProjectCatalog(ctx, []centralstore.ProjectEntrySpec{{
		Owned: &centralstore.OwnedProjectSpec{Slug: "demo", Rules: []centralstore.MatchRule{{Path: root}}},
	}}, 1); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/projects/demo/file?path=README.md", nil)
	rr := httptest.NewRecorder()
	workspaceProjectFilesContentHandler(rr, req, "demo", store)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "project root") {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/projects/missing/files", nil)
	rr = httptest.NewRecorder()
	workspaceProjectFilesListHandler(rr, req, "missing", store)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("missing status = %d body=%s", rr.Code, rr.Body.String())
	}

	if _, _, err := store.ReplaceProjectCatalog(ctx, []centralstore.ProjectEntrySpec{{
		Owned: &centralstore.OwnedProjectSpec{Slug: "multi", Rules: []centralstore.MatchRule{{Path: root}, {Path: t.TempDir()}}},
	}}, 2); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodGet, "/v1/projects/multi/files", nil)
	rr = httptest.NewRecorder()
	workspaceProjectFilesListHandler(rr, req, "multi", store)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "README.md") {
		t.Fatalf("multi-root canonical browse status = %d body=%s", rr.Code, rr.Body.String())
	}
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

func TestOpenWorkspaceResolvedPathRejectsPostValidationSymlinkSwap(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	inside := filepath.Join(root, "target.txt")
	if err := os.WriteFile(inside, []byte("inside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/file?path=target.txt", nil)
	_, rootReal, _, resolved, err := resolveWorkspaceFilePath(req, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(inside); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), inside); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	file, err := openWorkspaceResolvedPath(rootReal, resolved)
	if err == nil {
		file.Close()
		t.Fatal("post-validation symlink swap unexpectedly opened")
	}
	rr := httptest.NewRecorder()
	writeWorkspaceFileError(rr, err)
	if rr.Code != http.StatusForbidden || !strings.Contains(rr.Body.String(), "outside_root") {
		t.Fatalf("swap status = %d body=%s", rr.Code, rr.Body.String())
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

func TestValidSessionTempImageID(t *testing.T) {
	for _, id := range []string{"sess-123", "019f8275-596e-798b-afb6-c7d74e9c7ecf", "session_test.v1"} {
		if !validSessionTempImageID(id) {
			t.Errorf("validSessionTempImageID(%q) = false", id)
		}
	}
	for _, id := range []string{"", ".", "..", "../sess-1", "sess/1", "sess@peer", "세션"} {
		if validSessionTempImageID(id) {
			t.Errorf("validSessionTempImageID(%q) = true", id)
		}
	}
}

func TestSessionTempImageContent(t *testing.T) {
	tempRoot := t.TempDir()
	tempDir := sessionTempImageDir(tempRoot, "sess-files")
	if err := os.MkdirAll(tempDir, 0o700); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0}
	if err := os.WriteFile(filepath.Join(tempDir, "paste-37.png"), png, 0o600); err != nil {
		t.Fatal(err)
	}
	large, err := os.Create(filepath.Join(tempDir, "paste-38.png"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := large.Write(png); err != nil {
		t.Fatal(err)
	}
	if err := large.Truncate(workspaceImagePreviewLimit + 1); err != nil {
		t.Fatal(err)
	}
	if err := large.Close(); err != nil {
		t.Fatal(err)
	}
	sessions := testSessionStore(root)

	for _, raw := range []bool{false, true} {
		path := "/v1/sessions/sess-files/temp-file?name=paste-37.png"
		if raw {
			path += "&raw=1"
		}
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		sessionTempImageContentHandler(rr, req, "sess-files", sessions, tempRoot)
		if rr.Code != http.StatusOK {
			t.Fatalf("raw=%v status = %d body=%s", raw, rr.Code, rr.Body.String())
		}
		if raw && rr.Header().Get("Content-Type") != "image/png" {
			t.Fatalf("raw content-type = %q", rr.Header().Get("Content-Type"))
		}
		if !raw && !strings.Contains(rr.Body.String(), `"kind":"image"`) {
			t.Fatalf("metadata missing image kind: %s", rr.Body.String())
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/sess-files/temp-file?name=paste-38.png", nil)
	rr := httptest.NewRecorder()
	sessionTempImageContentHandler(rr, req, "sess-files", sessions, tempRoot)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("large image status = %d body=%s", rr.Code, rr.Body.String())
	}

	sessions["sess-other"] = centralstore.Session{ID: "sess-other", CWD: root, WorkspaceRoot: root}
	req = httptest.NewRequest(http.MethodGet, "/v1/sessions/sess-other/temp-file?name=paste-37.png", nil)
	rr = httptest.NewRecorder()
	sessionTempImageContentHandler(rr, req, "sess-other", sessions, tempRoot)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("cross-session image status = %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestSessionTempImageRejectsUnsafeFiles(t *testing.T) {
	tempRoot := t.TempDir()
	tempDir := sessionTempImageDir(tempRoot, "sess-files")
	if err := os.MkdirAll(tempDir, 0o700); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	sessions := testSessionStore(root)
	if err := os.WriteFile(filepath.Join(tempDir, "paste-1.png"), []byte("not an image"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(tempDir, "paste-2.png"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(tempDir, "paste-1.png"), filepath.Join(tempDir, "paste-3.png")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	cases := map[string]int{
		"../paste-1.png": http.StatusBadRequest,
		"paste-0.png":    http.StatusBadRequest,
		"paste-1.txt":    http.StatusBadRequest,
		"paste-1.png":    http.StatusUnsupportedMediaType,
		"paste-2.png":    http.StatusForbidden,
		"paste-3.png":    http.StatusForbidden,
		"paste-99.png":   http.StatusNotFound,
	}
	for name, want := range cases {
		req := httptest.NewRequest(http.MethodGet, "/v1/sessions/sess-files/temp-file?name="+name, nil)
		rr := httptest.NewRecorder()
		sessionTempImageContentHandler(rr, req, "sess-files", sessions, tempRoot)
		if rr.Code != want {
			t.Errorf("%s status = %d want %d body=%s", name, rr.Code, want, rr.Body.String())
		}
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
