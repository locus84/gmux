package vt

import "testing"

func TestHyperlinkFields(t *testing.T) {
	for _, tt := range []struct{ name, params, uri string }{
		{"empty params", "", "http://127.0.0.1:18080/proxy/3000/?v=scrollbar-startup"},
		{"id", "id=preview", "https://example.com/"},
		{"semicolon in URI", "id=preview", "https://example.com/a;b?q=c;d"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := NewEmulator(80, 5)
			defer e.Close()
			_, err := e.WriteString("\x1b]8;" + tt.params + ";" + tt.uri + "\x07X\x1b]8;;\x07Y")
			if err != nil {
				t.Fatal(err)
			}
			link := e.CellAt(0, 0).Link
			if link.URL != tt.uri || link.Params != tt.params {
				t.Fatalf("link=%+v, want URL=%q Params=%q", link, tt.uri, tt.params)
			}
			if link := e.CellAt(1, 0).Link; link.URL != "" || link.Params != "" {
				t.Fatalf("closing OSC 8 did not clear link: %+v", link)
			}
		})
	}
}
