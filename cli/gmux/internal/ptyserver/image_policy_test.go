package ptyserver

import "testing"

func TestChildEnvPiUsesViewerLinks(t *testing.T) {
	for _, inherited := range []string{"", "kitty", "iterm2", "none"} {
		t.Run(inherited, func(t *testing.T) {
			env := buildChildEnv([]string{"PI_IMAGE_PROTOCOL=" + inherited, "PI_HYPERLINKS=1"}, []string{"PI_IMAGE_PROTOCOL=kitty"}, "test")
			if got := envValue(env, "PI_IMAGE_PROTOCOL"); got != "none" {
				t.Fatalf("PI_IMAGE_PROTOCOL = %q, want none (no reserved image rows)", got)
			}
			if got := envValue(env, "PI_HYPERLINKS"); got != "1" {
				t.Fatalf("hyperlink preference was changed: %q", got)
			}
			if got := envValue(env, "KITTY_WINDOW_ID"); got != "1" {
				t.Fatalf("non-Pi terminal capabilities changed: %q", got)
			}
		})
	}
}
