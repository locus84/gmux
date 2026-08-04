package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gmuxapp/gmux/packages/paths"
	"github.com/gmuxapp/gmux/packages/workspace"
)

type worktreeRow struct {
	workspace.Worktree
	Current           bool         `json:"current"`
	LiveTerminalCount int          `json:"live_terminal_count"`
	Sessions          []cliSession `json:"sessions"`
}

func cmdWorktree(c *command) int {
	switch c.worktreeSub {
	case "current":
		return cmdWorktreeCurrent(c.json)
	case "ps":
		return cmdWorktreePS(c.worktreeSelector, c.json)
	case "create":
		return cmdWorktreeCreate(c)
	default:
		fmt.Fprintf(os.Stderr, "gmux: unknown worktree command %q\n", c.worktreeSub)
		return 2
	}
}

type worktreeCreateRequest struct {
	Repo, Name, Base, Path, Agent, Prompt string
}

type worktreeCreateResult struct {
	workspace.Worktree
	SessionID string `json:"session_id,omitempty"`
	PID       int    `json:"pid,omitempty"`
}

type worktreeCreateDeps struct {
	validateLauncher func(string) error
	launch           func(path, agent string) (string, int, error)
	sendPrompt       func(id, prompt string) error
	kill             func(id string) error
}

func cmdWorktreeCreate(c *command) int {
	repo := c.worktreeRepo
	if repo == "" {
		var err error
		repo, err = os.Getwd()
		if err != nil {
			return printWorktreeError(err)
		}
	}
	result, err := createWorktree(worktreeCreateRequest{
		Repo: repo, Name: c.worktreeName, Base: c.worktreeBase, Path: c.worktreePath,
		Agent: c.worktreeAgent, Prompt: c.worktreePrompt,
	}, defaultWorktreeCreateDeps())
	if err != nil {
		return printWorktreeError(err)
	}
	if c.json {
		return encodeWorktreeJSON(result)
	}
	fmt.Printf("created %s at %s\n", result.Branch, result.Path)
	if result.SessionID != "" {
		fmt.Printf("session %s\n", shortID(result.SessionID))
	}
	return 0
}

func createWorktree(req worktreeCreateRequest, deps worktreeCreateDeps) (worktreeCreateResult, error) {
	if req.Base == "" {
		req.Base = "HEAD"
	}
	if req.Repo == "" {
		return worktreeCreateResult{}, fmt.Errorf("repository path is required")
	}
	repo, err := gitOutput(req.Repo, "rev-parse", "--show-toplevel")
	if err != nil {
		return worktreeCreateResult{}, fmt.Errorf("resolve repository: %w", err)
	}
	// Keep cheap branch validation ahead of launcher probing so malformed input
	// never contacts gmuxd. CreateWorktreeContext performs the authoritative
	// validation again immediately before the Git operation.
	if _, err := gitOutput(repo, "check-ref-format", "--branch", req.Name); err != nil {
		return worktreeCreateResult{}, fmt.Errorf("invalid worktree branch %q: %w", req.Name, err)
	}
	if req.Prompt != "" && req.Agent == "" {
		return worktreeCreateResult{}, fmt.Errorf("prompt requires an agent")
	}
	if len(req.Prompt) >= maxSendBytes {
		return worktreeCreateResult{}, fmt.Errorf("prompt exceeds %d bytes including Enter", maxSendBytes)
	}
	if req.Agent != "" {
		if deps.validateLauncher == nil {
			return worktreeCreateResult{}, fmt.Errorf("launcher validation unavailable")
		}
		if err := deps.validateLauncher(req.Agent); err != nil {
			return worktreeCreateResult{}, err
		}
	}
	created, err := workspace.CreateWorktreeContext(context.Background(), workspace.CreateWorktreeOptions{
		Repository: repo, Branch: req.Name, Base: req.Base,
		Destination: req.Path, ManagedRoot: paths.WorktreesDir(),
	})
	if err != nil {
		return worktreeCreateResult{}, err
	}
	path := created.Path
	result := worktreeCreateResult{Worktree: created}
	if req.Agent == "" {
		return result, nil
	}
	id, pid, err := deps.launch(path, req.Agent)
	if err != nil {
		// The request may have reached gmuxd even when its response was lost.
		// Never remove a checkout beneath a possibly-live process.
		return result, fmt.Errorf("launch agent: %v; worktree preserved at %s", err, path)
	}
	result.SessionID, result.PID = id, pid
	if req.Prompt != "" {
		if err := deps.sendPrompt(id, req.Prompt); err != nil {
			var killErr error
			if deps.kill != nil {
				killErr = deps.kill(id)
			}
			if killErr != nil {
				return result, fmt.Errorf("send prompt: %v; could not stop session %s: %v; worktree preserved at %s", err, id, killErr, path)
			}
			return result, fmt.Errorf("send prompt: %v; session %s stopped; worktree preserved at %s", err, id, path)
		}
	}
	return result, nil
}

