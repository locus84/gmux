package centralstore

// This file is the one-time bridge from gmux 1.x's split JSON/meta state to
// the authoritative v2 SQLite model. Parsing legacy files intentionally lives
// outside centralstore; this package accepts only validated domain values and
// commits the entire import (sessions, catalog, and placements) atomically.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/gmuxapp/gmux/services/gmuxd/internal/centralstore/internal/db"
)

const legacyImportMetadataKey = "legacy_v1_import"

var ErrLegacyImportNotEmpty = errors.New("centralstore: legacy import requires an empty store")

type LegacyPlacement struct {
	ProjectIndex int
	SessionID    SessionID
}

type LegacyImport struct {
	Sessions   []NewSession
	Projects   []ProjectEntrySpec
	Placements []LegacyPlacement
	Peers      []ManualPeerSpec
}

type LegacyImportResult struct {
	Imported   bool
	Sessions   int
	Projects   int
	Placements int
	Peers      int
}

// LegacyImportEligible reports whether this database has never imported or
// received any durable user state. An empty database left behind by an
// interrupted/failed first v2 startup remains eligible.
func (s *Store) LegacyImportEligible(ctx context.Context) (bool, error) {
	if _, err := s.readQ.GetMetadata(ctx, legacyImportMetadataKey); err == nil {
		return false, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	sessions, err := s.readQ.ListSessions(ctx)
	if err != nil {
		return false, err
	}
	projects, err := s.readQ.ListProjectEntries(ctx)
	if err != nil {
		return false, err
	}
	peers, err := s.readQ.ListManualPeers(ctx)
	if err != nil {
		return false, err
	}
	return len(sessions) == 0 && len(projects) == 0 && len(peers) == 0, nil
}

// ImportLegacy atomically installs a normalized v1 snapshot. It is deliberately
// insert-only and refuses any non-empty store, so legacy files can never
// overwrite state authored by v2.
func (s *Store) ImportLegacy(ctx context.Context, input LegacyImport, at UnixMillis) (LegacyImportResult, error) {
	if at < 0 {
		return LegacyImportResult{}, errors.New("centralstore: legacy import timestamp must be non-negative")
	}
	projects, err := normalizeSpecs(input.Projects)
	if err != nil {
		return LegacyImportResult{}, fmt.Errorf("centralstore: legacy projects: %w", err)
	}
	seenSessions := make(map[SessionID]bool, len(input.Sessions))
	for _, session := range input.Sessions {
		if err := validateNewSession(session); err != nil {
			return LegacyImportResult{}, fmt.Errorf("centralstore: legacy session %q: %w", session.ID, err)
		}
		if seenSessions[session.ID] {
			return LegacyImportResult{}, fmt.Errorf("centralstore: duplicate legacy session %q", session.ID)
		}
		seenSessions[session.ID] = true
	}
	seenPeerNames := make(map[string]bool, len(input.Peers))
	seenPeerNodes := make(map[string]bool, len(input.Peers))
	for _, peer := range input.Peers {
		if peer.Name == "" || slugifyPeerName(peer.Name) != peer.Name {
			return LegacyImportResult{}, errors.New("centralstore: invalid legacy peer name")
		}
		if err := validatePeerURL(peer.URL); err != nil {
			return LegacyImportResult{}, err
		}
		if seenPeerNames[peer.Name] || (peer.NodeID != "" && seenPeerNodes[peer.NodeID]) {
			return LegacyImportResult{}, errors.New("centralstore: duplicate legacy peer")
		}
		seenPeerNames[peer.Name] = true
		if peer.NodeID != "" {
			seenPeerNodes[peer.NodeID] = true
		}
	}
	seenPlacements := make(map[SessionID]bool, len(input.Placements))
	for _, placement := range input.Placements {
		if placement.ProjectIndex < 0 || placement.ProjectIndex >= len(projects) || projects[placement.ProjectIndex].Owned == nil {
			return LegacyImportResult{}, errors.New("centralstore: invalid legacy placement project")
		}
		if !seenSessions[placement.SessionID] || seenPlacements[placement.SessionID] {
			return LegacyImportResult{}, errors.New("centralstore: invalid or duplicate legacy placement session")
		}
		seenPlacements[placement.SessionID] = true
	}

	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return LegacyImportResult{}, err
	}
	defer tx.Rollback()
	q := s.queries.WithTx(tx)
	if err := assertLegacyImportEmpty(ctx, q); err != nil {
		return LegacyImportResult{}, err
	}

	for _, v := range input.Sessions {
		cmd, remotes, marshalErr := marshalWhole(v.Command, v.Remotes)
		if marshalErr != nil {
			return LegacyImportResult{}, marshalErr
		}
		candidate := Session{ID: v.ID, Adapter: v.Adapter, SlugBase: v.SlugBase, Slug: v.Slug}
		proposal := v.SlugBase
		if proposal == "" {
			proposal = v.Slug
		}
		if err := allocateSessionSlug(ctx, q, &candidate, "", proposal); err != nil {
			return LegacyImportResult{}, err
		}
		v.SlugBase, v.Slug = candidate.SlugBase, candidate.Slug
		parent := sql.NullString{}
		if v.ParentSessionID != nil {
			parent = nullString(string(*v.ParentSessionID))
		}
		_, err = q.InsertSession(ctx, db.InsertSessionParams{
			ID: string(v.ID), Adapter: v.Adapter, DriveMode: normalizeDriveMode(v.DriveMode),
			ConversationRef: nullString(v.ConversationRef), CommandJson: cmd, Cwd: v.CWD,
			WorkspaceRoot: nullString(v.WorkspaceRoot), RemotesJson: remotes,
			Slug: nullString(v.Slug), SlugBase: nullString(v.SlugBase), ShellTitle: nullString(v.ShellTitle),
			AdapterTitle: nullString(v.AdapterTitle), Subtitle: nullString(v.Subtitle), Active: boolInt(v.Active),
			Interrupted: boolInt(v.Interrupted), Unread: boolInt(v.Unread), UnreadToken: v.UnreadToken,
			HasError: boolInt(v.Error), StatusReported: boolInt(v.StatusReported || v.Active || v.Error || v.Interrupted),
			CreatedAtMs: int64(v.CreatedAt), StartedAtMs: nullMillis(v.StartedAt), ExitedAtMs: nullMillis(v.ExitedAt),
			LastActivityAtMs: nullMillis(v.LastActivityAt), ExitCode: nullInt(v.ExitCode),
			TerminalCols: nullUint(v.TerminalCols), TerminalRows: nullUint(v.TerminalRows),
			ParentSessionID: parent, LaunchedFromSessionID: parent,
		})
		if err != nil {
			return LegacyImportResult{}, fmt.Errorf("centralstore: insert legacy session %q: %w", v.ID, err)
		}
	}

	catalog, _, err := replaceCatalogInTx(ctx, q, projects, at)
	if err != nil {
		return LegacyImportResult{}, err
	}
	for position, placement := range input.Placements {
		_, err = q.InsertLocalPlacement(ctx, db.InsertLocalPlacementParams{
			ProjectEntryID: int64(catalog[placement.ProjectIndex].ID), LocalSessionID: nullString(string(placement.SessionID)),
			SiblingScope: "r", Position: int64(position),
		})
		if err != nil {
			return LegacyImportResult{}, fmt.Errorf("centralstore: insert legacy placement: %w", err)
		}
	}
	if _, err = normalizePlacements(ctx, q, s.beforePlacementFinalize); err != nil {
		return LegacyImportResult{}, err
	}
	for _, peer := range input.Peers {
		if _, err = q.InsertManualPeer(ctx, db.InsertManualPeerParams{
			Name: peer.Name, Url: peer.URL, Token: nullString(peer.Token), NodeID: nullString(peer.NodeID),
			CreatedAtMs: int64(at), UpdatedAtMs: int64(at),
		}); err != nil {
			return LegacyImportResult{}, fmt.Errorf("centralstore: insert legacy peer: %w", err)
		}
	}
	if err = q.PutMetadata(ctx, db.PutMetadataParams{Key: legacyImportMetadataKey, Value: "complete"}); err != nil {
		return LegacyImportResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return LegacyImportResult{}, err
	}
	return LegacyImportResult{Imported: true, Sessions: len(input.Sessions), Projects: len(input.Projects), Placements: len(input.Placements), Peers: len(input.Peers)}, nil
}

func assertLegacyImportEmpty(ctx context.Context, q *db.Queries) error {
	if _, err := q.GetMetadata(ctx, legacyImportMetadataKey); err == nil {
		return ErrLegacyImportNotEmpty
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	sessions, err := q.ListSessions(ctx)
	if err != nil {
		return err
	}
	projects, err := q.ListProjectEntries(ctx)
	if err != nil {
		return err
	}
	peers, err := q.ListManualPeers(ctx)
	if err != nil {
		return err
	}
	if len(sessions) != 0 || len(projects) != 0 || len(peers) != 0 {
		return ErrLegacyImportNotEmpty
	}
	return nil
}
