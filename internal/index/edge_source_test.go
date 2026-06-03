// SPDX-License-Identifier: MIT

package index

import (
	"context"
	"strings"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// TestIndex_ResolvePassEdges_AtomicallyReplaced pins #475: a stale
// resolve-pass edge that the current rules would no longer produce
// must be cleared on the next Index() call. Per-file edges must
// survive untouched.
//
// Scenario:
//  1. Index a project that produces both a per-file CALLS edge
//     (intra-file) and a resolve-pass CALLS edge (cross-file).
//  2. Manually inject a stale resolve-pass CALLS edge whose target
//     is no longer in the project (simulates the v0.16.0 #465
//     polymorphic-method leak: a real edge from a prior rule set).
//  3. Re-Index. The stale resolve-pass edge must be gone; the
//     per-file edge must still be present.
func TestIndex_ResolvePassEdges_AtomicallyReplaced(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()

	writeFile(t, dir, "callee.go", "package mypkg\n\nfunc Foo() {}\n")
	writeFile(t, dir, "caller.go", "package mypkg\n\nfunc Bar() {\n\tFoo()\n}\nfunc Inner() {\n\tBar()\n}\n")

	if _, err := idx.Index(context.Background(), dir, true); err != nil {
		t.Fatalf("first Index: %v", err)
	}

	barID := db.MakeSymbolID("caller.go", "mypkg.Bar", "Function")
	innerID := db.MakeSymbolID("caller.go", "mypkg.Inner", "Function")
	fooID := db.MakeSymbolID("callee.go", "mypkg.Foo", "Function")
	projectID := db.ProjectIDFromPath(dir)

	// Sanity: per-file edge Inner→Bar (intra-caller.go) and resolve-pass
	// edge Bar→Foo (cross-file) both present.
	innerEdges, _ := store.EdgesFrom(innerID, []string{"CALLS"})
	if !hasEdgeTo(innerEdges, barID) {
		t.Fatalf("expected Inner→Bar per-file CALLS edge after first Index; got %v", innerEdges)
	}
	barEdges, _ := store.EdgesFrom(barID, []string{"CALLS"})
	if !hasEdgeTo(barEdges, fooID) {
		t.Fatalf("expected Bar→Foo resolve-pass CALLS edge after first Index; got %v", barEdges)
	}

	// Inject a stale resolve-pass edge pointing at a target the rules
	// would no longer produce. (We pick a real symbol as the target;
	// what matters is the (from, to) pair is NOT in current pending_edges.)
	stale := db.Edge{
		ProjectID:  projectID,
		FromID:     barID,
		ToID:       innerID, // Bar doesn't actually call Inner — stale
		Kind:       "CALLS",
		Confidence: 0.7,
		Source:     "resolve_pass",
	}
	if err := store.BulkUpsertEdges([]db.Edge{stale}); err != nil {
		t.Fatalf("inject stale: %v", err)
	}
	preStale, _ := store.EdgesFrom(barID, []string{"CALLS"})
	if !hasEdgeTo(preStale, innerID) {
		t.Fatalf("stale Bar→Inner injection didn't take; want it present before re-index")
	}

	// Re-index — resolve pass should DELETE the stale edge.
	if _, err := idx.Index(context.Background(), dir, true); err != nil {
		t.Fatalf("second Index: %v", err)
	}

	postEdges, _ := store.EdgesFrom(barID, []string{"CALLS"})
	if hasEdgeTo(postEdges, innerID) {
		t.Errorf("#475 not honored: stale Bar→Inner resolve-pass edge survived re-index; edges: %v", postEdges)
	}
	if !hasEdgeTo(postEdges, fooID) {
		t.Errorf("Bar→Foo (real resolve-pass edge) was wiped by the atomic replace; should have been re-inserted")
	}

	// Per-file edge survives.
	innerPost, _ := store.EdgesFrom(innerID, []string{"CALLS"})
	if !hasEdgeTo(innerPost, barID) {
		t.Errorf("Inner→Bar per-file edge wiped by the atomic replace; should be untouched")
	}
}

// hasEdgeTo is a small helper for the test above — substring match
// against an Edge slice on ToID.
func hasEdgeTo(edges []db.Edge, toID string) bool {
	for _, e := range edges {
		if e.ToID == toID {
			return true
		}
	}
	return false
}

// TestIndex_PerFileEdges_HaveSourceTag pins the source tag on per-file
// edge inserts (#475 ground truth). Every kind=CALLS row produced by
// the per-file pass must have source='per_file'; every row produced by
// the tail resolve pass must have source='resolve_pass'. The test
// reaches through the SQL layer because Source isn't exposed on the
// public EdgesFrom shape.
func TestIndex_PerFileEdges_HaveSourceTag(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()

	writeFile(t, dir, "callee.go", "package mypkg\n\nfunc Foo() {}\n")
	writeFile(t, dir, "caller.go", "package mypkg\n\nfunc Bar() {\n\tFoo()\n}\nfunc Inner() {\n\tBar()\n}\n")

	if _, err := idx.Index(context.Background(), dir, true); err != nil {
		t.Fatalf("Index: %v", err)
	}
	projectID := db.ProjectIDFromPath(dir)

	rows, err := store.DB().Query(
		`SELECT from_id, to_id, source FROM edges WHERE project_id=? AND kind='CALLS'`,
		projectID,
	)
	if err != nil {
		t.Fatalf("query edges: %v", err)
	}
	defer rows.Close()
	seen := map[string]string{}
	for rows.Next() {
		var fromID, toID, source string
		if err := rows.Scan(&fromID, &toID, &source); err != nil {
			t.Fatalf("scan: %v", err)
		}
		seen[fromID+"->"+toID] = source
	}

	innerToBar := db.MakeSymbolID("caller.go", "mypkg.Inner", "Function") +
		"->" + db.MakeSymbolID("caller.go", "mypkg.Bar", "Function")
	barToFoo := db.MakeSymbolID("caller.go", "mypkg.Bar", "Function") +
		"->" + db.MakeSymbolID("callee.go", "mypkg.Foo", "Function")

	if got := seen[innerToBar]; got != "per_file" {
		t.Errorf("Inner→Bar (intra-file) source=%q, want per_file", got)
	}
	if got := seen[barToFoo]; got != "resolve_pass" {
		t.Errorf("Bar→Foo (cross-file) source=%q, want resolve_pass", got)
	}
	// Defensive: surface the full map on failure for diagnosis.
	if t.Failed() {
		var lines []string
		for k, v := range seen {
			lines = append(lines, k+" = "+v)
		}
		t.Logf("all CALLS edges:\n%s", strings.Join(lines, "\n"))
	}
}