func worktreePathName(branch string) string {
	replacer := strings.NewReplacer("/", "-", "\\", "-", string(filepath.Separator), "-")
	return replacer.Replace(branch)
}

// mirroredWorktreeRepoPath converts a canonical absolute repository path into
// a safe relative namespace beneath paths.WorktreesDir. The full path keeps
// same-named clones distinct without opaque hashes.
func mirroredWorktreeRepoPath(repo string) (string, error) {
	repo = cleanWorktreePath(repo)
	volume := filepath.VolumeName(repo)
	rel := strings.TrimPrefix(repo, volume)
	rel = strings.TrimLeft(rel, "/\\")

	if volume != "" {
		rel = filepath.Join(mirroredWorktreeVolume(volume), rel)
	}
	rel = filepath.Clean(rel)
	if rel == "." || rel == "" {
		if filepath.IsAbs(repo) {
			return "_root", nil
		}
		return "", fmt.Errorf("repository path %q cannot be mirrored safely", repo)
	}
	if filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("repository path %q cannot be mirrored safely", repo)
	}
	return rel, nil
}

func mirroredWorktreeVolume(volume string) string {
	// Windows extended/device prefixes are path syntax, not valid namespace
	// components beneath the managed worktree root.
	volume = strings.TrimPrefix(volume, `\\?\`)
	volume = strings.TrimPrefix(volume, `\\.\`)
	if len(volume) >= 4 && strings.EqualFold(volume[:4], `UNC\`) {
		volume = volume[4:]
	}
	volume = strings.Trim(volume, "/\\")
	volume = strings.ReplaceAll(volume, ":", "")
	volume = strings.ReplaceAll(volume, "\\", string(filepath.Separator))
	return strings.ReplaceAll(volume, "/", string(filepath.Separator))
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return strings.TrimRight(string(out), "\r\n"), nil
}

func gitOK(dir string, args ...string) bool {
	return exec.Command("git", append([]string{"-C", dir}, args...)...).Run() == nil
}

func defaultWorktreeCreateDeps() worktreeCreateDeps {
	return worktreeCreateDeps{validateLauncher: validateWorktreeLauncher, launch: launchWorktreeAgent, sendPrompt: sendWorktreePrompt, kill: killWorktreeSession}
}

func validateWorktreeLauncher(agent string) error {
	ensureGmuxd()
	resp, err := gmuxdClient().Get(gmuxdBaseURL() + "/v1/health")
	if err != nil {
		return fmt.Errorf("contact gmuxd: %w", err)
	}
	defer resp.Body.Close()
	var envelope struct {
		Data struct {
			Launchers []struct {
				ID        string `json:"id"`
				Available bool   `json:"available"`
			} `json:"launchers"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("decode gmuxd health: %w", err)
	}
	for _, launcher := range envelope.Data.Launchers {
		if launcher.ID == agent && launcher.Available {
			return nil
		}
	}
	return fmt.Errorf("agent launcher %q is unavailable", agent)
}

