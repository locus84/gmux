package main

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gmuxapp/gmux/services/gmuxd/internal/centralstore"
	"github.com/gmuxapp/gmux/services/gmuxd/internal/sessioncoord"
)

type imageSessionLookup struct {
	row   centralstore.Session
	found bool
	err   error
}

func (s imageSessionLookup) Session(context.Context, centralstore.SessionID) (centralstore.Session, bool, error) {
	return s.row, s.found, s.err
}

func TestTerminalImageHandlerValidatesHashBeforeSessionLookup(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/s/images/BAD", nil)
	terminalImageHandler(rr, req, "s", "BAD", imageSessionLookup{err: context.Canceled}, sessioncoord.NewRegistry())
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "invalid_hash") {
		t.Fatalf("response=%d %s", rr.Code, rr.Body.String())
	}
}

func TestTerminalImageHandlerReturnsTypedExpiredForEndedRunner(t *testing.T) {
	hash := strings.Repeat("a", 64)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/s/images/"+hash, nil)
	terminalImageHandler(rr, req, "s", hash, imageSessionLookup{found: true}, sessioncoord.NewRegistry())
	if rr.Code != http.StatusGone || !strings.Contains(rr.Body.String(), "image_expired") {
		t.Fatalf("response=%d %s", rr.Code, rr.Body.String())
	}
}

func TestServeTerminalImageFromRunnerPreservesImmutableHeadersAndBody(t *testing.T) {
	f, err := os.CreateTemp("/tmp", "gmux-image-*.sock")
	if err != nil {
		t.Fatal(err)
	}
	path := f.Name()
	f.Close()
	os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	defer ln.Close()
	png := []byte("\x89PNG\r\n\x1a\nbody")
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images/"+strings.Repeat("b", 64) {
			t.Errorf("path=%q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(png)
	})}
	go srv.Serve(ln)
	defer srv.Close()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	serveTerminalImageFromRunner(rr, req, path, strings.Repeat("b", 64))
	if rr.Code != http.StatusOK || !bytes.Equal(rr.Body.Bytes(), png) || rr.Header().Get("Content-Type") != "image/png" || rr.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(rr.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("response=%d headers=%v body=%q", rr.Code, rr.Header(), rr.Body.Bytes())
	}
}
