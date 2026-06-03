// SPDX-License-Identifier: MIT

package db

import (
	"testing"
)

// TestTraceViaCTEScoped_DoesNotCrossProjects pins #7: even when two
// projects share an edge-endpoint symbol ID (which CAN happen pre-#1,
// since the symbol-ID format is global), the recursive BFS MUST stay
// within the requested project's edges.
func TestTraceViaCTEScoped_DoesNotCrossProjects(t *testing.T) {
	store := newTestStore(t)

	if err := store.UpsertProject(Project{ID: "scope-A", Path: "/tmp/A", Name: "A"}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertProject(Project{ID: "scope-B", Path: "/tmp/B", Name: "B"}); err != nil {
		t.Fatal(err)
	}

	// Synthesise the collision: same start ID exists in both projects.
	startID := "shared.go::pkg.Start#Function"
	if err := store.BulkUpsertSymbols([]Symbol{
		{ID: startID, ProjectID: "scope-A", FilePath: "shared.go", Name: "Start",
			QualifiedName: "pkg.Start", Kind: "Function", Language: "Go"},
	}); err != nil {
		t.Fatal(err)
	}
	// Project-A neighbour reachable via CALLS edge.
	neighborA := "pkgA.go::pkg.NeighborA#Function"
	if err := store.BulkUpsertSymbols([]Symbol{
		{ID: neighborA, ProjectID: "scope-A", FilePath: "pkgA.go", Name: "NeighborA",
			QualifiedName: "pkg.NeighborA", Kind: "Function", Language: "Go"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.BulkUpsertEdges([]Edge{
		{ProjectID: "scope-A", FromID: startID, ToID: neighborA, Kind: "CALLS"},
	}); err != nil {
		t.Fatal(err)
	}

	// Project-B neighbour also wired via CALLS from the same startID,
	// scoped to project B's edge row. With the unscoped TraceViaCTE,
	// a search starting from `startID` would hop to BOTH neighbours
	// because the join is on edge.from_id alone. With the scoped
	// variant, only the neighbour in the requested project comes back.
	neighborB := "pkgB.go::pkg.NeighborB#Function"
	if err := store.BulkUpsertSymbols([]Symbol{
		{ID: neighborB, ProjectID: "scope-B", FilePath: "pkgB.go", Name: "NeighborB",
			QualifiedName: "pkg.NeighborB", Kind: "Function", Language: "Go"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.BulkUpsertEdges([]Edge{
		{ProjectID: "scope-B", FromID: startID, ToID: neighborB, Kind: "CALLS"},
	}); err != nil {
		t.Fatal(err)
	}

	// Scoped trace from scope-A: must return neighborA only.
	resA, err := store.TraceViaCTEScoped("scope-A", startID, "outbound", []string{"CALLS"}, 3)
	if err != nil {
		t.Fatalf("TraceViaCTEScoped(A): %v", err)
	}
	hasA, hasB := false, false
	for _, r := range resA {
		if r.SymbolID == neighborA {
			hasA = true
		}
		if r.SymbolID == neighborB {
			hasB = true
		}
	}
	if !hasA {
		t.Errorf("scoped trace from scope-A: expected neighborA in %v", resA)
	}
	if hasB {
		t.Errorf("scoped trace from scope-A leaked into scope-B: neighborB present in %v", resA)
	}

	// Scoped trace from scope-B: symmetric.
	resB, err := store.TraceViaCTEScoped("scope-B", startID, "outbound", []string{"CALLS"}, 3)
	if err != nil {
		t.Fatalf("TraceViaCTEScoped(B): %v", err)
	}
	hasA, hasB = false, false
	for _, r := range resB {
		if r.SymbolID == neighborA {
			hasA = true
		}
		if r.SymbolID == neighborB {
			hasB = true
		}
	}
	if !hasB {
		t.Errorf("scoped trace from scope-B: expected neighborB in %v", resB)
	}
	if hasA {
		t.Errorf("scoped trace from scope-B leaked into scope-A: neighborA present in %v", resB)
	}

	// Unscoped trace (legacy): may hop both — pin that we're not
	// accidentally regressing the legacy path. With both edges sharing
	// from_id=startID and the same kind, both targets are reachable.
	all, err := store.TraceViaCTE(startID, "outbound", []string{"CALLS"}, 3)
	if err != nil {
		t.Fatalf("TraceViaCTE (unscoped): %v", err)
	}
	hasA, hasB = false, false
	for _, r := range all {
		if r.SymbolID == neighborA {
			hasA = true
		}
		if r.SymbolID == neighborB {
			hasB = true
		}
	}
	if !hasA || !hasB {
		t.Errorf("unscoped trace must reach BOTH projects' neighbours (legacy contract); got A=%v B=%v", hasA, hasB)
	}
}

// TestGetSymbolScoped_RejectsCrossProject pins the same property at the
// single-symbol lookup layer (#2): asking for project A's view of a
// colliding ID must not return project B's row.
func TestGetSymbolScoped_RejectsCrossProject(t *testing.T) {
	store := newTestStore(t)
	if err := store.UpsertProject(Project{ID: "p-A", Path: "/tmp/A", Name: "A"}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertProject(Project{ID: "p-B", Path: "/tmp/B", Name: "B"}); err != nil {
		t.Fatal(err)
	}
	id := "main.go::main.main#Function"
	if err := store.BulkUpsertSymbols([]Symbol{
		{ID: id, ProjectID: "p-A", FilePath: "main.go", Name: "main",
			QualifiedName: "main.main", Kind: "Function", Language: "Go", Signature: "A"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.BulkUpsertSymbols([]Symbol{
		{ID: id, ProjectID: "p-B", FilePath: "main.go", Name: "main",
			QualifiedName: "main.main", Kind: "Function", Language: "Go", Signature: "B"},
	}); err != nil {
		t.Fatal(err)
	}

	// Schema v28 (#1231): composite PRIMARY KEY (project_id, id) means
	// BOTH p-A's row AND p-B's row coexist — same id, two rows. Pre-v28
	// the bare-id PK forced INSERT OR REPLACE to clobber p-A's row when
	// p-B wrote; the test below used to assert that bug-behaviour. With
	// the composite PK fix, scoped lookups return each project's own
	// row distinctly.

	// Unscoped GetSymbol on a colliding id is now ambiguous — it returns
	// one of the rows (which one depends on storage order). The exact
	// row isn't a contract; what matters is that the scoped paths
	// disambiguate correctly. Just assert a non-nil result.
	got, err := store.GetSymbol(id)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Errorf("unscoped GetSymbol returned nil on colliding id; want one of A or B")
	}

	// GetSymbolScoped("p-B", id) returns p-B's row.
	got, err = store.GetSymbolScoped("p-B", id)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Signature != "B" {
		t.Errorf("scoped p-B: expected B's row, got %+v", got)
	}

	// GetSymbolScoped("p-A", id) now returns A's row (not nil). Pre-v28
	// it would have returned nil because A's row was clobbered by B's
	// write; that was the #1231 bug shape on shared DBs. With composite
	// PK both rows coexist and the scoped lookup picks the right one.
	got, err = store.GetSymbolScoped("p-A", id)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Signature != "A" {
		t.Errorf("scoped p-A: expected A's row (composite PK preserves it post-v28), got %+v", got)
	}

	// Empty projectID is a misuse — explicit error so callers don't
	// silently degrade to unscoped behaviour by passing "".
	if _, err := store.GetSymbolScoped("", id); err == nil {
		t.Errorf("GetSymbolScoped with empty projectID must error")
	}
}
