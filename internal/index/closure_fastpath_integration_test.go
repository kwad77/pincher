// SPDX-License-Identifier: MIT

package index

import (
	"context"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// TestTraceByID_ClosureFastPath_FiresAndReturnsHops verifies the
// closure-fast-path branch in TraceByID (#652 phase 1):
//   - PINCHER_CLOSURE_TABLES=1 set
//   - closure table populated for the project
//   - default trace edge-kind set
//   → take the fast-path (single SELECT) instead of the recursive CTE.
//
// Compared to a parallel CTE-only path the fast-path returns hops with
// empty Via (phase-1 trade-off). Both should agree on the hop set.
func TestTraceByID_ClosureFastPath_FiresAndReturnsHops(t *testing.T) {
	idx, store := newTestIndexer(t)
	pid := "fp"
	if err := store.UpsertProject(db.Project{ID: pid, Path: "/x", Name: pid}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	// Tiny graph A→B→C, plus A→C — exactly the closurebench fixture shape.
	for _, id := range []string{"A", "B", "C"} {
		if _, err := store.DB().Exec(
			`INSERT INTO symbols (id, project_id, file_path, name, qualified_name, kind, language, start_byte, end_byte, start_line, end_line) VALUES (?, ?, ?, ?, ?, 'Function', 'Go', 0, 1, 1, 1)`,
			id, pid, id+".go", id, id); err != nil {
			t.Fatalf("symbol: %v", err)
		}
	}
	for _, e := range []struct{ from, to string }{{"A", "B"}, {"B", "C"}, {"A", "C"}} {
		if _, err := store.DB().Exec(
			`INSERT INTO edges (project_id, from_id, to_id, kind, confidence) VALUES (?, ?, ?, 'CALLS', 1.0)`,
			pid, e.from, e.to); err != nil {
			t.Fatalf("edge: %v", err)
		}
	}
	if err := store.BuildClosure(context.Background(), pid, 3); err != nil {
		t.Fatalf("BuildClosure: %v", err)
	}

	// Toggle env to force the fast-path.
	t.Setenv("PINCHER_CLOSURE_TABLES", "1")

	hops, err := idx.TraceByID(context.Background(), pid, "A", "outbound", 3, false)
	if err != nil {
		t.Fatalf("TraceByID: %v", err)
	}
	// Outbound from A: B(d=1) and C(d=1). Order may vary; assert set + depths.
	// #685 phase 2: Via must now be populated from closure.via_kind —
	// the v0.54 phase-1 empty-Via trade-off is closed. Fixture edges
	// are all CALLS so every hop's Via must report "CALLS" exactly.
	gotIDs := make(map[string]int)
	for _, h := range hops {
		gotIDs[h.Symbol.ID] = h.Depth
		if h.Via != "CALLS" {
			t.Errorf("closure-fast-path Via should be populated from via_kind (phase-2/#685); got Via=%q on hop=%s", h.Via, h.Symbol.ID)
		}
	}
	if gotIDs["B"] != 1 {
		t.Errorf("expected B at depth=1; got %v", gotIDs)
	}
	if gotIDs["C"] != 1 {
		t.Errorf("expected C at depth=1; got %v", gotIDs)
	}
}

// TestTraceByID_NonDefaultKinds_FallsThroughToCTE verifies the gate:
// when a caller passes a non-default edge-kind set, even with the env
// flag on and closure populated, we MUST take the CTE path (closure
// rows don't store edge kind).
func TestTraceByID_NonDefaultKinds_FallsThroughToCTE(t *testing.T) {
	idx, store := newTestIndexer(t)
	pid := "ck"
	if err := store.UpsertProject(db.Project{ID: pid, Path: "/x", Name: pid}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	if _, err := store.DB().Exec(
		`INSERT INTO symbols (id, project_id, file_path, name, qualified_name, kind, language, start_byte, end_byte, start_line, end_line) VALUES (?, ?, ?, ?, ?, 'Function', 'Go', 0, 1, 1, 1)`,
		"A", pid, "a.go", "A", "A"); err != nil {
		t.Fatalf("symbol: %v", err)
	}
	if _, err := store.DB().Exec(
		`INSERT INTO symbols (id, project_id, file_path, name, qualified_name, kind, language, start_byte, end_byte, start_line, end_line) VALUES (?, ?, ?, ?, ?, 'Function', 'Go', 0, 1, 1, 1)`,
		"B", pid, "b.go", "B", "B"); err != nil {
		t.Fatalf("symbol: %v", err)
	}
	// Edge kind READS — outside the default trace set.
	if _, err := store.DB().Exec(
		`INSERT INTO edges (project_id, from_id, to_id, kind, confidence) VALUES (?, 'A', 'B', 'READS', 1.0)`,
		pid); err != nil {
		t.Fatalf("edge: %v", err)
	}
	if err := store.BuildClosure(context.Background(), pid, 3); err != nil {
		t.Fatalf("BuildClosure: %v", err)
	}
	t.Setenv("PINCHER_CLOSURE_TABLES", "1")

	// Non-default kinds → CTE path. CTE preserves Via.
	hops, err := idx.TraceByID(context.Background(), pid, "A", "outbound", 3, false, "READS")
	if err != nil {
		t.Fatalf("TraceByID: %v", err)
	}
	if len(hops) != 1 || hops[0].Symbol.ID != "B" {
		t.Fatalf("expected one hop to B via CTE path; got %+v", hops)
	}
	if hops[0].Via != "READS" {
		t.Errorf("CTE path should preserve Via; got Via=%q", hops[0].Via)
	}
}

// TestTraceByID_ClosureOff_UsesCTE verifies the default (env unset) path
// still works — we shouldn't have inadvertently broken existing trace
// callers when adding the fast-path.
func TestTraceByID_ClosureOff_UsesCTE(t *testing.T) {
	idx, store := newTestIndexer(t)
	pid := "off"
	if err := store.UpsertProject(db.Project{ID: pid, Path: "/x", Name: pid}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	for _, id := range []string{"A", "B"} {
		if _, err := store.DB().Exec(
			`INSERT INTO symbols (id, project_id, file_path, name, qualified_name, kind, language, start_byte, end_byte, start_line, end_line) VALUES (?, ?, ?, ?, ?, 'Function', 'Go', 0, 1, 1, 1)`,
			id, pid, id+".go", id, id); err != nil {
			t.Fatalf("symbol: %v", err)
		}
	}
	if _, err := store.DB().Exec(
		`INSERT INTO edges (project_id, from_id, to_id, kind, confidence) VALUES (?, 'A', 'B', 'CALLS', 1.0)`, pid); err != nil {
		t.Fatalf("edge: %v", err)
	}
	// Env explicitly off — even if the closure table existed, the
	// flag gate would route to CTE.
	t.Setenv("PINCHER_CLOSURE_TABLES", "0")

	hops, err := idx.TraceByID(context.Background(), pid, "A", "outbound", 3, false)
	if err != nil {
		t.Fatalf("TraceByID: %v", err)
	}
	if len(hops) != 1 || hops[0].Symbol.ID != "B" {
		t.Fatalf("expected one hop to B; got %+v", hops)
	}
	if hops[0].Via == "" {
		t.Errorf("CTE path should populate Via; got empty")
	}
}
