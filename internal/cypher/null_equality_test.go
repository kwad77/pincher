// SPDX-License-Identifier: MIT

package cypher

import (
	"context"
	"testing"
)

// #606: pinchQL `col=""` and `col=false` predicates must match NULL
// rows. SQL standard tri-state logic returns false for NULL=anything,
// but the user-facing semantics are "where this property is absent or
// empty". Pre-#606 the canonical "find undocumented APIs" query
// returned 0 rows on a corpus that obviously has undocumented
// functions.
//
// Same UX class as #473 (typo'd properties), #578 (unknown function
// names), #591 (multi-sourced edges silently inflate), and #593
// (cross-column predicates silently always-true) — pinchQL silently
// returns wrong / empty answers for predicates that look natural
// but don't behave the SQL way.

func TestExecute_NullEqualsEmpty_StringColumn(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	// Three Go functions: one with a docstring, two without.
	// Insert directly so the docstring column is NULL on the
	// second/third (vs explicit empty string).
	if _, err := db.Exec(`INSERT INTO symbols(id, project_id, file_path, name, qualified_name, kind, language, docstring, start_byte, end_byte, start_line, end_line) VALUES
		('a','proj1','f.go','Documented','Documented','Function','Go','It does the thing.', 0,10,1,2),
		('b','proj1','f.go','UndocOne','UndocOne','Function','Go',NULL, 11,20,3,4),
		('c','proj1','f.go','UndocTwo','UndocTwo','Function','Go',NULL, 21,30,5,6)`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(), `MATCH (n:Function) WHERE n.docstring="" RETURN n.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.Total != 2 {
		t.Errorf("docstring=\"\" should match the 2 NULL-docstring rows; got %d rows: %v", r.Total, r.Rows)
	}
}

func TestExecute_NullEqualsEmpty_DoesNotMatchNonEmpty(t *testing.T) {
	// Sanity: when the user writes a literal value (not ""), NULL
	// rows must NOT match — preserving the natural reading of
	// `WHERE col="hello"`. Prevents over-broad matching from the
	// #606 fix.
	db := newTestDB(t)
	defer db.Close()

	if _, err := db.Exec(`INSERT INTO symbols(id, project_id, file_path, name, qualified_name, kind, language, docstring, start_byte, end_byte, start_line, end_line) VALUES
		('a','proj1','f.go','A','A','Function','Go','hello', 0,10,1,2),
		('b','proj1','f.go','B','B','Function','Go','world', 11,20,3,4),
		('c','proj1','f.go','C','C','Function','Go',NULL, 21,30,5,6)`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(), `MATCH (n:Function) WHERE n.docstring="hello" RETURN n.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.Total != 1 {
		t.Errorf("docstring=\"hello\" should match 1 row only (NULL must NOT match); got %d: %v", r.Total, r.Rows)
	}
}

func TestExecute_NullEqualsFalse_BoolColumn(t *testing.T) {
	// `is_test=false` should match rows where is_test is NULL OR 0.
	// Pre-#606 NULL rows were excluded — meaning "exclude tests"
	// silently dropped any row whose is_test column hadn't been
	// populated by the extractor.
	db := newTestDB(t)
	defer db.Close()

	if _, err := db.Exec(`INSERT INTO symbols(id, project_id, file_path, name, qualified_name, kind, language, is_test, start_byte, end_byte, start_line, end_line) VALUES
		('a','proj1','f.go','Prod1','Prod1','Function','Go',0, 0,10,1,2),
		('b','proj1','f.go','Prod2','Prod2','Function','Go',NULL, 11,20,3,4),
		('c','proj1','f_test.go','TestX','TestX','Function','Go',1, 21,30,5,6)`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(), `MATCH (n:Function) WHERE n.is_test=false RETURN n.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.Total != 2 {
		t.Errorf("is_test=false should match the 2 non-test rows (one explicit 0, one NULL); got %d: %v", r.Total, r.Rows)
	}
	// The Test* row must NOT slip in.
	for _, row := range r.Rows {
		if row["n.name"] == "TestX" {
			t.Errorf("is_test=false should exclude TestX (is_test=1); got: %v", row)
		}
	}
}

// Canonical "find undocumented APIs" query — the demo from #438 that
// motivated #606 the fix. Combines is_exported predicate, docstring=""
// (NULL match), and is_test=false (NULL match). Pre-#606 returned 0;
// post-#606 returns the genuinely-undocumented exported non-test rows.
func TestExecute_CanonicalUndocumentedAPIQuery(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	if _, err := db.Exec(`INSERT INTO symbols(id, project_id, file_path, name, qualified_name, kind, language, is_exported, is_test, docstring, start_byte, end_byte, start_line, end_line) VALUES
		('a','proj1','f.go','PublicDocumented','PublicDocumented','Function','Go',1,0,'good docs', 0,10,1,2),
		('b','proj1','f.go','PublicUndocumented1','PublicUndocumented1','Function','Go',1,0,NULL, 11,20,3,4),
		('c','proj1','f.go','PublicUndocumented2','PublicUndocumented2','Function','Go',1,NULL,NULL, 21,30,5,6),
		('d','proj1','f.go','privateUndocumented','privateUndocumented','Function','Go',0,0,NULL, 31,40,7,8),
		('e','proj1','f_test.go','TestSomething','TestSomething','Function','Go',1,1,NULL, 41,50,9,10)`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (n:Function) WHERE n.is_exported=true AND n.docstring="" AND n.is_test=false RETURN n.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// Expected: the 2 PublicUndocumented* rows. NOT the documented one,
	// NOT the private one, NOT the test one.
	if r.Total != 2 {
		t.Errorf("undocumented-exported-non-test query should match 2; got %d: %v", r.Total, r.Rows)
	}
	names := map[string]bool{}
	for _, row := range r.Rows {
		names[row["n.name"].(string)] = true
	}
	if !names["PublicUndocumented1"] || !names["PublicUndocumented2"] {
		t.Errorf("expected both PublicUndocumented* rows; got %v", names)
	}
	if names["PublicDocumented"] || names["privateUndocumented"] || names["TestSomething"] {
		t.Errorf("query matched something it shouldn't; got %v", names)
	}
}
