// SPDX-License-Identifier: MIT

package index

import (
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #1231 v0.66 DOGFOOD: post-pass parity check guard. Compares the
// indexer's in-memory per-file extracted count against the DB
// COUNT(*) GROUP BY file_path. Any file with actual < expected*0.9
// trips a per-file slog.Warn + a summary warn naming #1231; counts
// are returned for IndexResult.

// Positive: a 73-extracted/8-actual shape (the literal #1231 repro
// numbers for pincher-repo's server.go) trips the guard.
func TestRunParityCheck_LargeLoss_TripsGuard(t *testing.T) {
	store := newDBStore(t)
	if err := store.UpsertProject(db.Project{ID: "p1", Path: "/p1", Name: "p1", IndexedAt: time.Now()}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	idx := New(store)

	// Persist 8 symbols for "server.go" — simulating the post-loss state.
	syms := make([]db.Symbol, 0, 8)
	for i := 0; i < 8; i++ {
		syms = append(syms, db.Symbol{
			ID:            db.MakeSymbolID("server.go", "pkg.method"+string(rune('A'+i)), "Method"),
			ProjectID:     "p1",
			FilePath:      "server.go",
			Name:          "method" + string(rune('A'+i)),
			QualifiedName: "pkg.method" + string(rune('A'+i)),
			Kind:          "Method",
			Language:      "Go",
		})
	}
	if err := store.BulkUpsertSymbols(syms); err != nil {
		t.Fatalf("BulkUpsertSymbols: %v", err)
	}

	expected := map[string]int{"server.go": 73}
	mismatch, missing := idx.runParityCheck("p1", "p1", expected)
	if mismatch != 1 {
		t.Errorf("expected mismatch=1; got %d", mismatch)
	}
	if missing != 65 {
		t.Errorf("expected missing=65 (73-8); got %d", missing)
	}
}

// Negative: a healthy 100/100 shape does NOT trip the guard.
func TestRunParityCheck_HealthyMatch_Silent(t *testing.T) {
	store := newDBStore(t)
	if err := store.UpsertProject(db.Project{ID: "p2", Path: "/p2", Name: "p2", IndexedAt: time.Now()}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	idx := New(store)

	syms := make([]db.Symbol, 0, 50)
	for i := 0; i < 50; i++ {
		syms = append(syms, db.Symbol{
			ID:            db.MakeSymbolID("good.go", "pkg.fn"+string(rune('a'+i%26))+string(rune('a'+i/26)), "Function"),
			ProjectID:     "p2",
			FilePath:      "good.go",
			Name:          "fn",
			QualifiedName: "pkg.fn" + string(rune('a'+i%26)) + string(rune('a'+i/26)),
			Kind:          "Function",
			Language:      "Go",
		})
	}
	if err := store.BulkUpsertSymbols(syms); err != nil {
		t.Fatalf("BulkUpsertSymbols: %v", err)
	}

	expected := map[string]int{"good.go": 50}
	mismatch, missing := idx.runParityCheck("p2", "p2", expected)
	if mismatch != 0 || missing != 0 {
		t.Errorf("healthy match must not trip guard; got mismatch=%d missing=%d", mismatch, missing)
	}
}

// Control: an edge case at exactly 90% retention is healthy (the
// threshold is inclusive of 90%, exclusive below). 9 of 10 expected.
func TestRunParityCheck_NinetyPercent_StillHealthy(t *testing.T) {
	store := newDBStore(t)
	if err := store.UpsertProject(db.Project{ID: "p3", Path: "/p3", Name: "p3", IndexedAt: time.Now()}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	idx := New(store)

	syms := make([]db.Symbol, 0, 9)
	for i := 0; i < 9; i++ {
		syms = append(syms, db.Symbol{
			ID:            db.MakeSymbolID("edge.go", "pkg.fn"+string(rune('a'+i)), "Function"),
			ProjectID:     "p3",
			FilePath:      "edge.go",
			Name:          "fn",
			QualifiedName: "pkg.fn" + string(rune('a'+i)),
			Kind:          "Function",
			Language:      "Go",
		})
	}
	if err := store.BulkUpsertSymbols(syms); err != nil {
		t.Fatalf("BulkUpsertSymbols: %v", err)
	}

	expected := map[string]int{"edge.go": 10}
	mismatch, _ := idx.runParityCheck("p3", "p3", expected)
	if mismatch != 0 {
		t.Errorf("exactly-90%% retention must be healthy (threshold is inclusive); got mismatch=%d", mismatch)
	}
}

// Control: a file with expected=0 (e.g., extractor returned zero
// symbols for a file — empty .go) is silently skipped, never tripping.
func TestRunParityCheck_ZeroExpected_Skipped(t *testing.T) {
	store := newDBStore(t)
	if err := store.UpsertProject(db.Project{ID: "p4", Path: "/p4", Name: "p4", IndexedAt: time.Now()}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	idx := New(store)

	expected := map[string]int{"empty.go": 0}
	mismatch, missing := idx.runParityCheck("p4", "p4", expected)
	if mismatch != 0 || missing != 0 {
		t.Errorf("zero-expected files must be silently skipped; got mismatch=%d missing=%d", mismatch, missing)
	}
}

// Cross-check: multiple files with mixed health — only the lossy ones
// surface; healthy ones are silent. Counts aggregate correctly.
func TestRunParityCheck_MixedHealth_OnlyLossyTrips(t *testing.T) {
	store := newDBStore(t)
	if err := store.UpsertProject(db.Project{ID: "p5", Path: "/p5", Name: "p5", IndexedAt: time.Now()}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	idx := New(store)

	// "healthy.go" 100/100, "lossy_a.go" 5/100, "lossy_b.go" 20/100.
	var syms []db.Symbol
	for i := 0; i < 100; i++ {
		syms = append(syms, db.Symbol{
			ID:            db.MakeSymbolID("healthy.go", "h.fn"+string(rune('a'+i%26))+string(rune('a'+i/26)), "Function"),
			ProjectID:     "p5",
			FilePath:      "healthy.go",
			Name:          "fn", QualifiedName: "h.fn" + string(rune('a'+i%26)) + string(rune('a'+i/26)), Kind: "Function", Language: "Go",
		})
	}
	for i := 0; i < 5; i++ {
		syms = append(syms, db.Symbol{
			ID:            db.MakeSymbolID("lossy_a.go", "a.fn"+string(rune('a'+i)), "Function"),
			ProjectID:     "p5",
			FilePath:      "lossy_a.go",
			Name:          "fn", QualifiedName: "a.fn" + string(rune('a'+i)), Kind: "Function", Language: "Go",
		})
	}
	for i := 0; i < 20; i++ {
		syms = append(syms, db.Symbol{
			ID:            db.MakeSymbolID("lossy_b.go", "b.fn"+string(rune('a'+i)), "Function"),
			ProjectID:     "p5",
			FilePath:      "lossy_b.go",
			Name:          "fn", QualifiedName: "b.fn" + string(rune('a'+i)), Kind: "Function", Language: "Go",
		})
	}
	if err := store.BulkUpsertSymbols(syms); err != nil {
		t.Fatalf("BulkUpsertSymbols: %v", err)
	}

	expected := map[string]int{
		"healthy.go": 100,
		"lossy_a.go": 100,
		"lossy_b.go": 100,
	}
	mismatch, missing := idx.runParityCheck("p5", "p5", expected)
	if mismatch != 2 {
		t.Errorf("expected mismatch=2 (lossy_a + lossy_b); got %d", mismatch)
	}
	// (100 - 5) + (100 - 20) = 175
	if missing != 175 {
		t.Errorf("expected missing=175; got %d", missing)
	}
}

// Direct unit on the new Store helper.
func TestSymbolCountsByFile(t *testing.T) {
	store := newDBStore(t)
	if err := store.UpsertProject(db.Project{ID: "p6", Path: "/p6", Name: "p6", IndexedAt: time.Now()}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	syms := []db.Symbol{
		{ID: "p6::a/x.go::pkg.A#Function", ProjectID: "p6", FilePath: "a/x.go", Name: "A", QualifiedName: "pkg.A", Kind: "Function", Language: "Go"},
		{ID: "p6::a/x.go::pkg.B#Function", ProjectID: "p6", FilePath: "a/x.go", Name: "B", QualifiedName: "pkg.B", Kind: "Function", Language: "Go"},
		{ID: "p6::b/y.go::pkg.C#Function", ProjectID: "p6", FilePath: "b/y.go", Name: "C", QualifiedName: "pkg.C", Kind: "Function", Language: "Go"},
	}
	if err := store.BulkUpsertSymbols(syms); err != nil {
		t.Fatalf("BulkUpsertSymbols: %v", err)
	}
	got, err := store.SymbolCountsByFile("p6")
	if err != nil {
		t.Fatalf("SymbolCountsByFile: %v", err)
	}
	if got["a/x.go"] != 2 {
		t.Errorf("a/x.go: expected 2, got %d", got["a/x.go"])
	}
	if got["b/y.go"] != 1 {
		t.Errorf("b/y.go: expected 1, got %d", got["b/y.go"])
	}
	if got["nonexistent.go"] != 0 {
		t.Errorf("nonexistent.go: expected 0, got %d", got["nonexistent.go"])
	}
}
