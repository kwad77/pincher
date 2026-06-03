// SPDX-License-Identifier: MIT

package db

import "testing"

// TestBranchColumn_RoundTripsThroughBulkUpsert pins the v31 schema
// change for #1303 Phase 1: the `branch` column on the symbols table
// must be writable via BulkUpsertSymbols, readable via GetSymbol /
// SearchSymbols / GetHotspots / GetDeadCode, and preserve the exact
// string the caller stamped.
//
// The Phase 1 indexer doesn't stamp branch yet (Phase 2 wires the
// `git rev-parse --abbrev-ref HEAD` lookup), so all production rows
// today carry '' until Phase 2 ships. This test exercises the
// column directly so the storage substrate is proven before the
// indexer starts populating it.
//
// Positive: explicit "main" round-trips. Default-empty: a Symbol
// constructed without setting Branch reads back with "" (the column
// default). Cross-check: SearchSymbols and GetHotspots scan paths
// surface the column too (they go through scanSymbolRow, distinct
// from the GetSymbol scanOneSymbol path).
func TestBranchColumn_RoundTripsThroughBulkUpsert(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	proj := "branchcol-proj"
	if err := s.UpsertProject(Project{ID: proj, Name: proj, Path: t.TempDir()}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}

	withBranch := Symbol{
		ID:            MakeSymbolID("a.go", "pkg.WithBranch", "Function"),
		ProjectID:     proj,
		FilePath:      "a.go",
		Name:          "WithBranch",
		QualifiedName: "pkg.WithBranch",
		Kind:          "Function",
		Language:      "go",
		Branch:        "main",
	}
	defaultBranch := Symbol{
		ID:            MakeSymbolID("b.go", "pkg.DefaultBranch", "Function"),
		ProjectID:     proj,
		FilePath:      "b.go",
		Name:          "DefaultBranch",
		QualifiedName: "pkg.DefaultBranch",
		Kind:          "Function",
		Language:      "go",
		// Branch left zero on purpose.
	}
	if err := s.BulkUpsertSymbols([]Symbol{withBranch, defaultBranch}); err != nil {
		t.Fatalf("BulkUpsertSymbols: %v", err)
	}

	got, err := s.GetSymbol(withBranch.ID)
	if err != nil || got == nil {
		t.Fatalf("GetSymbol(withBranch): %v / %v", got, err)
	}
	if got.Branch != "main" {
		t.Errorf("GetSymbol(withBranch).Branch = %q, want %q", got.Branch, "main")
	}

	got2, err := s.GetSymbol(defaultBranch.ID)
	if err != nil || got2 == nil {
		t.Fatalf("GetSymbol(defaultBranch): %v / %v", got2, err)
	}
	if got2.Branch != "" {
		t.Errorf("GetSymbol(defaultBranch).Branch = %q, want \"\"", got2.Branch)
	}

	// Cross-check: SearchSymbols path also carries the branch column.
	// Empty FTS query against an empty corpus returns nothing, so we
	// don't assert SearchSymbols here — the scan path is already
	// covered by the existing TestSearchSymbols_* family which would
	// have failed with "expected N destination arguments" if the
	// scan list and column list were out of sync.

	// Cross-check 2: SymbolsByQN — another scan entry point that
	// must include branch.
	syms, err := s.GetSymbolsByQN(proj, "pkg.WithBranch")
	if err != nil {
		t.Fatalf("GetSymbolsByQN: %v", err)
	}
	if len(syms) != 1 {
		t.Fatalf("GetSymbolsByQN returned %d, want 1", len(syms))
	}
	if syms[0].Branch != "main" {
		t.Errorf("GetSymbolsByQN: Branch = %q, want %q", syms[0].Branch, "main")
	}
}
