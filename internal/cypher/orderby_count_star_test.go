// SPDX-License-Identifier: MIT

package cypher

import (
	"context"
	"testing"
)

// #1120: ORDER BY COUNT(*) silently no-op'd because the parser dropped
// the asterisk (tokenized as HOPS) and q.orderBy was set to "COUNT()"
// while the projection column was "COUNT(*)". The grouped-rows sort
// looked up grouped[i]["COUNT()"], found nothing, returned the rows
// in scan order. Same `*`-as-HOPS shape as #946 (parseReturn fix).

func TestExecute_OrderByCountStar_ActuallySorts(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	// Seed: 3 Go Functions, 2 Python Functions, 1 Bash Function.
	insertSym(t, db, "g1", "f", "Function", "Go")
	insertSym(t, db, "g2", "g", "Function", "Go")
	insertSym(t, db, "g3", "h", "Function", "Go")
	insertSym(t, db, "p1", "f", "Function", "Python")
	insertSym(t, db, "p2", "g", "Function", "Python")
	insertSym(t, db, "b1", "f", "Function", "Bash")

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (n:Function) RETURN n.language, COUNT(*) ORDER BY COUNT(*) DESC`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(r.Rows) < 2 {
		t.Fatalf("expected ≥2 rows; got %d: %v", len(r.Rows), r.Rows)
	}
	// First row must be the highest count (Go=3).
	first := r.Rows[0]
	cnt, _ := first["COUNT(*)"].(int)
	if cnt != 3 {
		t.Errorf("first row should be Go (count=3) under ORDER BY COUNT(*) DESC; got %v (count=%v): %v",
			first["n.language"], first["COUNT(*)"], r.Rows)
	}
	// Last row in this 3-row result should be Bash (count=1).
	last := r.Rows[len(r.Rows)-1]
	lastCnt, _ := last["COUNT(*)"].(int)
	if lastCnt > 2 {
		t.Errorf("last row should have the lowest count under DESC; got count=%v: %v",
			last["COUNT(*)"], r.Rows)
	}
}

// Control: ORDER BY COUNT(n.id) (non-star) — the existing case that
// already worked must continue to work after the asterisk fix.
func TestExecute_OrderByCountVarProperty_StillSorts(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "g1", "f", "Function", "Go")
	insertSym(t, db, "g2", "g", "Function", "Go")
	insertSym(t, db, "p1", "f", "Function", "Python")

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (n:Function) RETURN n.language, COUNT(n.id) ORDER BY COUNT(n.id) DESC`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(r.Rows) < 1 {
		t.Fatalf("expected rows; got %d", len(r.Rows))
	}
	first := r.Rows[0]
	if first["n.language"] != "Go" {
		t.Errorf("Go should be first under DESC; got %v: %v", first, r.Rows)
	}
}
