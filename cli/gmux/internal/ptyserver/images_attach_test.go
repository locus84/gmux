package ptyserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestImageAssetsReplayLiveAndHTTPKeepRawClientsUnchanged(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "pty.sock")
	png := testPNG(800, 600, 256*1024)
	image := kitty("a=T,f=100,q=2,C=1,c=40,r=20,i=7", []byte(base64.StdEncoding.EncodeToString(png)))
	initial := append([]byte("\x1b[?2026h\x1b[H\x1b[2J\x1b[3J"), image...)
	initial = append(initial, replayESU...)
	live := []byte("\x1b_Ga=d,d=a,q=2\x1b\\\x1b_Ga=p,q=2,i=7,c=40,r=20,C=1\x1b\\LIVE_END")
	first, next := filepath.Join(dir, "first"), filepath.Join(dir, "next")
	for path, data := range map[string][]byte{first: initial, next: live} {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	srv, err := New(Config{Command: []string{"bash", "-c", `stty -echo; cat "$1"; read -r _; cat "$2"; read -r _`, "image-test", first, next}, Cwd: dir, Listener: mustBindSocket(t, sock), SocketPath: sock})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Shutdown()
	deadline := time.Now().Add(3 * time.Second)
	for {
		srv.mu.Lock()
		ready := srv.replay.valid && srv.imageReplay.valid && srv.imageTransform.safeBoundary()
		srv.mu.Unlock()
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("initial image checkpoint never became ready")
		}
		time.Sleep(time.Millisecond)
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", sock)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dial := func(query string) *websocket.Conn {
		t.Helper()
		c, _, err := websocket.Dial(ctx, "ws://localhost/?client=browser"+query, &websocket.DialOptions{HTTPClient: client})
		if err != nil {
			t.Fatal(err)
		}
		c.SetReadLimit(32 << 20)
		t.Cleanup(func() { c.CloseNow() })
		return c
	}
	readUntil := func(c *websocket.Conn, end []byte) []byte {
		t.Helper()
		var out []byte
		for !bytes.Contains(out, end) {
			typ, data, err := c.Read(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if typ == websocket.MessageBinary {
				out = append(out, data...)
			}
		}
		return out
	}
	raw, refs := dial(""), dial("&images=refs-v1")
	original := readUntil(raw, replayESU)
	compact := readUntil(refs, replayESU)
	if !bytes.Equal(original, initial) {
		t.Fatalf("raw checkpoint changed: got %d want %d", len(original), len(initial))
	}
	if bytes.Contains(compact, image) || !bytes.Contains(compact, []byte("gmux-image;")) || len(compact) > 1024 {
		t.Fatalf("replay was not compact: %d bytes", len(compact))
	}
	sum := sha256.Sum256(png)
	hash := hex.EncodeToString(sum[:])
	response, err := client.Get("http://localhost/images/" + hash)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || !bytes.Equal(stored, png) {
		t.Fatal("HTTP image did not match emitted bytes")
	}
	if _, err := srv.WritePTY([]byte("next\n")); err != nil {
		t.Fatal(err)
	}
	if got := readUntil(raw, []byte("LIVE_END")); !bytes.Equal(got, live) {
		t.Fatalf("raw live output changed: %q", got)
	}
	if got := readUntil(refs, []byte("LIVE_END")); !bytes.Contains(got, []byte("gmux-image;")) || bytes.Contains(got, []byte("a=p")) {
		t.Fatalf("cached placement not translated: %q", got)
	}
	reconnect := dial("&images=refs-v1")
	got := readUntil(reconnect, []byte("LIVE_END"))
	if len(got) > 2048 || bytes.Contains(got, image) || !bytes.Contains(got, []byte(hash)) {
		t.Fatalf("reconnect retransmitted image or lost refs: %d bytes", len(got))
	}
	t.Logf("initial attach: raw=%d bytes, refs=%d bytes; cached-placement reconnect=%d bytes", len(original), len(compact), len(got))
}
