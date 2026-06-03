// SPDX-License-Identifier: MIT

package cypher

import (
	"context"
	"testing"
)

// #1135: `RETURN name` (no `n.` prefix) rendered as `{name: {}}` — an
// empty object under a column name matching a real property. The
// parser stored variable="name", property="", and buildResult
// projected the unbound variable as an empty map. Same silent-
// confidently-wrong family as #1116 (unbound WHERE variable), worse
// because the column name itself looks like data.

// Positive: bare property name without var prefix → warning.
func TestExecute_ReturnBareProperty_Warns(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "f1", "main", "Function", "Go")

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (n:Function) RETURN name LIMIT 1`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !hasWarning(r.Warnings, "RETURN name", "no var prefix") {
		t.Errorf("expected warning naming the bare property + missing prefix; got %v", r.Warnings)
	}
	// With exactly one bound variable, the remediation should suggest
	// the qualified form directly.
	if !hasWarning(r.Warnings, "n.name") {
		t.Errorf("warning should suggest `n.name`; got %v", r.Warnings)
	}
}

// Positive: with multiple bound variables, warning lists them.
func TestExecute_ReturnBareProperty_TwoBoundVars_ListsBoth(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "f1", "main", "Function", "Go")
	insertSym(t, db, "f2", "helper", "Function", "Go")
	insertEdge(t, db, "f1", "f2", "CALLS")

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (a:Function)-[:CALLS]->(b:Function) RETURN name LIMIT 1`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !hasWarning(r.Warnings, "RETURN name", "no var prefix") {
		t.Errorf("expected warning; got %v", r.Warnings)
	}
	// Remediation should list both bound variables.
	if !hasWarning(r.Warnings, "a") || !hasWarning(r.Warnings, "b") {
		t.Errorf("warning should list bound variables a, b; got %v", r.Warnings)
	}
}

// Control: qualified RETURN (n.name) — no warning.
func TestExecute_ReturnQualified_NoWarning(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "f1", "main", "Function", "Go")

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (n:Function) RETURN n.name LIMIT 1`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, w := range r.Warnings {
		if hasWarning([]string{w}, "no var prefix") {
			t.Errorf("qualified RETURN should not trip #1135 warning; got: %v", r.Warnings)
		}
	}
}

// Control: bare bound-variable return (RETURN n) — legitimate
// "return whole node" form, no warning.
func TestExecute_ReturnBareBoundVar_NoWarning(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "f1", "main", "Function", "Go")

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (n:Function) RETURN n LIMIT 1`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, w := range r.Warnings {
		if hasWarning([]string{w}, "no var prefix") {
			t.Errorf("RETURN n (bound) is the canonical 'whole node' form — must not trip the warning; got: %v", r.Warnings)
		}
	}
}

// Control: bare name that ISN'T a valid property — leave it to the
// existing unbound-variable warning to handle.
func TestExecute_ReturnBareUnknownName_NotTrippedByThisWarning(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "f1", "main", "Function", "Go")

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (n:Function) RETURN totally_unknown_thing LIMIT 1`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// The #1135 warning is scoped to bare names that happen to match
	// a property — that's the silent-empty-object trap. A bare
	// "totally_unknown_thing" is a different shape and the existing
	// unbound-variable warning may catch it; this test just confirms
	// #1135 doesn't fire for it.
	for _, w := range r.Warnings {
		if hasWarning([]string{w}, "no var prefix") {
			t.Errorf("non-property bare name should not trip #1135 (let the unbound-var warning catch it); got: %v", r.Warnings)
		}
	}
}