func launchWorktreeAgent(path, agent string) (string, int, error) {
	ensureGmuxd()
	body, _ := json.Marshal(map[string]any{"cwd": path, "launcher_id": agent})
	resp, err := gmuxdClient().Post(gmuxdBaseURL()+"/v1/launch", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(resp.Body)
		return "", 0, fmt.Errorf("gmuxd returned %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	var envelope struct {
		Data struct {
			SessionID string `json:"session_id"`
			PID       int    `json:"pid"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return "", 0, err
	}
	if envelope.Data.SessionID == "" {
		return "", 0, fmt.Errorf("gmuxd returned no session id")
	}
	return envelope.Data.SessionID, envelope.Data.PID, nil
}

func sendWorktreePrompt(id, prompt string) error {
	req, err := http.NewRequest(http.MethodPost, gmuxdBaseURL()+"/v1/sessions/"+id+"/input", strings.NewReader(prompt+"\r"))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	client := gmuxdClient()
	client.Timeout = 0
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("gmuxd returned %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	return nil
}

func killWorktreeSession(id string) error {
	resp, err := gmuxdClient().Post(gmuxdBaseURL()+"/v1/sessions/"+id+"/kill", "application/json", strings.NewReader("{}"))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("gmuxd returned %s", resp.Status)
	}
	return nil
}

func cmdWorktreeCurrent(asJSON bool) int {
	cwd, err := os.Getwd()
	if err != nil {
		return printWorktreeError(err)
	}
	items, err := workspace.ListWorktrees(cwd)
	if err != nil {
		return printWorktreeError(err)
	}
	wt, err := workspace.CurrentWorktree(items, cwd)
	if err != nil {
		return printWorktreeError(err)
	}
	if asJSON {
		return encodeWorktreeJSON(wt)
	}
	fmt.Printf("%s  %s\n", worktreeBranch(wt), wt.Path)
	return 0
}

func cmdWorktreePS(selector string, asJSON bool) int {
	cwd, err := os.Getwd()
	if err != nil {
		return printWorktreeError(err)
	}
	items, err := workspace.ListWorktrees(cwd)
	if err != nil {
		return printWorktreeError(err)
	}
	sessions, err := fetchSessions()
	if err != nil {
		return printWorktreeError(err)
	}
	rows, err := buildWorktreeRows(items, sessions, cwd, selector)
	if err != nil {
		return printWorktreeError(err)
	}
	if asJSON {
		return encodeWorktreeJSON(rows)
	}
	if len(rows) == 0 {
		fmt.Println("no worktrees")
		return 0
	}
	for _, row := range rows {
		marker := " "
		if row.Current {
			marker = "*"
		}
		fmt.Printf("%s %-24s live:%d  %s\n", marker, worktreeBranch(row.Worktree), row.LiveTerminalCount, row.Path)
		for _, sess := range row.Sessions {
			state := "idle"
			if sess.Status != nil && sess.Status.Working {
				state = "working"
			}
			fmt.Printf("    %s  %-8s  %s\n", displayID(sess), state, firstNonEmpty(sess.Title, sess.Slug))
		}
	}
	return 0
}

func buildWorktreeRows(items []workspace.Worktree, sessions []cliSession, cwd, selector string) ([]worktreeRow, error) {
	selected := items
	if selector != "" {
		wt, err := workspace.ResolveWorktree(items, selector, cwd)
		if err != nil {
			return nil, err
		}
		selected = []workspace.Worktree{wt}
	}
	rows := make([]worktreeRow, len(selected))
	for i, wt := range selected {
		rows[i] = worktreeRow{Worktree: wt, Sessions: []cliSession{}}
		if cur, err := workspace.CurrentWorktree(items, cwd); err == nil && cur.Path == wt.Path {
			rows[i].Current = true
		}
	}
	for _, sess := range sessions {
		if !sess.Alive || sess.Peer != "" || sess.Cwd == "" {
			continue
		}
		best := -1
		for i := range rows {
			if containsPath(rows[i].Path, sess.Cwd) && (best < 0 || len(rows[i].Path) > len(rows[best].Path)) {
				best = i
			}
		}
		if best >= 0 {
			rows[best].Sessions = append(rows[best].Sessions, sess)
		}
	}
	for i := range rows {
		sort.SliceStable(rows[i].Sessions, func(a, b int) bool { return rows[i].Sessions[a].StartedAt > rows[i].Sessions[b].StartedAt })
		rows[i].LiveTerminalCount = len(rows[i].Sessions)
	}
	return rows, nil
}

func containsPath(root, child string) bool {
	root = cleanWorktreePath(root)
	child = cleanWorktreePath(child)
	rel, err := filepath.Rel(root, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func cleanWorktreePath(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	path = filepath.Clean(path)
	probe := path
	var suffix []string
	for {
		if real, err := filepath.EvalSymlinks(probe); err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				real = filepath.Join(real, suffix[i])
			}
			return filepath.Clean(real)
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return path
		}
		suffix = append(suffix, filepath.Base(probe))
		probe = parent
	}
}

func worktreeBranch(wt workspace.Worktree) string {
	if wt.Branch != "" {
		return wt.Branch
	}
	if len(wt.Head) > 12 {
		return wt.Head[:12]
	}
	return wt.Head
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return "-"
}

func encodeWorktreeJSON(value any) int {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(value); err != nil {
		return printWorktreeError(err)
	}
	return 0
}

func printWorktreeError(err error) int {
	fmt.Fprintln(os.Stderr, "gmux:", err)
	return 1
}
