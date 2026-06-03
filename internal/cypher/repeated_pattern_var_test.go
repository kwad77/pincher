// SPDX-License-Identifier: MIT

package cypher

import (
	"context"
	"testing"
)

// #1124: `MATCH (a)-[:CALLS]->(a)` is standard Cypher for self-loops on a
// — bind `a` once, return only edges where from_id == to_id. Pre-fix
// pincher's runJoinQuery bound the two `a`s independently and returned
// every CALLS edge in the graph. A recursion-finder query silently
// returned all CALLS edges; user reads "functions that call themselves"
// but gets all callers.
//
// The fix injects `AND a.id = b.id` so the row set matches standard
// semantics, plus a warning so the row-count change is teachable.

// Positive: self-loop returns only self-edges, with warning.
func TestExecute_RepeatedPatternVar_ReturnsOnlySelfLoops(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "f1", "recurse", "Function", "Go")
	insertSym(t, db, "f2", "caller", "Function", "Go")
	insertSym(t, db, "f3", "callee", "Function", "Go")
	insertEdge(t, db, "f1", "f1", "CALLS") // self-loop — desired
	insertEdge(t, db, "f2", "f3", "CALLS") // not a self-loop
	insertEdge(t, db, "f3", "f1", "CALLS") // not a self-loop

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (a:Function)-[:CALLS]->(a) RETURN a.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(r.Rows) != 1 {
		t.Fatalf("expected exactly 1 self-loop; got %d rows: %v", len(r.Rows), r.Rows)
	}
	if r.Rows[0]["a.name"] != "recurse" {
		t.Errorf("expected recurse (self-loop); got %v", r.Rows[0])
	}
	if !hasWarning(r.Warnings, "self-loop", "from_id = to_id") {
		t.Errorf("expected self-loop semantics warning; got %v", r.Warnings)
	}
}

// Control: independently-named endpoints — no warning, no filter, all edges.
func TestExecute_DistinctPatternVars_NoFilterApplied(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "f1", "a", "Function", "Go")
	insertSym(t, db, "f2", "b", "Function", "Go")
	insertEdge(t, db, "f1", "f1", "CALLS")
	insertEdge(t, db, "f1", "f2", "CALLS")
	insertEdge(t, db, "f2", "f1", "CALLS")

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (a:Function)-[:CALLS]->(b:Function) RETURN a.name, b.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(r.Rows) != 3 {
		t.Errorf("distinct vars should not enforce self-loop; expected 3 edges, got %d: %v", len(r.Rows), r.Rows)
	}
	for _, w := range r.Warnings {
		if containsCI1124(w, "self-loop") {
			t.Errorf("distinct-var query should not trip the self-loop warning; got %v", r.Warnings)
		}
	}
}

// Control: node-only scan with same-name binding (no edge) — no warning.
// (No edge pattern means there's no "both ends" to detect.)
func TestExecute_NodeOnlyScan_NoSelfLoopWarning(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "f1", "x", "Function", "Go")

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (a:Function) RETURN a.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, w := range r.Warnings {
		if containsCI1124(w, "self-loop") {
			t.Errorf("node-only scan should not trip self-loop warning; got %v", r.Warnings)
		}
	}
}

func containsCI1124(s, frag string) bool {
	return hasWarning([]string{s}, frag)
}
