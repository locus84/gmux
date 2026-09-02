// Package legacyimport reads gmux 1.x's retired JSON/sessionmeta state and
// translates it into centralstore domain values. It is a one-time bootstrap
// adapter, never an authority and never a writer of legacy files.
package legacyimport

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/gmuxapp/gmux/services/gmuxd/internal/centralstore"
	"github.com/gmuxapp/gmux/services/gmuxd/internal/conversations"
	"github.com/gmuxapp/gmux/services/gmuxd/internal/projectmatch"
	"github.com/gmuxapp/gmux/services/gmuxd/internal/sessionmeta"
	"github.com/gmuxapp/gmux/services/gmuxd/internal/store"
)

const (
	legacyProjectsFile = "projects.json"
	legacyPeersFile    = "peers.json"
)

type projectState struct {
	Version int           `json:"version"`
	Items   []projectItem `json:"items"`
}
type projectItem struct {
	Slug     string        `json:"slug"`
	Peer     string        `json:"peer,omitempty"`
	NodeID   string        `json:"node_id,omitempty"`
	Match    []projectRule `json:"match,omitempty"`
	Sessions []string      `json:"sessions,omitempty"`
}
type projectRule struct {
	Path, Remote string
	Exact        bool
}
type peerRecord struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Token  string `json:"token,omitempty"`
	NodeID string `json:"node_id,omitempty"`
}

// Report makes stale/unresolvable v1 slots visible without failing the safe
// subset. Report counts never include peer credentials and are safe to log.
type Report struct {
	MetaSessions         int
	ConversationSessions int
	UnresolvedSlots      int
	// UnresolvedKeys is diagnostic detail for tests/operator tooling. Startup
	// logs only the count because titles and conversation IDs may be private.
	UnresolvedKeys []string
}

