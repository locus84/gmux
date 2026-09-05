package ptyserver

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

func replayFullFrame(payload []byte) []byte {
	out := append(bytes.Clone(replayBSU), []byte("\x1b[2J\x1b[H\x1b[3J")...)
	out = append(out, payload...)
	return append(out, replayESU...)
}

func TestRawReplayCommitsFullFrameAndSuffix(t *testing.T) {
	var replay rawReplay
	frame := replayFullFrame([]byte("frame"))
	replay.write(append([]byte("discarded"), frame...))
	replay.write([]byte("post"))

	want := append(bytes.Clone(frame), []byte("post")...)
	if got := replay.bytes(); !bytes.Equal(got, want) {
		t.Fatalf("replay = %q, want %q", got, want)
	}
}

func TestRawReplayMarkersAcrossEveryWriteBoundary(t *testing.T) {
	frame := replayFullFrame([]byte("payload"))
	want := append(bytes.Clone(frame), []byte("suffix")...)
	stream := append(bytes.Clone(frame), []byte("suffix")...)

	for split := 0; split <= len(stream); split++ {
		var replay rawReplay
		replay.write(stream[:split])
		replay.write(stream[split:])
		if got := replay.bytes(); !bytes.Equal(got, want) {
			t.Fatalf("split %d: replay = %q, want %q", split, got, want)
		}
	}
}

func TestRawReplayKeepsFragmentedKittyPayloadAtomic(t *testing.T) {
	// Marker-shaped bytes inside APC payload are opaque and must not start or
	// finish a synchronized frame.
	kitty := []byte("\x1b_Gf=100,a=T,m=0;AAAA\x1b[?2026lBBBB\x1b\\")
	frame := replayFullFrame(kitty)

	for split := 1; split < len(kitty); split++ {
		var replay rawReplay
		prefix := len(replayBSU) + len("\x1b[2J\x1b[H\x1b[3J")
		replay.write(frame[:prefix+split])
		if replay.safeBoundary() {
			t.Fatalf("split %d: Kitty payload reported a safe attach boundary", split)
		}
		replay.write(frame[prefix+split:])
		if got := replay.bytes(); !bytes.Equal(got, frame) {
			t.Fatalf("split %d: replay did not preserve Kitty frame", split)
		}
	}
}

func TestRawReplaySupportsSixelAndIIPStringTerminators(t *testing.T) {
	payloads := [][]byte{
		[]byte("\x1bPq~sixel\x1b\\"),
		[]byte("\x1b]1337;File=inline=1;size=4:AAAA\a"),
		[]byte("\x1b]1337;File=inline=1;size=4:AAAA\x1b\\"),
	}
	for _, payload := range payloads {
		var replay rawReplay
		frame := replayFullFrame(payload)
		for _, b := range frame {
			replay.write([]byte{b})
		}
		if got := replay.bytes(); !bytes.Equal(got, frame) {
			t.Fatalf("payload %q: replay mismatch", payload)
		}
	}
}

func TestRawReplayWaitsDuringIncompleteFrame(t *testing.T) {
	var replay rawReplay
	old := replayFullFrame([]byte("old"))
	replay.write(old)
	replay.write(append(bytes.Clone(replayBSU), []byte("\x1b[2J\x1b[H\x1b[3J\x1b_Ga=T;partial")...))

	if replay.safeBoundary() {
		t.Fatal("incomplete synchronized Kitty frame reported safe")
	}
	if got := replay.bytes(); got != nil {
		t.Fatalf("incomplete replay = %q, want nil", got)
	}
}

func TestRawReplayRejectsPartialFrameWithoutReplacingCheckpoint(t *testing.T) {
	var replay rawReplay
	checkpoint := replayFullFrame([]byte("checkpoint"))
	partial := append(bytes.Clone(replayBSU), []byte("\x1b[3Aupdate")...)
	partial = append(partial, replayESU...)
	replay.write(checkpoint)
	replay.write(partial)

	want := append(bytes.Clone(checkpoint), partial...)
	if got := replay.bytes(); !bytes.Equal(got, want) {
		t.Fatalf("replay = %q, want checkpoint + partial frame", got)
	}
}

