package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	pathpkg "path"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gmuxapp/gmux/services/gmuxd/internal/config"
)

const maxFrontendSettingsPatchBytes = 4 << 10

type frontendSettingsPatch struct {
	VSCodeServerURL     *string
	VSCodeServerHomeDir *string
}

func handleFrontendConfigPatch(
	w http.ResponseWriter,
	r *http.Request,
	update func(config.SettingsPatch) (json.RawMessage, error),
) {
	patch, err := decodeFrontendSettingsPatch(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if patch.VSCodeServerURL != nil {
		value, err := normalizeVSCodeServerURL(*patch.VSCodeServerURL)
		if err != nil {
			writeError(w, http.StatusBadRequest, "validation_error", err.Error())
			return
		}
		patch.VSCodeServerURL = &value
	}
	if patch.VSCodeServerHomeDir != nil {
		value, err := normalizeVSCodeServerHomeDir(*patch.VSCodeServerHomeDir)
		if err != nil {
			writeError(w, http.StatusBadRequest, "validation_error", err.Error())
			return
		}
		patch.VSCodeServerHomeDir = &value
	}

	settings, err := update(config.SettingsPatch{
		VSCodeServerURL:     patch.VSCodeServerURL,
		VSCodeServerHomeDir: patch.VSCodeServerHomeDir,
	})
	if err != nil {
		if errors.Is(err, config.ErrSettingsConflict) {
			writeError(w, http.StatusConflict, "invalid_config", "settings.jsonc could not be safely updated; check it for syntax errors, duplicate keys, or external changes")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal", "failed to save frontend settings")
		return
	}
	writeJSON(w, map[string]any{
		"ok": true,
		"data": map[string]any{
			"settings": settings,
		},
	})
}

func decodeFrontendSettingsPatch(body io.Reader) (frontendSettingsPatch, error) {
	data, err := io.ReadAll(io.LimitReader(body, maxFrontendSettingsPatchBytes+1))
	if err != nil {
		return frontendSettingsPatch{}, fmt.Errorf("read request body")
	}
	if len(data) > maxFrontendSettingsPatchBytes {
		return frontendSettingsPatch{}, fmt.Errorf("request body is too large")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return frontendSettingsPatch{}, fmt.Errorf("invalid JSON body")
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return frontendSettingsPatch{}, fmt.Errorf("body must be a JSON object")
	}
	var patch frontendSettingsPatch
	seen := map[string]bool{}
	for dec.More() {
		rawKey, err := dec.Token()
		if err != nil {
			return frontendSettingsPatch{}, fmt.Errorf("invalid JSON body")
		}
		key, ok := rawKey.(string)
		if !ok || seen[key] {
			return frontendSettingsPatch{}, fmt.Errorf("duplicate or invalid field %q", key)
		}
		seen[key] = true
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return frontendSettingsPatch{}, fmt.Errorf("invalid value for %s", key)
		}
		if string(raw) == "null" {
			return frontendSettingsPatch{}, fmt.Errorf("%s must be a string", key)
		}
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return frontendSettingsPatch{}, fmt.Errorf("%s must be a string", key)
		}
		switch key {
		case "vsCodeServerUrl":
			patch.VSCodeServerURL = &value
		case "vsCodeServerHomeDir":
			patch.VSCodeServerHomeDir = &value
		default:
			return frontendSettingsPatch{}, fmt.Errorf("unknown field %q", key)
		}
	}
	if _, err := dec.Token(); err != nil {
		return frontendSettingsPatch{}, fmt.Errorf("invalid JSON body")
	}
	if tok, err := dec.Token(); err != io.EOF || tok != nil {
		return frontendSettingsPatch{}, fmt.Errorf("body must contain one JSON object")
	}
	if len(seen) == 0 {
		return frontendSettingsPatch{}, fmt.Errorf("at least one setting is required")
	}
	return patch, nil
}

func normalizeVSCodeServerURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if len(value) > 2048 || !utf8.ValidString(value) || containsUnsafeSettingRune(value) {
		return "", fmt.Errorf("VS Code Server URL is invalid")
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return "", fmt.Errorf("VS Code Server URL must be an absolute HTTP(S) URL without credentials")
	}
	return value, nil
}

func normalizeVSCodeServerHomeDir(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if len(value) > 4096 || !utf8.ValidString(value) || containsUnsafeSettingRune(value) || !pathpkg.IsAbs(value) {
		return "", fmt.Errorf("VS Code Server home directory must be an absolute POSIX path")
	}
	return pathpkg.Clean(value), nil
}

func containsUnsafeSettingRune(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return true
		}
	}
	return false
}
