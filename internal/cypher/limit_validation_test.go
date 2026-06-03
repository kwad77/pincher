// SPDX-License-Identifier: MIT

package cypher

import (
	"context"
	"strings"
	"testing"
)

// #360: LIMIT 0 must return zero rows, not the default 200. Pre-fix the
// runner had `if limit <= 0 { limit = 200 }` which collapsed an explicit
// zero into the default — common SQL/Cypher idiom for "validate the
// query, no rows" was silently broken.

func TestExecute_LimitZero_ReturnsZeroRows(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "f1", "A", "Function", "Go")
	insertSym(t, db, "f2", "B", "Function", "Go")
	insertSym(t, db, "f3", "C", "Function", "Go")

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(), "MATCH (f:Function) RETURN f.name LIMIT 0")
	if err != nil {
		t.Fatalf("LIMIT 0: %v", err)
	}
	if r.Total != 0 {
		t.Errorf("LIMIT 0 should return 0 rows, got %d", r.Total)
	}
	if len(r.Rows) != 0 {
		t.Errorf("LIMIT 0 rows should be empty, got %d entries", len(r.Rows))
	}
}

// LIMIT 0 with COUNT — aggregating queries shouldn't be limited (#308),
// but explicit LIMIT 0 should still return no rows. Confirm the
// aggregating path also honors zero.
func TestExecute_LimitZero_WithCount(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "f1", "A", "Function", "Go")

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(), "MATCH (f:Function) RETURN COUNT(f) LIMIT 0")
	if err != nil {
		t.Fatalf("COUNT LIMIT 0: %v", err)
	}
	if r.Total != 0 {
		t.Errorf("COUNT with LIMIT 0 should return 0 rows, got %d", r.Total)
	}
}

// No LIMIT clause → default 200. Pre-fix this used `q.limit==200`; post-
// fix it's `q.limit==-1` and the runner translates -1 to default. Pin
// the externally-observable behavior so the change is invisible to
// callers who never specified LIMIT.
func TestExecute_NoLimitClause_UsesDefault(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	for i := 0; i < 5; i++ {
		id := string(rune('a' + i))
		insertSym(t, db, id, id, "Function", "Go")
	}

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(), "MATCH (f:Function) RETURN f.name")
	if err != nil {
		t.Fatalf("no LIMIT: %v", err)
	}
	if r.Total != 5 {
		t.Errorf("expected 5 rows, got %d", r.Total)
	}
}

// #361: query missing MATCH must error.
func TestExecute_WhereWithoutMatch_Errors(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	_, err := e.Execute(context.Background(), "WHERE n.name='A' RETURN n.name")
	if err == nil {
		t.Fatal("WHERE without MATCH should error, got nil")
	}
	if !strings.Contains(err.Error(), "MATCH") {
		t.Errorf("error should mention MATCH; got %v", err)
	}
}

// #361: typo in keywords must error rather than silently dropping them.
// Pre-fix `MATC` (typo of MATCH) was skipped via `default: p.next()` and
// the empty queryAST returned no rows.
func TestExecute_TypoKeyword_Errors(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "f1", "Foo", "Function", "Go")

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	// Both keywords typo'd — parser should see no MATCH and reject.
	_, err := e.Execute(context.Background(), "MATC (f:Function) RETRN f.name")
	if err == nil {
		t.Fatal("typo'd keywords should error, got nil")
	}
}
