// SPDX-License-Identifier: MIT

package cypher

import (
	"context"
	"strings"
	"testing"
)

// #1109: *0..N silently coerces to *1..N because pincher's BFS only
// emits length≥1 hops. Cypher's *0..N includes the seed itself
// (length-0 path) — pre-fix the agent read the result as
// seed-inclusive when it was actually seed-exclusive. Same silent-
// confidently-wrong family as #869 (inverted hop range).

func TestExecute_ZeroMinHopRange_SurfacesWarning(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "a", "A", "Function", "Go")
	insertSym(t, db, "b", "B", "Function", "Go")
	insertEdge(t, db, "a", "b", "CALLS")

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (a)-[:CALLS*0..3]->(b) WHERE a.name = "A" RETURN b.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var saw bool
	for _, w := range r.Warnings {
		if strings.Contains(w, "*0..") && strings.Contains(w, "*1..") {
			saw = true
			break
		}
	}
	if !saw {
		t.Errorf("expected min-clamped hop-range warning naming the *0→*1 coercion; got: %v", r.Warnings)
	}
}

// Control: a well-formed *1..N produces no min-clamped warning.
func TestExecute_NormalHopRange_NoMinClampedWarning(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "a", "A", "Function", "Go")
	insertSym(t, db, "b", "B", "Function", "Go")
	insertEdge(t, db, "a", "b", "CALLS")

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (a)-[:CALLS*1..3]->(b) WHERE a.name = "A" RETURN b.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, w := range r.Warnings {
		if strings.Contains(w, "*0..") && strings.Contains(w, "coerced") {
			t.Errorf("*1..3 must not trip min-clamped warning; got: %q", w)
		}
	}
}

// *0..0 — every hop is zero-length, every match is seed-only. Post-
// fix the agent should learn the coercion happened.
func TestExecute_ZeroOnlyHopRange_SurfacesWarning(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "a", "A", "Function", "Go")
	insertSym(t, db, "b", "B", "Function", "Go")
	insertEdge(t, db, "a", "b", "CALLS")

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (a)-[:CALLS*0..0]->(b) WHERE a.name = "A" RETURN b.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var saw bool
	for _, w := range r.Warnings {
		if strings.Contains(w, "*0..") {
			saw = true
			break
		}
	}
	if !saw {
		t.Errorf("expected hop-range warning for *0..0; got: %v", r.Warnings)
	}
}
