package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gmuxapp/gmux/services/gmuxd/internal/config"
)

func TestDecodeFrontendSettingsPatch(t *testing.T) {
	valid := []string{
		`{"vsCodeServerUrl":"https://code.example.test"}`,
		`{"vsCodeServerHomeDir":""}`,
		`{"vsCodeServerUrl":"","vsCodeServerHomeDir":"/home/rhee"}`,
	}
	for _, body := range valid {
		if _, err := decodeFrontendSettingsPatch(strings.NewReader(body)); err != nil {
			t.Errorf("valid body %s: %v", body, err)
		}
	}

	invalid := []string{
		``, `null`, `[]`, `{}`,
		`{"unknown":"x"}`,
		`{"vsCodeServerUrl":null}`,
		`{"vsCodeServerUrl":1}`,
		`{"vsCodeServerUrl":"a","vsCodeServerUrl":"b"}`,
		`{"vsCodeServerUrl":"a"} {}`,
		strings.Repeat(" ", maxFrontendSettingsPatchBytes+1),
	}
	for _, body := range invalid {
		if _, err := decodeFrontendSettingsPatch(strings.NewReader(body)); err == nil {
			t.Errorf("invalid body accepted: %q", body)
		}
	}
}

func TestNormalizeVSCodeServerConfig(t *testing.T) {
	for _, value := range []string{"https://code.example.test", "http://127.0.0.1:8766/base/", ""} {
		if _, err := normalizeVSCodeServerURL(value); err != nil {
			t.Errorf("URL %q: %v", value, err)
		}
	}
	for _, value := range []string{"code.example.test", "file:///tmp/code", "https://user:pass@example.test", "javascript:alert(1)", "https://example.test/\nnext"} {
		if _, err := normalizeVSCodeServerURL(value); err == nil {
			t.Errorf("invalid URL accepted: %q", value)
		}
	}
	if got, err := normalizeVSCodeServerHomeDir(" /home/rhee/../code/ "); err != nil || got != "/home/code" {
		t.Fatalf("normalized home = %q, %v", got, err)
	}
	for _, value := range []string{"~", "home/rhee", "/home/\nrhee"} {
		if _, err := normalizeVSCodeServerHomeDir(value); err == nil {
			t.Errorf("invalid home accepted: %q", value)
		}
	}
}

func TestHandleFrontendConfigPatch(t *testing.T) {
	var got config.SettingsPatch
	update := func(patch config.SettingsPatch) (json.RawMessage, error) {
		got = patch
		return json.RawMessage(`{"vsCodeServerUrl":"https://code.example.test","fontSize":16}`), nil
	}
	req := httptest.NewRequest(http.MethodPatch, "/v1/frontend-config", strings.NewReader(`{
		"vsCodeServerUrl":" https://code.example.test ",
		"vsCodeServerHomeDir":"/home/rhee/"
	}`))
	res := httptest.NewRecorder()
	handleFrontendConfigPatch(res, req, update)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", res.Code, res.Body.String())
	}
	if got.VSCodeServerURL == nil || *got.VSCodeServerURL != "https://code.example.test" {
		t.Fatalf("URL patch = %#v", got.VSCodeServerURL)
	}
	if got.VSCodeServerHomeDir == nil || *got.VSCodeServerHomeDir != "/home/rhee" {
		t.Fatalf("home patch = %#v", got.VSCodeServerHomeDir)
	}
	if !strings.Contains(res.Body.String(), `"fontSize":16`) {
		t.Fatalf("response = %s", res.Body.String())
	}
}

func TestHandleFrontendConfigPatchErrors(t *testing.T) {
	t.Run("validation", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPatch, "/v1/frontend-config", strings.NewReader(`{"vsCodeServerUrl":"file:///tmp/code"}`))
		res := httptest.NewRecorder()
		handleFrontendConfigPatch(res, req, func(config.SettingsPatch) (json.RawMessage, error) {
			t.Fatal("update called for invalid request")
			return nil, nil
		})
		if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "validation_error") {
			t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
		}
	})

	t.Run("conflict", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPatch, "/v1/frontend-config", strings.NewReader(`{"vsCodeServerUrl":""}`))
		res := httptest.NewRecorder()
		handleFrontendConfigPatch(res, req, func(config.SettingsPatch) (json.RawMessage, error) {
			return nil, config.ErrSettingsConflict
		})
		if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "invalid_config") {
			t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
		}
	})

	t.Run("internal", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPatch, "/v1/frontend-config", strings.NewReader(`{"vsCodeServerUrl":""}`))
		res := httptest.NewRecorder()
		handleFrontendConfigPatch(res, req, func(config.SettingsPatch) (json.RawMessage, error) {
			return nil, errors.New("disk path secret")
		})
		if res.Code != http.StatusInternalServerError || strings.Contains(res.Body.String(), "secret") {
			t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
		}
	})
}
