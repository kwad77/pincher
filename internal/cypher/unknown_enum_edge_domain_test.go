// SPDX-License-Identifier: MIT

package cypher

import (
	"context"
	"testing"
)

// #1132: pre-fix, `WHERE r.kind = "READS"` on a pattern with edge
// variable `r` emitted a warning listing SYMBOL kinds (Function,
// Method, Class…) even though the user named an edge variable. The
// warning was lying about which vocabulary the user got wrong: the
// answer "did you mean Function, Method, Class…?" is nonsensical
// because the user was asking about edges. New scope-aware probe
// runs against the edges table when c.variable is bound to an edge.

// Positive: r.kind = unknown_edge_kind warns with EDGE vocabulary.
func TestExecute_WhereOnEdgeKind_WarnsWithEdgeVocabulary(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "f1", "a", "Function", "Go")
	insertSym(t, db, "f2", "b", "Function", "Go")
	insertEdge(t, db, "f1", "f2", "CALLS")
	insertEdge(t, db, "f1", "f2", "READS")

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (a:Function)-[r:CALLS]->(b) WHERE r.kind = "BOGUS_EDGE" RETURN a.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !hasWarning(r.Warnings, "BOGUS_EDGE", "edges") {
		t.Errorf("warning should name the unknown value + scope it to 'edges' (not 'symbols'); got %v", r.Warnings)
	}
	// Negative: must NOT list symbol kinds (Function, Method, Class)
	// — that's the cross-domain confusion the fix is for.
	for _, w := range r.Warnings {
		if hasWarning([]string{w}, "Function") || hasWarning([]string{w}, "Method") {
			t.Errorf("warning for an edge variable must not list symbol kinds; got: %v", r.Warnings)
		}
	}
	// Positive: the actual edge kinds (CALLS, READS) should appear.
	if !hasWarning(r.Warnings, "CALLS") {
		t.Errorf("warning should list the actual edge kinds (CALLS, READS); got %v", r.Warnings)
	}
}

// Control: node-side WHERE n.kind = unknown still warns with SYMBOL
// vocabulary (the #501 path must keep working).
func TestExecute_WhereOnNodeKind_WarnsWithSymbolVocabulary(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "f1", "a", "Function", "Go")

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (n) WHERE n.kind = "BOGUS_SYMBOL_KIND" RETURN n.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !hasWarning(r.Warnings, "BOGUS_SYMBOL_KIND", "symbols") {
		t.Errorf("node-side warning should scope to 'symbols'; got %v", r.Warnings)
	}
	if !hasWarning(r.Warnings, "Function") {
		t.Errorf("node-side warning should list the actual symbol kinds (Function); got %v", r.Warnings)
	}
}
