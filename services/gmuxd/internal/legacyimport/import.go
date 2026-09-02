// Package legacyimport reads gmux 1.x's retired JSON/sessionmeta state and
// translates it into centralstore domain values. It is a one-time bootstrap
// adapter, never an authority and never a writer of legacy files.
package legacyimport

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	pathpkg "github.com/gmuxapp/gmux/packages/paths"
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
	// Original/unversioned and v1 shape, migrated in memory only.
	Remote string   `json:"remote,omitempty"`
	Paths  []string `json:"paths,omitempty"`
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
var ErrUnresolvedSlots = errors.New("legacy import: unresolved project slots")

type Report struct {
	MetaSessions         int
	ConversationSessions int
	UnresolvedSlots      int
	// UnresolvedKeys is diagnostic detail for tests/operator tooling. Startup
	// logs only the count because titles and conversation IDs may be private.
	UnresolvedKeys []string
}

func Exists(stateDir string) (bool, error) {
	for _, name := range []string{legacyProjectsFile, legacyPeersFile} {
		if _, err := os.Stat(filepath.Join(stateDir, name)); err == nil {
			return true, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
	}
	entries, err := os.ReadDir(filepath.Join(stateDir, "sessions"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(stateDir, "sessions", entry.Name(), "meta.json")); err == nil {
			return true, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
	}
	return false, nil
}

// Load reads a stable point-in-time snapshot. It imports every valid local
// meta record, then fills project slots that only have an adapter-owned
// conversation transcript. Stale slots are counted and omitted.
func Load(stateDir string, infos []conversations.Info) (centralstore.LegacyImport, Report, error) {
	var state projectState
	data, err := os.ReadFile(filepath.Join(stateDir, legacyProjectsFile))
	if err == nil {
		if err := json.Unmarshal(data, &state); err != nil {
			return centralstore.LegacyImport{}, Report{}, fmt.Errorf("legacy import: parse projects: %w", err)
		}
		if state.Version < 0 || state.Version > 4 {
			return centralstore.LegacyImport{}, Report{}, fmt.Errorf("legacy import: unsupported projects version %d", state.Version)
		}
		if state.Version < 2 {
			for i := range state.Items {
				if len(state.Items[i].Match) > 0 {
					continue
				}
				if state.Items[i].Remote != "" {
					state.Items[i].Match = append(state.Items[i].Match, projectRule{Remote: state.Items[i].Remote})
				}
				for _, path := range state.Items[i].Paths {
					if path != "" {
						state.Items[i].Match = append(state.Items[i].Match, projectRule{Path: pathpkg.CanonicalizePath(path)})
					}
				}
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return centralstore.LegacyImport{}, Report{}, fmt.Errorf("legacy import: read projects: %w", err)
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
	bySlug := make(map[string][]int)
	usedIDs := make(map[centralstore.SessionID]bool, len(meta))
	for i, session := range meta {
		byID[string(session.ID)] = i
		usedIDs[session.ID] = true
		if session.Slug != "" {
			bySlug[session.Slug] = append(bySlug[session.Slug], i)
		}
	}
	out.Sessions = append(out.Sessions, meta...)

	// Index.All intentionally has map order; stable sorting makes generated
	// fallback IDs and placement expansion deterministic across retries.
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

	conversationIDs := make(map[int]centralstore.SessionID)
	placed := make(map[centralstore.SessionID]bool)
	for projectIndex, item := range state.Items {
		if item.Peer != "" {
			continue
		}
		for _, key := range item.Sessions {
			var ids []centralstore.SessionID
			if i, ok := byID[key]; ok {
				// An exact durable ID is unambiguous; do not reinterpret it as a
				// coincidentally equal title.
				ids = append(ids, out.Sessions[i].ID)
			} else {
				// v3 keys were adapter-less. Preserve every matching metadata
				// session exactly as the retired v3→v4 migration did, then also
				// inspect transcript-only candidates: a metadata match in one
				// adapter must not hide another adapter's conversation.
				for _, i := range bySlug[key] {
					ids = append(ids, out.Sessions[i].ID)
				}
				for _, i := range resolveConversations(conversationByKey[key], infos, state.Items, projectIndex) {
					info := infos[i]
					if mi, exists := findMetaConversation(meta, info); exists {
						ids = append(ids, meta[mi].ID)
						continue
					}
					id, exists := conversationIDs[i]
					if !exists {
						id = allocateConversationSessionID(info, usedIDs)
						conversationIDs[i] = id
						usedIDs[id] = true
						out.Sessions = append(out.Sessions, conversationSession(info, id))
						report.ConversationSessions++
					}
					ids = append(ids, id)
				}
			}
			if len(ids) == 0 {
				report.UnresolvedSlots++
				report.UnresolvedKeys = append(report.UnresolvedKeys, key)
				continue
			}
			seenSlot := make(map[centralstore.SessionID]bool, len(ids))
			for _, id := range ids {
				if seenSlot[id] || placed[id] {
					continue
				}
				seenSlot[id] = true
				placed[id] = true
				out.Placements = append(out.Placements, centralstore.LegacyPlacement{ProjectIndex: projectIndex, SessionID: id})
			}
		}
	}
	if report.UnresolvedSlots > 0 {
		return out, report, fmt.Errorf("%w: %d project slots", ErrUnresolvedSlots, report.UnresolvedSlots)
	}
	return out, report, nil
}

func resolveConversations(candidates []int, infos []conversations.Info, items []projectItem, projectIndex int) []int {
	if len(candidates) <= 1 {
		return append([]int(nil), candidates...)
	}
	entries := make([]projectmatch.Entry, len(items))
	for i, item := range items {
		entries[i].Reference = item.Peer != ""
		for _, rule := range item.Match {
			entries[i].Rules = append(entries[i].Rules, projectmatch.Rule{Path: rule.Path, Remote: rule.Remote, Exact: rule.Exact})
		}
	}
	var matchedHere, unknown []int
	for _, candidate := range candidates {
		winner, matched := projectmatch.Match(entries, projectmatch.Inputs{CWD: infos[candidate].Cwd})
		switch {
		case matched && winner == projectIndex:
			matchedHere = append(matchedHere, candidate)
		case !matched:
			unknown = append(unknown, candidate)
		}
	}
	if len(matchedHere) > 0 {
		// At least one candidate has positive path evidence for this project;
		// unknown candidates are more likely unrelated same-title transcripts.
		return matchedHere
	}
	// No candidate has positive path evidence. Preserve unknowns because the
	// project may be remote-only or its workspace may have moved.
	return unknown
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
		if !pathpkg.IsValidSessionID(entry.Name()) {
			return nil, fmt.Errorf("legacy import: invalid session directory %q", entry.Name())
		}
		session, readErr := legacy.Read(entry.Name())
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		if readErr != nil {
			return nil, readErr
		}
		if session.ID != entry.Name() || !pathpkg.IsValidSessionID(session.ID) {
			return nil, fmt.Errorf("legacy import: session metadata identity mismatch in %q", entry.Name())
		}
		data, rawErr := os.ReadFile(filepath.Join(dir, entry.Name(), "meta.json"))
		if rawErr != nil {
			return nil, rawErr
		}
		var compatibility struct {
			LastActivityAt string `json:"last_activity_at"`
			Status         *struct {
				Working *bool `json:"working"`
				Active  *bool `json:"active"`
			} `json:"status"`
		}
		if jsonErr := json.Unmarshal(data, &compatibility); jsonErr != nil {
			return nil, jsonErr
		}
		// v1 used last_activity_at and status.working before the fields were
		// renamed. sessionmeta handles the other renamed fields; preserve these
		// two explicitly. Do not let working override a current active field if
		// a hand-edited/transitional record happens to contain both.
		if session.LastOutputAt == "" {
			session.LastOutputAt = compatibility.LastActivityAt
		}
		if compatibility.Status != nil && compatibility.Status.Working != nil && compatibility.Status.Active == nil {
			if session.Status == nil {
				session.Status = &store.Status{}
			}
			session.Status.Active = *compatibility.Status.Working
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

func allocateConversationSessionID(info conversations.Info, used map[centralstore.SessionID]bool) centralstore.SessionID {
	identity := info.Adapter + "\x00" + info.ConversationID + "\x00" + info.Ref + "\x00" + info.Key
	for nonce := 0; ; nonce++ {
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d", identity, nonce)))
		id := centralstore.SessionID(hex.EncodeToString(sum[:4]))
		if !used[id] {
			return id
		}
	}
}

func conversationSession(info conversations.Info, id centralstore.SessionID) centralstore.NewSession {
	created := info.Created.UnixMilli()
	if created < 0 {
		created = 0
	}
	var activity *centralstore.UnixMillis
	if !info.LastActivity.IsZero() {
		x := centralstore.UnixMillis(info.LastActivity.UnixMilli())
		activity = &x
	}
	return centralstore.NewSession{ID: id, Adapter: info.Adapter, ConversationRef: info.Ref,
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
