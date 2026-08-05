package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestExternalFrontendHandlerServesSPA(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "index.html"), "<html>gmux</html>")
	mustWrite(t, filepath.Join(dir, "manifest.json"), `{"name":"gmux"}`)
	mustWrite(t, filepath.Join(dir, "sw.js"), `self.addEventListener('fetch', () => {})`)
	if err := os.Mkdir(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "assets", "app.js"), "console.log('gmux')")

	h, err := externalFrontendHandler(dir)
	if err != nil {
		t.Fatal(err)
	}

	assertFrontendResponse(t, h, "/", http.StatusOK, "<html>gmux</html>", "no-cache")
	assertFrontendResponse(t, h, "/manifest.json", http.StatusOK, `{"name":"gmux"}`, "no-cache")
	assertFrontendResponse(t, h, "/sw.js", http.StatusOK, `self.addEventListener('fetch', () => {})`, "no-cache")
	assertFrontendResponse(t, h, "/projects/gmux/session/abc", http.StatusOK, "<html>gmux</html>", "no-cache")
	assertFrontendResponse(t, h, "/assets/app.js", http.StatusOK, "console.log('gmux')", "public, max-age=31536000, immutable")
	assertFrontendResponse(t, h, "/v1/sessions", http.StatusNotFound, "", "")
	assertFrontendResponse(t, h, "/ws/session", http.StatusNotFound, "", "")
}

func TestExternalFrontendHandlerRequiresIndex(t *testing.T) {
	_, err := externalFrontendHandler(t.TempDir())
	if err == nil {
		t.Fatal("expected error for directory without index.html")
	}
}

func TestFrontendFSPathCleansURLPath(t *testing.T) {
	cases := map[string]string{
		"/":                "index.html",
		"":                 "index.html",
		"/assets/app.js":   "assets/app.js",
		"/foo/../bar":      "bar",
		"/../index.html":   "index.html",
		"//assets//app.js": "assets/app.js",
	}
	for in, want := range cases {
		if got := frontendFSPath(in); got != want {
			t.Fatalf("frontendFSPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func mustWrite(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertFrontendResponse(t *testing.T, h http.Handler, path string, wantStatus int, wantBody, wantCache string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != wantStatus {
		t.Fatalf("%s status = %d, want %d; body=%q", path, rr.Code, wantStatus, rr.Body.String())
	}
	if wantBody != "" && rr.Body.String() != wantBody {
		t.Fatalf("%s body = %q, want %q", path, rr.Body.String(), wantBody)
	}
	if got := rr.Header().Get("Cache-Control"); got != wantCache {
		t.Fatalf("%s Cache-Control = %q, want %q", path, got, wantCache)
	}
}