func TestRawReplayNewCheckpointReplacesOldSuffix(t *testing.T) {
	var replay rawReplay
	old := replayFullFrame([]byte("old"))
	newer := replayFullFrame([]byte("new"))
	replay.write(old)
	replay.write([]byte("old suffix"))
	replay.write(newer)
	replay.write([]byte("new suffix"))

	want := append(bytes.Clone(newer), []byte("new suffix")...)
	if got := replay.bytes(); !bytes.Equal(got, want) {
		t.Fatalf("replay = %q, want %q", got, want)
	}
}

func TestRawReplayCapOverflowFallsBack(t *testing.T) {
	var replay rawReplay
	replay.write(replayFullFrame([]byte("valid")))
	replay.suffix = make([]byte, replaySuffixLimit)
	replay.write([]byte("x"))
	if replay.valid || replay.bytes() != nil {
		t.Fatal("suffix overflow did not invalidate raw replay")
	}

	replay.startCandidate()
	replay.candidate = make([]byte, replayCheckpointLimit)
	replay.candidateErase = true
	replay.candidateHome = true
	replay.candidateScrollback = true
	replay.write(append([]byte("x"), replayESU...))
	if replay.valid || replay.bytes() != nil {
		t.Fatal("checkpoint overflow did not fall back")
	}
	if len(replay.candidate) != 0 {
		t.Fatalf("oversized candidate retained %d bytes", len(replay.candidate))
	}
}

func TestRawReplayRequiresScrollbackReset(t *testing.T) {
	var replay rawReplay
	frame := append(bytes.Clone(replayBSU), []byte("\x1b[2J\x1b[Hcontent")...)
	frame = append(frame, replayESU...)
	replay.write(frame)
	if got := replay.bytes(); got != nil {
		t.Fatalf("frame without 3J was accepted: %q", got)
	}
}

func TestRawReplayInvalidatesUnboundedCSI(t *testing.T) {
	var replay rawReplay
	replay.write(replayFullFrame([]byte("valid")))
	replay.write(append([]byte("\x1b["), bytes.Repeat([]byte("1"), replayCSILimit+1)...))
	if replay.valid || replay.bytes() != nil {
		t.Fatal("oversized CSI did not invalidate raw replay")
	}
	if !replay.safeBoundary() {
		t.Fatal("parser did not recover to a safe boundary after oversized CSI")
	}
}

func TestRawReplayPlainTextHasNoCheckpoint(t *testing.T) {
	var replay rawReplay
	replay.write([]byte("plain text\r\n"))
	if got := replay.bytes(); got != nil {
		t.Fatalf("plain replay = %q, want ANSI snapshot fallback", got)
	}
}

func TestPTYServerAttachUsesRawReplayCheckpointForMultipleClients(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "raw.sock")
	srv, err := New(Config{
		Command:    []string{"bash", "-c", "sleep 30"},
		Cwd:        "/tmp",
		Listener:   mustBindSocket(t, sockPath),
		SocketPath: sockPath,
		Cols:       80,
		Rows:       24,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	defer srv.Shutdown()

	kitty := append([]byte("\x1b_Gf=100,a=T,m=0;"), bytes.Repeat([]byte("A"), replayMessageLimit+32)...)
	kitty = append(kitty, []byte("\x1b\\")...)
	frame := replayFullFrame(kitty)
	suffix := []byte("\x1b[4;1Hafter-image")
	srv.mu.Lock()
	srv.replay.write(frame)
	srv.replay.write(suffix)
	srv.mu.Unlock()
	want := append(bytes.Clone(frame), suffix...)

	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		conn, _, err := websocket.Dial(ctx, "ws://localhost/", &websocket.DialOptions{
			HTTPClient: &http.Client{Transport: &http.Transport{
				DialContext: func(context.Context, string, string) (net.Conn, error) {
					return net.Dial("unix", sockPath)
				},
			}},
		})
		if err != nil {
			cancel()
			t.Fatalf("client %d dial: %v", i, err)
		}
		conn.SetReadLimit(2 * replayMessageLimit)
		var got []byte
		messages := 0
		for len(got) < len(want) {
			_, data, err := conn.Read(ctx)
			if err != nil {
				conn.Close(websocket.StatusNormalClosure, "")
				cancel()
				t.Fatalf("client %d read: %v", i, err)
			}
			got = append(got, data...)
			messages++
		}
		if messages < 2 {
			t.Fatalf("client %d replay was not split below proxy limits", i)
		}
		conn.Close(websocket.StatusNormalClosure, "")
		cancel()
		if !bytes.Equal(got, want) {
			t.Fatalf("client %d first frame mismatch\n got: %q\nwant: %q", i, got, want)
		}
	}
}
