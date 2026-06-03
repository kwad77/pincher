// SPDX-License-Identifier: MIT

package cypher

import (
	"context"
	"strings"
	"testing"
)

// #889: a WHERE comparison that crosses literal type — `n.start_line =
// "twenty"` or `n.name = 12345` — silently returns 0 rows. SQLite's
// type affinity coerces the literal, and the result is a confidently-
// wrong "nothing matches" for what is actually a malformed query.
// Same silent-confidently-wrong family as #473 (typo'd property), #867
// (unknown edge kind), #881 (unknown ORDER BY column).

func TestExecute_StringLiteralOnIntColumn_SurfacesWarning(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "a", "A", "Function", "Go")

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (n:Function) WHERE n.start_line = "twenty" RETURN n.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !hasWarning(r.Warnings, "start_line", "string") {
		t.Errorf("expected type-mismatch warning naming start_line + string; got: %v", r.Warnings)
	}
}

func TestExecute_NumberLiteralOnTextColumn_SurfacesWarning(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "a", "A", "Function", "Go")

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (n:Function) WHERE n.name = 12345 RETURN n.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !hasWarning(r.Warnings, "name", "number") {
		t.Errorf("expected type-mismatch warning naming name + number; got: %v", r.Warnings)
	}
}

func TestExecute_StringLiteralOnRealColumn_SurfacesWarning(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "a", "A", "Function", "Go")

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (n:Function) WHERE n.extraction_confidence = "high" RETURN n.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !hasWarning(r.Warnings, "extraction_confidence", "string") {
		t.Errorf("expected type-mismatch warning naming extraction_confidence + string; got: %v", r.Warnings)
	}
}

func TestExecute_StringLiteralWithComparisonOp_SurfacesWarning(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "a", "A", "Function", "Go")

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (n:Function) WHERE n.start_line > "twenty" RETURN n.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !hasWarning(r.Warnings, "start_line", "string") {
		t.Errorf("expected type-mismatch warning for > op; got: %v", r.Warnings)
	}
}

// Control: a well-typed predicate produces no type-mismatch warning.
func TestExecute_WellTypedPredicate_NoTypeMismatchWarning(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "a", "A", "Function", "Go")

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (n:Function) WHERE n.name = "A" AND n.start_line > 0 RETURN n.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, w := range r.Warnings {
		if strings.Contains(strings.ToLower(w), "type affinity") {
			t.Errorf("well-typed query must not produce a type-mismatch warning; got: %v", w)
		}
	}
}

// Boolean columns accept the TRUE/FALSE keywords (normalised to "1"/"0")
// and bare 0/1 numerics — neither path should warn.
func TestExecute_BoolColumnTrueKeyword_NoWarning(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "a", "A", "Function", "Go")

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (n:Function) WHERE n.is_exported = true RETURN n.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, w := range r.Warnings {
		if strings.Contains(strings.ToLower(w), "type affinity") {
			t.Errorf("bool = true must not warn; got: %v", w)
		}
	}
}

func TestExecute_BoolColumnNumericLiteral_NoWarning(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "a", "A", "Function", "Go")

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (n:Function) WHERE n.is_exported = 1 RETURN n.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, w := range r.Warnings {
		if strings.Contains(strings.ToLower(w), "type affinity") {
			t.Errorf("bool = 1 must not warn; got: %v", w)
		}
	}
}

// A bool column compared to a clearly-wrong NUMBER (42) or STRING
// ("yes") is a typo and should warn.
func TestExecute_BoolColumnGarbageNumber_SurfacesWarning(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "a", "A", "Function", "Go")

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (n:Function) WHERE n.is_exported = 42 RETURN n.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !hasWarning(r.Warnings, "is_exported", "number") {
		t.Errorf("bool = 42 should warn; got: %v", r.Warnings)
	}
}

func TestExecute_BoolColumnGarbageString_SurfacesWarning(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "a", "A", "Function", "Go")

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (n:Function) WHERE n.is_exported = "yes" RETURN n.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !hasWarning(r.Warnings, "is_exported", "string") {
		t.Errorf("bool = \"yes\" should warn; got: %v", r.Warnings)
	}
}

// Unit test for the property→type classifier.
func TestCypherPropType(t *testing.T) {
	cases := []struct{ prop, want string }{
		{"name", "text"},
		{"qualified_name", "text"},
		{"file_path", "text"},
		{"kind", "text"},
		{"language", "text"},
		{"signature", "text"},
		{"return_type", "text"},
		{"docstring", "text"},
		{"id", "text"},
		{"project_id", "text"},
		{"start_line", "int"},
		{"end_line", "int"},
		{"start_byte", "int"},
		{"end_byte", "int"},
		{"complexity", "int"},
		{"extraction_confidence", "real"},
		{"confidence", "real"},
		{"is_exported", "bool"},
		{"is_entry_point", "bool"},
		{"is_test", "bool"},
		{"totally_unknown", ""},
	}
	for _, c := range cases {
		got := cypherPropType(c.prop)
		if got != c.want {
			t.Errorf("cypherPropType(%q) = %q, want %q", c.prop, got, c.want)
		}
	}
}

// hasWarning returns true when any warning string contains all the
// given fragments (case-insensitive on the fragments).
func hasWarning(warnings []string, fragments ...string) bool {
	for _, w := range warnings {
		lw := strings.ToLower(w)
		matched := true
		for _, f := range fragments {
			if !strings.Contains(lw, strings.ToLower(f)) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}
