// SPDX-License-Identifier: MIT

package db

import (
	"context"
	"testing"
)

// #685: closure phase 2 — tests for via_kind on closure rows.
// The closure table now records the last-hop edge kind so the trace
// fast-path can populate Via without falling back to CTE.
//
// Tests cover:
//   - via_kind populated on insert + survives the SELECT round-trip
//   - non-default edge kinds filtered out at BuildClosure time
//   - closure-vs-CTE equivalence on default kinds (the #1162 unblock)
//   - mixed-kind edges record the actual last-hop kind, not 'CALLS' blanket

// closureMixedKindFixture builds a small graph with CALLS, READS, and
// HTTP_CALLS edges. Used to exercise both the build-time filter (READS
// must be dropped) and the via_kind round-trip (CALLS vs HTTP_CALLS
// must surface correctly per-row).
//
// Graph: A→B (CALLS), A→C (HTTP_CALLS), A→D (READS — must be filtered).
// B→E (CALLS) so depth-2 outbound from A reaches E via CALLS.
func closureMixedKindFixture(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	pid := "p1"
	if err := store.UpsertProject(Project{ID: pid, Path: dir, Name: "p1"}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	for _, id := range []string{"A", "B", "C", "D", "E"} {
		if _, err := store.DB().Exec(
			`INSERT INTO symbols (id, project_id, file_path, name, qualified_name, kind, language, start_byte, end_byte, start_line, end_line) VALUES (?, ?, ?, ?, ?, 'Function', 'Go', 0, 1, 1, 1)`,
			id, pid, id+".go", id, id); err != nil {
			t.Fatalf("insert symbol %s: %v", id, err)
		}
	}
	for _, e := range []struct{ from, to, kind string }{
		{"A", "B", "CALLS"},
		{"A", "C", "HTTP_CALLS"},
		{"A", "D", "READS"}, // must be filtered out by BuildClosure
		{"B", "E", "CALLS"},
	} {
		if _, err := store.DB().Exec(
			`INSERT INTO edges (project_id, from_id, to_id, kind, confidence) VALUES (?, ?, ?, ?, 1.0)`,
			pid, e.from, e.to, e.kind); err != nil {
			t.Fatalf("insert edge %s→%s (%s): %v", e.from, e.to, e.kind, e.kind)
		}
	}
	return store, pid
}

// TestBuildClosure_FiltersNonDefaultKinds pins the build-time filter:
// READS / WRITES / IMPORTS / REFERENCES edges must NOT contribute to
// the closure table. Pre-fix, closure traversal was over ALL edge
// kinds while trace's fast-path only fires for the default
// {CALLS, HTTP_CALLS, ASYNC_CALLS} set — closure returned a superset
// disagreeing with the CTE path (#1162 measurement caught this).
func TestBuildClosure_FiltersNonDefaultKinds(t *testing.T) {
	store, pid := closureMixedKindFixture(t)
	if err := store.BuildClosure(context.Background(), pid, 3); err != nil {
		t.Fatalf("BuildClosure: %v", err)
	}

	// Outbound from A: {B (CALLS), C (HTTP_CALLS), E (CALLS at depth 2)}
	// D must NOT appear because A→D is a READS edge.
	rows, err := store.TraceViaClosure(pid, "A", "outbound", 3)
	if err != nil {
		t.Fatalf("TraceViaClosure: %v", err)
	}
	got := make(map[string]bool)
	for _, r := range rows {
		got[r.SymbolID] = true
	}
	for _, want := range []string{"B", "C", "E"} {
		if !got[want] {
			t.Errorf("expected %q in closure outbound from A; got rows=%v", want, rows)
		}
	}
	if got["D"] {
		t.Errorf("D appeared in closure but A→D is a READS edge — build-time filter regression")
	}
}

// TestBuildClosure_RecordsViaKind pins the via_kind round-trip: each
// closure row must carry the actual last-hop edge kind, recoverable
// via TraceViaClosure → TraceResult.ViaKind.
func TestBuildClosure_RecordsViaKind(t *testing.T) {
	store, pid := closureMixedKindFixture(t)
	if err := store.BuildClosure(context.Background(), pid, 3); err != nil {
		t.Fatalf("BuildClosure: %v", err)
	}

	rows, err := store.TraceViaClosure(pid, "A", "outbound", 3)
	if err != nil {
		t.Fatalf("TraceViaClosure: %v", err)
	}
	gotVia := make(map[string]string)
	for _, r := range rows {
		gotVia[r.SymbolID] = r.ViaKind
	}
	wantVia := map[string]string{
		"B": "CALLS",      // A→B
		"C": "HTTP_CALLS", // A→C — HTTP_CALLS, NOT 'CALLS' (catches the regression where via_kind was blanket-default)
		"E": "CALLS",      // last hop B→E
	}
	for sym, want := range wantVia {
		if got := gotVia[sym]; got != want {
			t.Errorf("via_kind for %s: got %q, want %q (last-hop kind)", sym, got, want)
		}
	}
}

// TestBuildClosure_VsCTEEquivalence is the #1162-unblocking integrity
// gate: closure-path trace and CTE-path trace must return the same
// (symbol_id, depth, via_kind) tuples for the default edge kinds.
// Pre-#685 they diverged because closure traversed all kinds. With
// the filter + via_kind in place, they should agree exactly.
func TestBuildClosure_VsCTEEquivalence(t *testing.T) {
	store, pid := closureMixedKindFixture(t)
	if err := store.BuildClosure(context.Background(), pid, 3); err != nil {
		t.Fatalf("BuildClosure: %v", err)
	}
	defaultKinds := []string{"CALLS", "HTTP_CALLS", "ASYNC_CALLS"}

	for _, start := range []string{"A", "B"} {
		clos, err := store.TraceViaClosure(pid, start, "outbound", 3)
		if err != nil {
			t.Fatalf("TraceViaClosure(%s): %v", start, err)
		}
		cte, err := store.TraceViaCTEScoped(pid, start, "outbound", defaultKinds, 3)
		if err != nil {
			t.Fatalf("TraceViaCTEScoped(%s): %v", start, err)
		}

		closIDs := make(map[string]int)
		for _, r := range clos {
			closIDs[r.SymbolID] = r.Depth
		}
		cteIDs := make(map[string]int)
		for _, r := range cte {
			cteIDs[r.SymbolID] = r.Depth
		}
		if len(closIDs) != len(cteIDs) {
			t.Errorf("start=%s: closure returned %d rows, CTE returned %d — not equivalent (closIDs=%v cteIDs=%v)",
				start, len(closIDs), len(cteIDs), closIDs, cteIDs)
		}
		for id, depth := range cteIDs {
			if closIDs[id] != depth {
				t.Errorf("start=%s symbol=%s: CTE depth=%d, closure depth=%d (or missing)",
					start, id, depth, closIDs[id])
			}
		}
	}
}

// TestBuildClosure_InboundViaKind covers the symmetric inbound case.
// closure's inbound query joins on to_id, and via_kind must still
// surface as the edge kind that "completed the path" — for inbound
// that's the FIRST edge in the walk (the one starting from the
// inbound predecessor toward the start node). Pin the actual
// behaviour so a future inbound-semantics tweak doesn't silently
// flip the contract.
func TestBuildClosure_InboundViaKind(t *testing.T) {
	store, pid := closureMixedKindFixture(t)
	if err := store.BuildClosure(context.Background(), pid, 3); err != nil {
		t.Fatalf("BuildClosure: %v", err)
	}
	// inbound to E: A reaches E via B (depth 2) — via_kind should be
	// the edge kind that put E in the closure row from A's BFS,
	// i.e. B→E which is CALLS.
	rows, err := store.TraceViaClosure(pid, "E", "inbound", 3)
	if err != nil {
		t.Fatalf("TraceViaClosure: %v", err)
	}
	gotVia := make(map[string]string)
	for _, r := range rows {
		gotVia[r.SymbolID] = r.ViaKind
	}
	// B → E directly: via_kind should be CALLS.
	if gotVia["B"] != "CALLS" {
		t.Errorf("inbound to E from B: via_kind=%q, want CALLS", gotVia["B"])
	}
	// A → ... → E: via_kind should also be CALLS (B→E is the last
	// hop that reached E).
	if gotVia["A"] != "CALLS" {
		t.Errorf("inbound to E from A: via_kind=%q, want CALLS (last hop B→E)", gotVia["A"])
	}
}
