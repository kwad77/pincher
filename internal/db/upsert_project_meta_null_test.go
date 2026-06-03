// SPDX-License-Identifier: MIT

package db

import (
	"testing"
	"time"
)

// #1086: pre-v18 projects (schema_version_at_index = NULL) couldn't be
// re-stamped on re-index. SQLite NULL propagation through MAX() and
// CASE WHEN meant `MAX(NULL, 26)` returned NULL and
// `excluded.X >= NULL` evaluated to NULL (false in CASE), so
// binary_version and schema_version_at_index stayed NULL forever —
// the drift warning fired permanently even after `index force=true`.
// Fix: COALESCE(schema_version_at_index, 0) in both expressions.

func TestUpsertProjectMeta_LegacyNullSchema_RestampsOnReindex(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	// Seed a legacy pre-v18 row: schema_version_at_index is NULL,
	// binary_version is "" (empty / pre-v18). This matches the on-disk
	// state of any project last indexed before the v14→v15 migration.
	if _, err := s.db.Exec(`
		INSERT INTO projects(id, path, name, indexed_at, file_count, sym_count, edge_count, schema_version_at_index, binary_version)
		VALUES (?,?,?,?,0,0,0,NULL,'')`,
		"legacy", "/tmp/legacy", "legacy", time.Now().Add(-7*24*time.Hour).Unix()); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}

	// Re-index with a current binary at the current schema version.
	if err := s.UpsertProjectMeta(Project{
		ID:            "legacy",
		Path:          "/tmp/legacy",
		Name:          "legacy",
		IndexedAt:     time.Now(),
		BinaryVersion: "v0.59.0-test",
	}); err != nil {
		t.Fatalf("UpsertProjectMeta: %v", err)
	}

	got, err := s.GetProject("legacy")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if got == nil {
		t.Fatal("GetProject returned nil")
	}
	// Pre-fix: both stayed NULL / "". Post-fix: re-stamped to current.
	if got.SchemaVersionAtIndex == nil {
		t.Errorf("schema_version_at_index must be re-stamped from NULL; still nil")
	} else if *got.SchemaVersionAtIndex != CurrentSchemaVersion() {
		t.Errorf("schema_version_at_index must equal current (%d); got %d",
			CurrentSchemaVersion(), *got.SchemaVersionAtIndex)
	}
	if got.BinaryVersion != "v0.59.0-test" {
		t.Errorf("binary_version must be re-stamped from empty; got %q", got.BinaryVersion)
	}
}