func Exists(stateDir string) (bool, error) {
	_, err := os.Stat(filepath.Join(stateDir, legacyProjectsFile))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

// Load reads a stable point-in-time snapshot. It imports every valid local
// meta record, then fills project slots that only have an adapter-owned
// conversation transcript. Stale slots are counted and omitted.
func Load(stateDir string, infos []conversations.Info) (centralstore.LegacyImport, Report, error) {
	data, err := os.ReadFile(filepath.Join(stateDir, legacyProjectsFile))
	if err != nil {
		return centralstore.LegacyImport{}, Report{}, fmt.Errorf("legacy import: read projects: %w", err)
	}
	var state projectState
	if err := json.Unmarshal(data, &state); err != nil {
		return centralstore.LegacyImport{}, Report{}, fmt.Errorf("legacy import: parse projects: %w", err)
	}
	if state.Version < 1 || state.Version > 4 {
		return centralstore.LegacyImport{}, Report{}, fmt.Errorf("legacy import: unsupported projects version %d", state.Version)
	}

	out := centralstore.LegacyImport{}
	peerData, peerErr := os.ReadFile(filepath.Join(stateDir, legacyPeersFile))
	if peerErr == nil && len(peerData) > 0 {
		var peers []peerRecord
		if err := json.Unmarshal(peerData, &peers); err != nil {
			return centralstore.LegacyImport{}, Report{}, fmt.Errorf("legacy import: parse peers: %w", err)
		}
		for _, peer := range peers {
			out.Peers = append(out.Peers, centralstore.ManualPeerSpec{Name: peer.Name, URL: peer.URL, Token: peer.Token, NodeID: peer.NodeID})
		}
	} else if peerErr != nil && !errors.Is(peerErr, os.ErrNotExist) {
		return centralstore.LegacyImport{}, Report{}, fmt.Errorf("legacy import: read peers: %w", peerErr)
	}
	for _, item := range state.Items {
		if item.Slug == "" {
			return centralstore.LegacyImport{}, Report{}, errors.New("legacy import: project slug is empty")
		}
		if item.Peer != "" {
			out.Projects = append(out.Projects, centralstore.ProjectEntrySpec{Reference: &centralstore.ProjectReference{PeerKey: centralstore.PeerKey(item.Peer), Slug: item.Slug, NodeID: item.NodeID}})
			continue
		}
		rules := make([]centralstore.MatchRule, 0, len(item.Match))
		for _, rule := range item.Match {
			rules = append(rules, centralstore.MatchRule{Path: rule.Path, Remote: rule.Remote, Exact: rule.Exact})
		}
		out.Projects = append(out.Projects, centralstore.ProjectEntrySpec{Owned: &centralstore.OwnedProjectSpec{Slug: item.Slug, Rules: rules}})
	}

	meta, err := loadMetaSessions(filepath.Join(stateDir, "sessions"))
	if err != nil {
		return centralstore.LegacyImport{}, Report{}, err
	}
	report := Report{MetaSessions: len(meta)}
	byID := make(map[string]int, len(meta))
	bySlug := make(map[string]int, len(meta))
	ambiguousSlug := make(map[string]bool)
	for i, session := range meta {
		byID[string(session.ID)] = i
		if session.Slug != "" {
			if _, found := bySlug[session.Slug]; found {
				ambiguousSlug[session.Slug] = true
			} else {
				bySlug[session.Slug] = i
			}
		}
	}
	out.Sessions = append(out.Sessions, meta...)

	// Sort first: Index.All intentionally has map order, but migration output
	// and ambiguity handling must be deterministic.
	sort.Slice(infos, func(i, j int) bool {
		if infos[i].Adapter != infos[j].Adapter {
			return infos[i].Adapter < infos[j].Adapter
		}
		if infos[i].Key != infos[j].Key {
			return infos[i].Key < infos[j].Key
		}
		return infos[i].ConversationID < infos[j].ConversationID
	})
	conversationByKey := make(map[string][]int)
	for i, info := range infos {
		keys := []string{info.Key, info.Slug, info.ConversationID}
		seenKey := make(map[string]bool, len(keys))
		for _, key := range keys {
			if key == "" || seenKey[key] {
				continue
			}
			seenKey[key] = true
			conversationByKey[key] = append(conversationByKey[key], i)
		}
	}

	placed := make(map[centralstore.SessionID]bool)
	for projectIndex, item := range state.Items {
		if item.Peer != "" {
			continue
		}
		for _, key := range item.Sessions {
			var id centralstore.SessionID
			if i, ok := byID[key]; ok {
				id = out.Sessions[i].ID
			} else if i, ok := bySlug[key]; ok && !ambiguousSlug[key] {
				id = out.Sessions[i].ID
			} else if i, ok := resolveConversation(conversationByKey[key], infos, state.Items, projectIndex); ok {
				info := infos[i]
				// A meta row for the same adapter+slug owns the durable identity.
				if mi, exists := findMetaConversation(out.Sessions, info); exists {
					id = out.Sessions[mi].ID
				} else {
					session := conversationSession(info)
					if session.ID == "" {
						report.UnresolvedSlots++
						report.UnresolvedKeys = append(report.UnresolvedKeys, key)
						continue
					}
					if _, collision := byID[string(session.ID)]; collision {
						report.UnresolvedSlots++
						report.UnresolvedKeys = append(report.UnresolvedKeys, key)
						continue
					}
					byID[string(session.ID)] = len(out.Sessions)
					out.Sessions = append(out.Sessions, session)
					report.ConversationSessions++
					id = session.ID
				}
			} else {
				report.UnresolvedSlots++
				report.UnresolvedKeys = append(report.UnresolvedKeys, key)
				continue
			}
			if placed[id] {
				continue
			}
			placed[id] = true
			out.Placements = append(out.Placements, centralstore.LegacyPlacement{ProjectIndex: projectIndex, SessionID: id})
		}
	}
	return out, report, nil
}

func resolveConversation(candidates []int, infos []conversations.Info, items []projectItem, projectIndex int) (int, bool) {
	if len(candidates) == 1 {
		return candidates[0], true
	}
	if len(candidates) == 0 {
		return 0, false
	}
	entries := make([]projectmatch.Entry, len(items))
	for i, item := range items {
		entries[i].Reference = item.Peer != ""
		for _, rule := range item.Match {
			entries[i].Rules = append(entries[i].Rules, projectmatch.Rule{Path: rule.Path, Remote: rule.Remote, Exact: rule.Exact})
		}
	}
	resolved := -1
	for _, candidate := range candidates {
		winner, matched := projectmatch.Match(entries, projectmatch.Inputs{CWD: infos[candidate].Cwd})
		if !matched || winner != projectIndex {
			continue
		}
		if resolved != -1 {
			return 0, false
		}
		resolved = candidate
	}
	return resolved, resolved != -1
}

func loadMetaSessions(dir string) ([]centralstore.NewSession, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("legacy import: read sessions: %w", err)
	}
	legacy := sessionmeta.New(dir)
	out := make([]centralstore.NewSession, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == ".invalid" {
			continue
		}
		session, readErr := legacy.Read(entry.Name())
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		if readErr != nil {
			return nil, readErr
		}
		// v1 used last_activity_at before the field was renamed to
		// last_output_at. sessionmeta's compatibility decoder handles the
		// other renamed fields; preserve this timestamp here as well.
		if session.LastOutputAt == "" {
			data, rawErr := os.ReadFile(filepath.Join(dir, entry.Name(), "meta.json"))
			if rawErr != nil {
				return nil, rawErr
			}
			var legacyTimes struct {
				LastActivityAt string `json:"last_activity_at"`
			}
			if jsonErr := json.Unmarshal(data, &legacyTimes); jsonErr != nil {
				return nil, jsonErr
			}
			session.LastOutputAt = legacyTimes.LastActivityAt
		}
		converted, convertErr := metaSession(session)
		if convertErr != nil {
			return nil, convertErr
		}
		out = append(out, converted)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func metaSession(v store.Session) (centralstore.NewSession, error) {
	created, err := parseRequiredTime(v.CreatedAt)
	if err != nil {
		return centralstore.NewSession{}, fmt.Errorf("legacy import: session %q created_at: %w", v.ID, err)
	}
	started, err := parseOptionalTime(v.StartedAt)
	if err != nil {
		return centralstore.NewSession{}, err
	}
	exited, err := parseOptionalTime(v.ExitedAt)
	if err != nil {
		return centralstore.NewSession{}, err
	}
	activity, err := parseOptionalTime(v.LastOutputAt)
	if err != nil {
		return centralstore.NewSession{}, err
	}
	if v.ExitCode != nil && exited == nil {
		v.ExitCode = nil
	}
	var cols, rows *uint16
	if v.TerminalCols > 0 && v.TerminalRows > 0 {
		c, r := v.TerminalCols, v.TerminalRows
		cols, rows = &c, &r
	}
	var parent *centralstore.SessionID
	if v.ParentSessionID != "" && v.ParentSessionID != v.ID {
		p := centralstore.SessionID(v.ParentSessionID)
		parent = &p
	}
	status := v.Status
	return centralstore.NewSession{
		ID: centralstore.SessionID(v.ID), Adapter: v.Adapter, ConversationRef: v.ConversationRef,
		Command: v.Command, CWD: v.Cwd, WorkspaceRoot: v.WorkspaceRoot, Remotes: v.Remotes,
		Slug: v.Slug, SlugBase: v.Slug, ShellTitle: v.ShellTitle, AdapterTitle: v.AdapterTitle, Subtitle: v.Subtitle,
		Active: status != nil && status.Active, Error: status != nil && status.Error,
		Interrupted: status != nil && status.Interrupted, StatusReported: status != nil,
		Unread: v.Unread, UnreadToken: v.UnreadToken, CreatedAt: created, StartedAt: started,
		ExitedAt: exited, LastActivityAt: activity, ExitCode: v.ExitCode,
		TerminalCols: cols, TerminalRows: rows, ParentSessionID: parent,
	}, nil
}

func conversationSession(info conversations.Info) centralstore.NewSession {
	id := info.ConversationID
	if id == "" {
		id = info.Key
	}
	created := info.Created.UnixMilli()
	if created < 0 {
		created = 0
	}
	var activity *centralstore.UnixMillis
	if !info.LastActivity.IsZero() {
		x := centralstore.UnixMillis(info.LastActivity.UnixMilli())
		activity = &x
	}
	return centralstore.NewSession{ID: centralstore.SessionID(id), Adapter: info.Adapter, ConversationRef: info.Ref,
		Command: append([]string(nil), info.ResumeCommand...), CWD: info.Cwd, Slug: info.Slug, SlugBase: info.Slug,
		AdapterTitle: info.Title, CreatedAt: centralstore.UnixMillis(created), LastActivityAt: activity}
}

func findMetaConversation(sessions []centralstore.NewSession, info conversations.Info) (int, bool) {
	for i, session := range sessions {
		if session.Adapter == info.Adapter && ((session.Slug != "" && session.Slug == info.Slug) || (session.ConversationRef != "" && session.ConversationRef == info.Ref)) {
			return i, true
		}
	}
	return 0, false
}

func parseRequiredTime(value string) (centralstore.UnixMillis, error) {
	if value == "" {
		return 0, nil
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return 0, err
	}
	return centralstore.UnixMillis(t.UnixMilli()), nil
}
func parseOptionalTime(value string) (*centralstore.UnixMillis, error) {
	if value == "" {
		return nil, nil
	}
	x, err := parseRequiredTime(value)
	if err != nil {
		return nil, err
	}
	return &x, nil
}
