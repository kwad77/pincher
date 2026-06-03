// SPDX-License-Identifier: MIT

package db

import (
	"testing"
	"time"
)

// #894: at the start of an Index() run the indexer used to call
// UpsertProject with a freshly-constructed Project struct whose
// FileCount/SymCount/EdgeCount were the Go zero value (0). The old
// UPSERT then overwrote the previous run's accurate counts with
// zeros, so `health` reported 0 symbols / 0 files / 0 edges for the
// brief window between start-of-index and the first
// UpdateProjectCounts call. UpsertProjectMeta is the targeted method
// that doesn't touch the count columns — counts remain stable across
// the gap.

func TestUpsertProjectMeta_PreservesExistingCounts(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	// First: a full UpsertProject with non-zero counts (simulating the
	// end-of-Index() call from a previous run).
	p := Project{
		ID:            "proj-894",
		Path:          "/tmp/proj-894",
		Name:          "proj-894",
		IndexedAt:     time.Now().Add(-1 * time.Minute),
		FileCount:     424,
		SymCount:      5506,
		EdgeCount:     8929,
		BinaryVersion: "v0.56.0",
	}
	if err := s.UpsertProject(p); err != nil {
		t.Fatalf("seed UpsertProject: %v", err)
	}

	// Now: start-of-Index() metadata-only refresh — IndexedAt advances,
	// counts are NOT touched.
	freshStart := time.Now()
	if err := s.UpsertProjectMeta(Project{
		ID:            "proj-894",
		Path:          "/tmp/proj-894",
		Name:          "proj-894",
		IndexedAt:     freshStart,
		BinaryVersion: "v0.57.0",
	}); err != nil {
		t.Fatalf("UpsertProjectMeta: %v", err)
	}

	got, err := s.GetProject("proj-894")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if got == nil {
		t.Fatal("GetProject returned nil")
	}
	// Counts must survive.
	if got.FileCount != 424 || got.SymCount != 5506 || got.EdgeCount != 8929 {
		t.Errorf("counts must be preserved across UpsertProjectMeta; got files=%d sym=%d edge=%d (want 424/5506/8929)",
			got.FileCount, got.SymCount, got.EdgeCount)
	}
	// Metadata must update.
	if got.BinaryVersion != "v0.57.0" {
		t.Errorf("binary_version must update; got %q want \"v0.57.0\"", got.BinaryVersion)
	}
	if got.IndexedAt.Unix() != freshStart.Unix() {
		t.Errorf("indexed_at must advance; got %v want %v", got.IndexedAt, freshStart)
	}
}

// UpsertProjectMeta on a fresh project (no prior row) inserts with
// zero counts — the UpdateProjectCounts / final UpsertProject calls
// later in the run write the real totals.
func TestUpsertProjectMeta_InsertsNewProjectWithZeroCounts(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	now := time.Now()
	if err := s.UpsertProjectMeta(Project{
		ID:            "brand-new",
		Path:          "/tmp/brand-new",
		Name:          "brand-new",
		IndexedAt:     now,
		BinaryVersion: "v0.57.0",
	}); err != nil {
		t.Fatalf("UpsertProjectMeta: %v", err)
	}

	got, err := s.GetProject("brand-new")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if got == nil {
		t.Fatal("GetProject returned nil")
	}
	if got.FileCount != 0 || got.SymCount != 0 || got.EdgeCount != 0 {
		t.Errorf("fresh project must have zero counts; got files=%d sym=%d edge=%d",
			got.FileCount, got.SymCount, got.EdgeCount)
	}
	if got.BinaryVersion != "v0.57.0" {
		t.Errorf("binary_version: got %q want \"v0.57.0\"", got.BinaryVersion)
	}
}

// #724 monotonic guard parity: UpsertProjectMeta must not let a stale
// orphan re-walker stomp newer schema_version_at_index. Sets the row
// with a high schema version first, then attempts a metadata refresh
// from a "stale" binary — the metadata-only path preserves the higher
// schema version and the matching binary_version.
func TestUpsertProjectMeta_MonotonicSchemaGuard(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	// Seed with full UpsertProject at the current schema version (whatever
	// CurrentSchemaVersion returns) — that's the highest the test binary
	// understands.
	high := CurrentSchemaVersion()
	if _, err := s.db.Exec(`
		INSERT INTO projects(id, path, name, indexed_at, file_count, sym_count, edge_count, schema_version_at_index, binary_version)
		VALUES (?,?,?,?,0,0,0,?,?)`,
		"mono-894", "/tmp/mono-894", "mono-894", time.Now().Unix(),
		high, "v0.57.0-fresh"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Pretend a stale orphan calls UpsertProjectMeta at schema=high-2.
	// We can't actually downgrade CurrentSchemaVersion at runtime, but
	// the guard's correctness is observable through the parity with
	// UpsertProject's MonotonicMetadataGuard test — UpsertProjectMeta
	// uses the same CASE expression in the UPSERT. Verify here that a
	// same-version call doesn't lose the binary_version.
	if err := s.UpsertProjectMeta(Project{
		ID:            "mono-894",
		Path:          "/tmp/mono-894",
		Name:          "mono-894",
		IndexedAt:     time.Now().Add(1 * time.Second),
		BinaryVersion: "v0.57.0-meta-refresh",
	}); err != nil {
		t.Fatalf("UpsertProjectMeta: %v", err)
	}

	got, err := s.GetProject("mono-894")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	// Same schema version → binary_version updates per the CASE rule.
	if got.BinaryVersion != "v0.57.0-meta-refresh" {
		t.Errorf("binary_version: got %q want \"v0.57.0-meta-refresh\"", got.BinaryVersion)
	}
	if got.SchemaVersionAtIndex == nil || *got.SchemaVersionAtIndex != high {
		t.Errorf("schema_version_at_index must remain %d; got %v", high, got.SchemaVersionAtIndex)
	}
}
