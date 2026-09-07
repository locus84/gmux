package adapters

import (
	"os/exec"
	"path/filepath"

	"github.com/gmuxapp/gmux/packages/adapter"
)

// Compile-time interface checks.
var (
	_ adapter.Launchable = (*Hermes)(nil)
)

func init() {
	All = append(All, NewHermes())
}

// Hermes is the adapter for the Hermes agent CLI. It is intentionally minimal:
// gmux can launch and classify Hermes sessions, while terminal title fallback
// handles display until Hermes exposes a stable hook or session-file format.
type Hermes struct{}

func NewHermes() *Hermes { return &Hermes{} }

func (h *Hermes) Name() string { return "hermes" }

func (h *Hermes) Discover() bool {
	_, err := exec.LookPath("hermes")
	return err == nil
}

// Match returns true if any argument before "--" is the `hermes` binary.
func (h *Hermes) Match(cmd []string) bool {
	for _, arg := range cmd {
		if arg == "--" {
			break
		}
		if filepath.Base(arg) == "hermes" {
			return true
		}
	}
	return false
}

// Env returns no extra environment variables.
func (h *Hermes) Env(_ adapter.EnvContext) []string { return nil }

func (h *Hermes) Launchers() []adapter.Launcher {
	return []adapter.Launcher{{
		ID:          "hermes",
		Label:       "Hermes",
		Command:     []string{"hermes"},
		Description: "Coding Agent",
	}}
}
