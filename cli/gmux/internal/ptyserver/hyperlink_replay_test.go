package ptyserver

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/vt"
)

func TestCheckpointPreservesHyperlinks(t *testing.T) {
	const uri = "http://127.0.0.1:18080/proxy/3000/?v=scrollbar-startup;a=b"
	for _, params := range []string{"", "id=preview"} {
		t.Run(params, func(t *testing.T) {
			source := vt.NewEmulator(80, 5)
			defer source.Close()
			opening := "\x1b]8;" + params + ";" + uri + "\x07"
			if _, err := source.WriteString(opening + "WebGL 확인\x1b]8;;\x07!"); err != nil {
				t.Fatal(err)
			}
			frame := snapshotFrame(source, false)
			if !strings.Contains(string(frame), "\x1b]8;"+params+";"+uri) {
				t.Fatalf("checkpoint lost hyperlink: %q", frame)
			}
			replay := vt.NewEmulator(80, 5)
			defer replay.Close()
			if _, err := replay.Write(frame); err != nil {
				t.Fatal(err)
			}
			for _, x := range []int{0, 1, 2, 3, 4, 5, 6, 8} {
				if got := replay.CellAt(x, 0).Link; got.URL != uri || got.Params != params {
					t.Fatalf("cell %d link=%+v", x, got)
				}
			}
			if got := replay.CellAt(10, 0).Link.URL; got != "" {
				t.Fatalf("link leaked after close: %q", got)
			}
		})
	}
}
