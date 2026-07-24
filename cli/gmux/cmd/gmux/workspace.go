package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type workspaceAddResult struct {
	Slug string
	Path string
}

func cmdWorkspace(c *command) int {
	switch c.workspaceSub {
	case "add":
		result, err := addWorkspace(c.workspacePath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "gmux:", err)
			return 1
		}
		fmt.Printf("Added workspace %s at %s\n", result.Slug, result.Path)
		return 0
	default:
		fmt.Fprintln(os.Stderr, "gmux: unsupported workspace command")
		return 2
	}
}

func addWorkspace(path string) (workspaceAddResult, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return workspaceAddResult{}, fmt.Errorf("resolve workspace path: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return workspaceAddResult{}, fmt.Errorf("workspace path %q: %w", path, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return workspaceAddResult{}, fmt.Errorf("workspace path %q: %w", path, err)
	}
	if !info.IsDir() {
		return workspaceAddResult{}, fmt.Errorf("workspace path %q is not a directory", path)
	}

	ensureGmuxd()
	// Another CLI process may already be starting or replacing gmuxd. Wait for
	// whichever daemon owns the socket to report healthy before mutating it.
	if !waitForGmuxd(3 * time.Second) {
		return workspaceAddResult{}, fmt.Errorf("gmuxd did not become ready")
	}

	payload, err := json.Marshal(struct {
		Paths []string `json:"paths"`
	}{Paths: []string{resolved}})
	if err != nil {
		return workspaceAddResult{}, fmt.Errorf("encode workspace request: %w", err)
	}
	resp, err := gmuxdClient().Post(gmuxdBaseURL()+"/v1/projects/add", "application/json", bytes.NewReader(payload))
	if err != nil {
		return workspaceAddResult{}, fmt.Errorf("contact gmuxd: %w", err)
	}
	defer resp.Body.Close()

	var envelope struct {
		OK   bool `json:"ok"`
		Data struct {
			Slug  string `json:"slug"`
			Match []struct {
				Path string `json:"path"`
			} `json:"match"`
		} `json:"data"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&envelope); err != nil {
		return workspaceAddResult{}, fmt.Errorf("gmuxd returned %s with an invalid response", resp.Status)
	}
	if resp.StatusCode != http.StatusOK || !envelope.OK {
		if envelope.Error.Message != "" {
			return workspaceAddResult{}, fmt.Errorf("add workspace: %s", envelope.Error.Message)
		}
		return workspaceAddResult{}, fmt.Errorf("add workspace: gmuxd returned %s", resp.Status)
	}
	if envelope.Data.Slug == "" {
		return workspaceAddResult{}, fmt.Errorf("add workspace: gmuxd response is missing a slug")
	}
	canonicalPath := resolved
	for _, rule := range envelope.Data.Match {
		if rule.Path != "" {
			canonicalPath = rule.Path
			break
		}
	}
	return workspaceAddResult{Slug: envelope.Data.Slug, Path: canonicalPath}, nil
}

func waitForGmuxd(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if gmuxdHealthy(250 * time.Millisecond) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}
