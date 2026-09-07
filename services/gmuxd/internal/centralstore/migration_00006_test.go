package centralstore

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestMigration00006DefaultsExistingProjectsToNotFavorite(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	files := fstest.MapFS{}
	for _, name := range []string{
		"00001_initial_schema.sql", "00002_drive_mode.sql",
		"00003_session_parent_provenance.sql", "00004_unread_token.sql",
		"00005_single_axis_promotion.sql",
	} {
		data, err := fs.ReadFile(migrationFiles, "migrations/"+name)
		if err != nil {
			t.Fatal(err)
		}
		files["migrations/"+name] = &fstest.MapFile{Data: data}
	}
	v5, err := openWithMigrationFS(ctx, dir, files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = v5.database.ExecContext(ctx, `INSERT INTO project_entries
		(id, sidebar_order, entry_kind, slug, created_at_ms, updated_at_ms)
		VALUES (1, 0, 'owned', 'existing', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if err = v5.Close(); err != nil {
		t.Fatal(err)
	}

	head, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = head.Close() })
	var favorite int
	if err = head.database.QueryRowContext(ctx, `SELECT favorite FROM project_entries WHERE id = 1`).Scan(&favorite); err != nil {
		t.Fatal(err)
	}
	if favorite != 0 {
		t.Fatalf("migrated favorite = %d, want 0", favorite)
	}
}
