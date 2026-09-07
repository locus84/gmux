package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

type projectAddResult struct {
	Slug     string
	Path     string
	Existing bool
}

func cmdProject(c *command) int {
	switch c.projectSub {
	case "add":
		result, err := addProject(c.projectPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "gmux:", err)
			return 1
		}
		if result.Existing {
			fmt.Printf("project %s already registered at %s\n", result.Slug, result.Path)
		} else {
			fmt.Printf("added project %s at %s\n", result.Slug, result.Path)
		}
		return 0
	default:
		fmt.Fprintln(os.Stderr, "gmux: unsupported project command")
		return 1
	}
}

func addProject(path string) (projectAddResult, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return projectAddResult{}, fmt.Errorf("resolve project path: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return projectAddResult{}, fmt.Errorf("project path %q: %w", path, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return projectAddResult{}, fmt.Errorf("project path %q: %w", path, err)
	}
	if !info.IsDir() {
		return projectAddResult{}, fmt.Errorf("project path %q is not a directory", path)
	}

	ensureGmuxd()
	payload, err := json.Marshal(struct {
		Paths []string `json:"paths"`
	}{Paths: []string{resolved}})
	if err != nil {
		return projectAddResult{}, fmt.Errorf("encode project request: %w", err)
	}
	resp, err := gmuxdClient().Post(gmuxdBaseURL()+"/v1/projects/add", "application/json", bytes.NewReader(payload))
	if err != nil {
		return projectAddResult{}, fmt.Errorf("contact gmuxd: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return projectAddResult{}, fmt.Errorf("read gmuxd response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		message := extractMessage(body)
		if message == "" {
			message = resp.Status
		}
		return projectAddResult{}, fmt.Errorf("add project: %s", message)
	}
	var envelope struct {
		OK   bool `json:"ok"`
		Data struct {
			Slug     string `json:"slug"`
			Existing bool   `json:"existing"`
			Match    []struct {
				Path string `json:"path"`
			} `json:"match"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return projectAddResult{}, fmt.Errorf("gmuxd returned %s with an invalid response", resp.Status)
	}
	if !envelope.OK || envelope.Data.Slug == "" {
		return projectAddResult{}, fmt.Errorf("gmuxd response is missing project data")
	}
	canonicalPath := resolved
	for _, rule := range envelope.Data.Match {
		if rule.Path != "" {
			canonicalPath = rule.Path
			break
		}
	}
	return projectAddResult{Slug: envelope.Data.Slug, Path: canonicalPath, Existing: envelope.Data.Existing}, nil
}
