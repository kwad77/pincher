// SPDX-License-Identifier: MIT

package cypher

import (
	"context"
	"strings"
	"testing"
)

// #869: a variable-length pattern written with bounds backwards
// (`*3..1`) used to silently collapse to `*3..3` via parseHops' old
// `max = min` clamp — a transposed-bounds typo returned depth-3-only
// results that matched neither the written range nor the likely intent
// (`*1..3`), with no signal. parseHops now swaps to the intended range
// and the engine warns.

func TestExecute_InvertedHopRange_SurfacesWarning(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "a", "A", "Function", "Go")
	insertSym(t, db, "b", "B", "Function", "Go")
	insertSym(t, db, "c", "C", "Function", "Go")
	insertEdge(t, db, "a", "b", "CALLS")
	insertEdge(t, db, "b", "c", "CALLS")

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (a)-[:CALLS*3..1]->(b) WHERE a.name = "A" RETURN b.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var saw bool
	for _, w := range r.Warnings {
		if strings.Contains(w, "bounds backwards") && strings.Contains(w, "*1..3") {
			saw = true
			break
		}
	}
	if !saw {
		t.Errorf("expected inverted-hop-range warning naming the swapped *1..3 range; got: %v", r.Warnings)
	}

	// Swapped to *1..3: A reaches B (1 hop) and C (2 hops). Pre-fix
	// (*3..3) this returned nothing reachable within exactly 3 hops.
	got := map[string]bool{}
	for _, row := range r.Rows {
		if n, ok := row["b.name"].(string); ok {
			got[n] = true
		}
	}
	if !got["B"] || !got["C"] {
		t.Errorf("`*3..1` should swap to `*1..3` and reach B and C; got rows %v", r.Rows)
	}
}

// Control: a normal range never trips the inverted-hop warning.
func TestExecute_NormalHopRange_NoWarning(t *testing.T) {
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
		if strings.Contains(w, "bounds backwards") {
			t.Errorf("a well-ordered range must not warn; got: %v", w)
		}
	}
}
