package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestLoadTheme_MissingFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	data, err := LoadTheme()
	if err != nil {
		t.Fatal(err)
	}
	if data != nil {
		t.Errorf("expected nil for missing file, got %s", data)
	}
}

func TestLoadTheme_ValidJSON(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	writeFile(t, dir, "theme.jsonc", `{"background": "#282a36"}`)

	data, err := LoadTheme()
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"background": "#282a36"}` {
		t.Errorf("got %s", data)
	}
}

func TestLoadTheme_StripsComments(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	writeFile(t, dir, "theme.jsonc", `{
  // Dark background
  "background": "#282a36",
  /* Dracula foreground */
  "foreground": "#f8f8f2"
}`)

	data, err := LoadTheme()
	if err != nil {
		t.Fatal(err)
	}
	if data == nil {
		t.Fatal("expected non-nil data")
	}
}

func TestLoadTheme_StripsTrailingCommas(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	writeFile(t, dir, "theme.jsonc", `{
  "background": "#282a36",
  "foreground": "#f8f8f2",
}`)

	data, err := LoadTheme()
	if err != nil {
		t.Fatal(err)
	}
	if data == nil {
		t.Fatal("expected non-nil data")
	}
}

func TestLoadTheme_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	writeFile(t, dir, "theme.jsonc", `{invalid json}`)

	_, err := LoadTheme()
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestLoadSettings_MissingFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	data, err := LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if data != nil {
		t.Errorf("expected nil for missing file, got %s", data)
	}
}

func TestLoadSettings_ValidObject(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	writeFile(t, dir, "settings.jsonc", `{
  // Terminal font
  "fontSize": 16,
  // Remap ctrl+t
  "keybinds": [
    { "key": "ctrl+alt+t", "action": "sendKeys", "args": "ctrl+t" },
  ],
}`)

	data, err := LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if data == nil {
		t.Fatal("expected non-nil data")
	}
}

func TestUpdateSettingsPreservesJSONCAndUnrelatedValues(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path := filepath.Join(dir, "gmux", "settings.jsonc")
	writeFile(t, dir, "settings.jsonc", `{
  // keep this terminal setting
  "fontSize": 16,
  "nested": { "enabled": true },
}`)
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	serverURL := "https://code.example.test/base/"
	homeDir := "/home/rhee"
	settings, err := UpdateSettings(SettingsPatch{VSCodeServerURL: &serverURL, VSCodeServerHomeDir: &homeDir})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(settings), `"fontSize": 16`) || !strings.Contains(string(settings), `"vsCodeServerUrl": "https://code.example.test/base/"`) {
		t.Fatalf("returned settings = %s", settings)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"// keep this terminal setting", `"nested": { "enabled": true }`, `"vsCodeServerUrl": "https://code.example.test/base/"`, `"vsCodeServerHomeDir": "/home/rhee"`} {
		if !strings.Contains(string(written), want) {
			t.Errorf("written settings missing %q:\n%s", want, written)
		}
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o640 {
		t.Errorf("mode = %o, want 640", info.Mode().Perm())
	}
}

func TestUpdateSettingsCreatesAndClearsValues(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	serverURL := "https://code.example.test"
	if _, err := UpdateSettings(SettingsPatch{VSCodeServerURL: &serverURL}); err != nil {
		t.Fatal(err)
	}
	homeDir := "/Users/rhee/"
	if _, err := UpdateSettings(SettingsPatch{VSCodeServerHomeDir: &homeDir}); err != nil {
		t.Fatal(err)
	}
	empty := ""
	if _, err := UpdateSettings(SettingsPatch{VSCodeServerURL: &empty}); err != nil {
		t.Fatal(err)
	}
	settings, err := LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	text := string(settings)
	if !strings.Contains(text, `"vsCodeServerUrl": ""`) || !strings.Contains(text, `"vsCodeServerHomeDir": "/Users/rhee/"`) {
		t.Fatalf("settings = %s", settings)
	}
	info, err := os.Stat(filepath.Join(dir, "gmux", "settings.jsonc"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("new file mode = %o, want 600", info.Mode().Perm())
	}
}

func TestUpdateSettingsRejectsUnsafeExistingFiles(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
	}{
		{name: "malformed", content: `{broken`},
		{name: "non object", content: `[]`},
		{name: "duplicate target", content: `{"vsCodeServerUrl":"a","vsCodeServerUrl":"b"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", dir)
			writeFile(t, dir, "settings.jsonc", tc.content)
			value := "https://code.example.test"
			_, err := UpdateSettings(SettingsPatch{VSCodeServerURL: &value})
			if !errors.Is(err, ErrSettingsConflict) {
				t.Fatalf("error = %v, want settings conflict", err)
			}
			got, _ := os.ReadFile(filepath.Join(dir, "gmux", "settings.jsonc"))
			if string(got) != tc.content {
				t.Fatalf("file changed on conflict: %q", got)
			}
		})
	}
}

func TestUpdateSettingsRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	gmuxDir := filepath.Join(dir, "gmux")
	if err := os.MkdirAll(gmuxDir, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "target.jsonc")
	if err := os.WriteFile(target, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(gmuxDir, "settings.jsonc")); err != nil {
		t.Fatal(err)
	}
	value := "https://code.example.test"
	if _, err := UpdateSettings(SettingsPatch{VSCodeServerURL: &value}); !errors.Is(err, ErrSettingsConflict) {
		t.Fatalf("error = %v, want settings conflict", err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != `{}` {
		t.Fatalf("symlink target changed: %s", got)
	}
}

func TestUpdateSettingsSerializesIndependentPatches(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	serverURL := "https://code.example.test"
	homeDir := "/home/rhee"
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if _, err := UpdateSettings(SettingsPatch{VSCodeServerURL: &serverURL}); err != nil {
			t.Errorf("URL update: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		if _, err := UpdateSettings(SettingsPatch{VSCodeServerHomeDir: &homeDir}); err != nil {
			t.Errorf("home update: %v", err)
		}
	}()
	wg.Wait()
	settings, err := LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(settings), serverURL) || !strings.Contains(string(settings), homeDir) {
		t.Fatalf("lost concurrent update: %s", settings)
	}
}

func writeFile(t *testing.T, xdgDir, name, content string) {
	t.Helper()
	dir := filepath.Join(xdgDir, "gmux")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)
}
