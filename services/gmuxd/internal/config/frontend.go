package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/tailscale/hujson"
	"github.com/tidwall/jsonc"
)

var (
	settingsMu = sync.Mutex{}

	// ErrSettingsConflict means the existing settings file cannot be safely
	// patched without risking lost or ambiguous user configuration.
	ErrSettingsConflict = errors.New("settings conflict")
)

type SettingsPatch struct {
	VSCodeServerURL     *string
	VSCodeServerHomeDir *string
}

// LoadTheme reads ~/.config/gmux/theme.jsonc, strips JSONC comments,
// and returns the raw JSON. Returns nil (not an error) if the file is missing.
// The file contains terminal colors in Windows Terminal theme format.
func LoadTheme() (json.RawMessage, error) {
	return loadJSONC(filepath.Join(Dir(), "theme.jsonc"))
}

// LoadSettings reads ~/.config/gmux/settings.jsonc, strips JSONC comments,
// and returns the raw JSON. Returns nil (not an error) if the file is missing.
// The file contains frontend preferences: keybinds, terminal options, UI prefs.
func LoadSettings() (json.RawMessage, error) {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	return loadJSONC(filepath.Join(Dir(), "settings.jsonc"))
}

// UpdateSettings patches only the supplied top-level settings while retaining
// JSONC comments, trailing commas, key order, and unrelated values. Writes are
// serialized and atomically replace a regular file. Symlinks are rejected, and
// external edits observed before the final rename return a conflict. As with
// standard temp-file replacement, an uncooperative editor can still race in the
// narrow interval between the final comparison and rename.
func UpdateSettings(patch SettingsPatch) (json.RawMessage, error) {
	settingsMu.Lock()
	defer settingsMu.Unlock()

	path := filepath.Join(Dir(), "settings.jsonc")
	original, mode, existed, err := readSettingsForWrite(path)
	if err != nil {
		return nil, err
	}
	if !existed {
		original = []byte("{}\n")
		mode = 0o600
	}

	value, err := hujson.Parse(original)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid JSONC", ErrSettingsConflict)
	}
	obj, ok := value.Value.(*hujson.Object)
	if !ok {
		return nil, fmt.Errorf("%w: settings root is not an object", ErrSettingsConflict)
	}
	if err := rejectDuplicateSettings(obj, patch); err != nil {
		return nil, err
	}
	if patch.VSCodeServerURL != nil {
		setJSONObjectString(obj, "vsCodeServerUrl", *patch.VSCodeServerURL)
	}
	if patch.VSCodeServerHomeDir != nil {
		setJSONObjectString(obj, "vsCodeServerHomeDir", *patch.VSCodeServerHomeDir)
	}

	updated := value.Pack()
	standard := jsonc.ToJSON(updated)
	if !json.Valid(standard) {
		return nil, fmt.Errorf("%w: patched settings are invalid", ErrSettingsConflict)
	}
	if err := writeSettingsAtomic(path, original, updated, mode, existed); err != nil {
		return nil, err
	}
	return json.RawMessage(standard), nil
}

func readSettingsForWrite(path string) (data []byte, mode os.FileMode, existed bool, err error) {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, false, nil
		}
		return nil, 0, false, fmt.Errorf("read settings metadata: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, 0, false, fmt.Errorf("%w: settings file is not regular", ErrSettingsConflict)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		return nil, 0, false, fmt.Errorf("read settings: %w", err)
	}
	return data, info.Mode().Perm(), true, nil
}

func rejectDuplicateSettings(obj *hujson.Object, patch SettingsPatch) error {
	counts := map[string]int{}
	for _, member := range obj.Members {
		name, ok := member.Name.Value.(hujson.Literal)
		if !ok || name.Kind() != '"' {
			continue
		}
		counts[name.String()]++
	}
	for key, supplied := range map[string]bool{
		"vsCodeServerUrl":     patch.VSCodeServerURL != nil,
		"vsCodeServerHomeDir": patch.VSCodeServerHomeDir != nil,
	} {
		if supplied && counts[key] > 1 {
			return fmt.Errorf("%w: duplicate %s", ErrSettingsConflict, key)
		}
	}
	return nil
}

func setJSONObjectString(obj *hujson.Object, key, value string) {
	for i := range obj.Members {
		name, ok := obj.Members[i].Name.Value.(hujson.Literal)
		if ok && name.Kind() == '"' && name.String() == key {
			obj.Members[i].Value.Value = hujson.String(value)
			return
		}
	}

	var trailing hujson.Extra
	if len(obj.Members) > 0 {
		last := &obj.Members[len(obj.Members)-1]
		trailing = last.Value.AfterExtra
		last.Value.AfterExtra = nil
	}
	before := hujson.Extra("\n  ")
	if len(obj.Members) == 0 && len(obj.AfterExtra) == 0 {
		before = nil
	}
	obj.Members = append(obj.Members, hujson.ObjectMember{
		Name:  hujson.Value{BeforeExtra: before, Value: hujson.String(key)},
		Value: hujson.Value{BeforeExtra: hujson.Extra(" "), Value: hujson.String(value), AfterExtra: trailing},
	})
}

func writeSettingsAtomic(path string, original, updated []byte, mode os.FileMode, existed bool) (retErr error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".settings.jsonc-*")
	if err != nil {
		return fmt.Errorf("create temporary settings: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		if tmp != nil {
			_ = tmp.Close()
		}
		_ = os.Remove(tmpPath)
	}()
	if err := tmp.Chmod(mode); err != nil {
		return fmt.Errorf("set settings permissions: %w", err)
	}
	if _, err := tmp.Write(updated); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync settings: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close settings: %w", err)
	}
	tmp = nil

	current, _, currentExists, err := readSettingsForWrite(path)
	if err != nil {
		return err
	}
	if currentExists != existed || (existed && !bytes.Equal(current, original)) {
		return fmt.Errorf("%w: settings changed while saving", ErrSettingsConflict)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace settings: %w", err)
	}
	return nil
}

// loadJSONC reads a file, strips // and /* */ comments and trailing commas,
// then validates the result as JSON. Returns nil for missing files.
func loadJSONC(path string) (json.RawMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	stripped := jsonc.ToJSON(data)

	if !json.Valid(stripped) {
		return nil, fmt.Errorf("parsing %s: invalid JSON after stripping comments", path)
	}

	return json.RawMessage(stripped), nil
}
