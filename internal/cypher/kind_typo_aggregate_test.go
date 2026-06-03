// SPDX-License-Identifier: MIT

package cypher

import (
	"context"
	"testing"
)

// #1139: pre-fix, `MATCH (n:Funtion) RETURN COUNT(*)` returned
// `{COUNT(*): 0}` silently with no warning. The Total==0 outer gate
// for collectUnknownEnumValueWarnings checked res.Total (the response
// row count), not the user-perspective "zero outcome." Aggregate
// queries always return ≥1 row (the COUNT/SUM/AVG result), so the
// pattern-label probe was skipped for them. Now: pattern labels are
// probed unconditionally via a dedicated helper, AND the WHERE-value
// probe gates on isEffectivelyZero (aggregate-value-of-zero counts).

// Positive: typo'd pattern label in aggregate query warns.
func TestExecute_TypoKindInAggregateQuery_Warns(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "f1", "main", "Function", "Go")

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (n:Funtion) RETURN COUNT(*)`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !hasWarning(r.Warnings, "Funtion", "kind") {
		t.Errorf("expected typo'd-kind warning naming Funtion + kind; got %v", r.Warnings)
	}
	if !hasWarning(r.Warnings, "Function") {
		t.Errorf("warning should list the actual kind Function as the likely intent; got %v", r.Warnings)
	}
}

// Positive: typo'd pattern label in non-aggregate empty query also
// warns (preserves #501 path).
func TestExecute_TypoKindInRowQuery_StillWarns(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "f1", "main", "Function", "Go")

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (n:Funtion) RETURN n.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !hasWarning(r.Warnings, "Funtion") {
		t.Errorf("expected typo'd-kind warning; got %v", r.Warnings)
	}
}

// Control: valid pattern label in aggregate — no warning.
func TestExecute_ValidKindInAggregateQuery_NoWarning(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "f1", "main", "Function", "Go")

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (n:Function) RETURN COUNT(*)`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, w := range r.Warnings {
		if hasWarning([]string{w}, "is not a kind") {
			t.Errorf("valid kind must not trip the typo warning; got: %v", r.Warnings)
		}
	}
}

// Control: typo'd kind on edge variable does not poison node-side
// probe — the edge has its own probe path (#1132).
func TestExecute_AggregateZeroAggregateValue_TripsWhereValueProbe(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "f1", "main", "Function", "Go")

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (n) WHERE n.kind = "BOGUS_KIND" RETURN COUNT(*)`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// COUNT(*) returned 0 → isEffectivelyZero is true → WHERE-value
	// probe still fires.
	if !hasWarning(r.Warnings, "BOGUS_KIND") {
		t.Errorf("aggregate-zero WHERE-value should still warn; got %v", r.Warnings)
	}
}
