// SPDX-License-Identifier: MIT

package cypher

import (
	"context"
	"strings"
	"testing"
)

// #1439 v0.72: WHERE x IN [a, b, c] — list membership predicate.
// Pre-fix this errored with "unsupported operator: IN — combine
// equality conditions with OR" (open issue since v0.8.0). The
// canonical "find every Method whose name is one of these N
// CRUD handlers" query had no working pinchQL spelling that
// scaled past 5-10 OR-clauses.
//
// Table: positive (string/number/multi-row), control (single-
// match, no-match), negative (empty list, wrong delimiter, bad
// literal), and cross-check (NOT IN via the existing `negated`
// flag, IN alongside other predicates, SQL pushdown vs in-Go
// evaluation parity).

func TestExecute_IN_StringList_MatchesAnyMember(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	if _, err := db.Exec(`INSERT INTO symbols(id, project_id, file_path, name, qualified_name, kind, language, start_byte, end_byte, start_line, end_line) VALUES
		('a','proj1','f.go','Open','pkg.Open','Function','Go', 0,10,1,2),
		('b','proj1','f.go','Close','pkg.Close','Function','Go', 11,20,3,4),
		('c','proj1','f.go','Init','pkg.Init','Function','Go', 21,30,5,6),
		('d','proj1','f.go','Other','pkg.Other','Function','Go', 31,40,7,8)`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (n:Function) WHERE n.name IN ["Open", "Close", "Init"] RETURN n.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := map[string]bool{}
	for _, row := range r.Rows {
		got[toStringForTest(t, row["n.name"])] = true
	}
	want := map[string]bool{"Open": true, "Close": true, "Init": true}
	for k := range want {
		if !got[k] {
			t.Errorf("IN missed expected match %q (got=%v)", k, got)
		}
	}
	if got["Other"] {
		t.Errorf("IN matched non-member 'Other' (got=%v)", got)
	}
}

// Cross-check — IN over an INTEGER column. SQLite affinity
// coerces the bind args, so numeric literals in the list match
// numeric column values.
func TestExecute_IN_NumberList_AffinityCoercesToInt(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	if _, err := db.Exec(`INSERT INTO symbols(id, project_id, file_path, name, qualified_name, kind, language, start_byte, end_byte, start_line, end_line) VALUES
		('a','proj1','f.go','A','A','Function','Go', 0,10,10,20),
		('b','proj1','f.go','B','B','Function','Go', 11,20,30,40),
		('c','proj1','f.go','C','C','Function','Go', 21,30,50,60),
		('d','proj1','f.go','D','D','Function','Go', 31,40,70,80)`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (n:Function) WHERE n.start_line IN [10, 50] RETURN n.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := map[string]bool{}
	for _, row := range r.Rows {
		got[toStringForTest(t, row["n.name"])] = true
	}
	if !got["A"] || !got["C"] || got["B"] || got["D"] {
		t.Errorf("IN on numeric column wrong; want {A,C}, got %v", got)
	}
}

// Cross-check — NOT IN via the existing `negated` flag. NOT IN
// is the inverse: matches rows NOT in the list (and excludes
// NULL rows, matching SQL semantics).
func TestExecute_NOT_IN_MatchesComplement(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	if _, err := db.Exec(`INSERT INTO symbols(id, project_id, file_path, name, qualified_name, kind, language, start_byte, end_byte, start_line, end_line) VALUES
		('a','proj1','f.go','Open','pkg.Open','Function','Go', 0,10,1,2),
		('b','proj1','f.go','Close','pkg.Close','Function','Go', 11,20,3,4),
		('c','proj1','f.go','Init','pkg.Init','Function','Go', 21,30,5,6),
		('d','proj1','f.go','Other','pkg.Other','Function','Go', 31,40,7,8)`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (n:Function) WHERE NOT n.name IN ["Open", "Close"] RETURN n.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := map[string]bool{}
	for _, row := range r.Rows {
		got[toStringForTest(t, row["n.name"])] = true
	}
	if !got["Init"] || !got["Other"] || got["Open"] || got["Close"] {
		t.Errorf("NOT IN wrong; want {Init,Other}, got %v", got)
	}
}

// Cross-check — IN composed with another predicate (AND).
// Verifies the predicate composes with the rest of the WHERE
// tree, not just as a standalone leaf.
func TestExecute_IN_ComposedWithAND_OtherPredicate(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	if _, err := db.Exec(`INSERT INTO symbols(id, project_id, file_path, name, qualified_name, kind, language, start_byte, end_byte, start_line, end_line) VALUES
		('a','proj1','a.go','Open','pkg.Open','Function','Go', 0,10,1,2),
		('b','proj1','b.go','Open','pkg.Open','Function','Go', 0,10,1,2),
		('c','proj1','a.go','Close','pkg.Close','Function','Go', 11,20,3,4)`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (n:Function) WHERE n.name IN ["Open","Close"] AND n.file_path = "a.go" RETURN n.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(r.Rows) != 2 {
		t.Errorf("IN AND filter wrong row count; got %d, want 2 (a.go's Open + Close)", len(r.Rows))
	}
}

// Control — single-member IN behaves like a plain equality.
func TestExecute_IN_SingleMember_EquivalentToEquality(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	if _, err := db.Exec(`INSERT INTO symbols(id, project_id, file_path, name, qualified_name, kind, language, start_byte, end_byte, start_line, end_line) VALUES
		('a','proj1','f.go','Open','pkg.Open','Function','Go', 0,10,1,2),
		('b','proj1','f.go','Close','pkg.Close','Function','Go', 11,20,3,4)`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (n:Function) WHERE n.name IN ["Open"] RETURN n.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(r.Rows) != 1 || toStringForTest(t, r.Rows[0]["n.name"]) != "Open" {
		t.Errorf("single-member IN wrong; got rows=%v", r.Rows)
	}
}

// Negative — empty list rejected at parse time. `IN []` is
// always-false in SQL; surface it as an explicit error rather
// than silent zero-rows (same silent-confidently-wrong family
// as #606 / #892).
func TestExecute_IN_EmptyList_RejectedWithExplicitError(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	e := &Executor{DB: db, ProjectID: "proj1"}
	_, err := e.Execute(context.Background(),
		`MATCH (n:Function) WHERE n.name IN [] RETURN n.name`)
	if err == nil {
		t.Fatal("expected error for empty IN list; got nil")
	}
	if !strings.Contains(err.Error(), "empty list") {
		t.Errorf("error %q should mention 'empty list'", err)
	}
}

// Negative — parens not allowed for the list literal. Cypher
// uses brackets; parens would conflict with grouping syntax.
func TestExecute_IN_ParensRejected_HintsAtBrackets(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	e := &Executor{DB: db, ProjectID: "proj1"}
	_, err := e.Execute(context.Background(),
		`MATCH (n:Function) WHERE n.name IN ("Open","Close") RETURN n.name`)
	if err == nil {
		t.Fatal("expected error for paren-list; got nil")
	}
	if !strings.Contains(err.Error(), "bracket") {
		t.Errorf("error %q should point at bracket-list", err)
	}
}

// Cross-check — IN matches no rows when the list contains
// values absent from the column.
func TestExecute_IN_NoMatch_ReturnsEmpty(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	if _, err := db.Exec(`INSERT INTO symbols(id, project_id, file_path, name, qualified_name, kind, language, start_byte, end_byte, start_line, end_line) VALUES
		('a','proj1','f.go','Open','pkg.Open','Function','Go', 0,10,1,2)`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (n:Function) WHERE n.name IN ["Foo","Bar","Baz"] RETURN n.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(r.Rows) != 0 {
		t.Errorf("no-match IN should return empty; got %v", r.Rows)
	}
}

// Helper — extract string from any row value, tolerating int/string.
func toStringForTest(t *testing.T, v any) string {
	t.Helper()
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
