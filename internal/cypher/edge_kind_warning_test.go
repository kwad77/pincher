// SPDX-License-Identifier: MIT

package cypher

import (
	"context"
	"strings"
	"testing"
)

// #867: an unknown relationship type — `-[:CALLZ]->`, a typo — compiled
// to `e.kind IN ('CALLZ')`, matched nothing, and returned 0 rows with
// no signal. The edge-side twin of #473 (unknown property): the caller
// can't tell "no CALLZ edges" from "CALLZ isn't a kind." Now it warns.
// Also: edge kinds are upper-cased at parse time, so a lower-case
// `-[:calls]->` resolves instead of silently matching nothing.

func TestExecute_UnknownEdgeKind_SurfacesWarning(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "a", "A", "Function", "Go")
	insertSym(t, db, "b", "B", "Function", "Go")
	insertEdge(t, db, "a", "b", "CALLS")

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (a)-[:CALLZ]->(b) RETURN a.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var saw bool
	for _, w := range r.Warnings {
		if strings.Contains(w, "edge kind") && strings.Contains(w, "CALLZ") {
			saw = true
			break
		}
	}
	if !saw {
		t.Errorf("expected unknown-edge-kind warning naming CALLZ; got: %v", r.Warnings)
	}
	if r.Total != 0 {
		t.Errorf("CALLZ matches no edge — expected 0 rows; got %d", r.Total)
	}
}

// A lower-case but valid kind must resolve (parse-time upper-casing) and
// must NOT trip the unknown-kind warning.
func TestExecute_LowercaseEdgeKind_ResolvesNoWarning(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "a", "A", "Function", "Go")
	insertSym(t, db, "b", "B", "Function", "Go")
	insertEdge(t, db, "a", "b", "CALLS")

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (a)-[:calls]->(b) RETURN a.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, w := range r.Warnings {
		if strings.Contains(w, "edge kind") {
			t.Errorf("lower-case `calls` is a valid kind — must not warn; got: %v", w)
		}
	}
	if r.Total != 1 {
		t.Errorf("`-[:calls]->` must resolve to the CALLS edge (parse-time upper-case); got %d rows", r.Total)
	}
}

// Control: a valid upper-case kind warns about nothing.
func TestExecute_ValidEdgeKind_NoWarning(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "a", "A", "Function", "Go")
	insertSym(t, db, "b", "B", "Function", "Go")
	insertEdge(t, db, "a", "b", "CALLS")

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (a)-[:CALLS]->(b) RETURN a.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, w := range r.Warnings {
		if strings.Contains(w, "edge kind") {
			t.Errorf("CALLS is valid — must not warn; got: %v", w)
		}
	}
	if r.Total != 1 {
		t.Errorf("expected 1 row for the valid CALLS edge; got %d", r.Total)
	}
}
