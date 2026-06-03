// SPDX-License-Identifier: MIT

package cypher

import (
	"context"
	"testing"
)

// #892: `n.docstring = ""` and `n.docstring <> ""` must partition the
// corpus — the same NULL row cannot match both. Pre-fix it did:
//   `= ""`  matched NULL rows by the #606 zero-value rule.
//   `<> ""` ALSO matched NULL rows (SQL emitter wrapped in
//           `(col IS NULL OR col<>?)` and the in-Go evaluator returned
//           TRUE for NULL-vs-anything).
//
// That broke every "find missing field" audit. The fix makes `<>` the
// dual of `=` for zero-value RHS: NULL excluded. For non-zero RHS the
// inequality keeps the previous "NULL surfaces" behaviour, since
// `WHERE col <> "x"` naturally reads as "anything but x".

func TestExecute_NullDoesNotMatchInequalityWithEmpty(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	if _, err := db.Exec(`INSERT INTO symbols(id, project_id, file_path, name, qualified_name, kind, language, docstring, start_byte, end_byte, start_line, end_line) VALUES
		('a','proj1','f.go','Documented','Documented','Function','Go','docs here', 0,10,1,2),
		('b','proj1','f.go','Undoc','Undoc','Function','Go',NULL, 11,20,3,4),
		('c','proj1','f.go','Empty','Empty','Function','Go','', 21,30,5,6)`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (n:Function) WHERE n.docstring <> "" RETURN n.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.Total != 1 {
		t.Fatalf("docstring <> \"\" must match only the non-empty row; got %d: %v", r.Total, r.Rows)
	}
	if got, _ := r.Rows[0]["n.name"].(string); got != "Documented" {
		t.Errorf("expected Documented; got %q", got)
	}
}

// The two predicates must partition the corpus.
func TestExecute_NullEqualityPartitionsCorpus(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	if _, err := db.Exec(`INSERT INTO symbols(id, project_id, file_path, name, qualified_name, kind, language, docstring, start_byte, end_byte, start_line, end_line) VALUES
		('a','proj1','f.go','Documented','Documented','Function','Go','docs here', 0,10,1,2),
		('b','proj1','f.go','Undoc1','Undoc1','Function','Go',NULL, 11,20,3,4),
		('c','proj1','f.go','Undoc2','Undoc2','Function','Go',NULL, 21,30,5,6),
		('d','proj1','f.go','Empty','Empty','Function','Go','', 31,40,7,8)`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}

	eq, err := e.Execute(context.Background(),
		`MATCH (n:Function) WHERE n.docstring = "" RETURN n.name`)
	if err != nil {
		t.Fatalf("=: %v", err)
	}
	neq, err := e.Execute(context.Background(),
		`MATCH (n:Function) WHERE n.docstring <> "" RETURN n.name`)
	if err != nil {
		t.Fatalf("<>: %v", err)
	}

	// The two sets must be disjoint and together cover the 4 rows.
	eqNames := map[string]bool{}
	for _, row := range eq.Rows {
		eqNames[row["n.name"].(string)] = true
	}
	neqNames := map[string]bool{}
	for _, row := range neq.Rows {
		neqNames[row["n.name"].(string)] = true
	}
	for n := range eqNames {
		if neqNames[n] {
			t.Errorf("row %q matched BOTH = \"\" and <> \"\" — predicates do not partition", n)
		}
	}
	if eq.Total+neq.Total != 4 {
		t.Errorf("= and <> should partition all 4 rows; got eq=%d + neq=%d = %d", eq.Total, neq.Total, eq.Total+neq.Total)
	}
}

// Non-zero RHS keeps the pre-existing "NULL surfaces" behaviour — a
// query `WHERE col <> "hello"` naturally reads as "anything but
// hello", and the user expects NULL to be included.
func TestExecute_NullSurfacesOnNonZeroInequality(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	if _, err := db.Exec(`INSERT INTO symbols(id, project_id, file_path, name, qualified_name, kind, language, docstring, start_byte, end_byte, start_line, end_line) VALUES
		('a','proj1','f.go','Match','Match','Function','Go','hello', 0,10,1,2),
		('b','proj1','f.go','Other','Other','Function','Go','world', 11,20,3,4),
		('c','proj1','f.go','Null','Null','Function','Go',NULL, 21,30,5,6)`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (n:Function) WHERE n.docstring <> "hello" RETURN n.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.Total != 2 {
		t.Fatalf("docstring <> \"hello\" should match the 2 non-hello rows including the NULL; got %d: %v", r.Total, r.Rows)
	}
	names := map[string]bool{}
	for _, row := range r.Rows {
		names[row["n.name"].(string)] = true
	}
	if names["Match"] {
		t.Errorf("the hello row must not match <> \"hello\"")
	}
	if !names["Other"] || !names["Null"] {
		t.Errorf("both Other (world) and Null rows must match; got %v", names)
	}
}

// Bool inequality dual: `is_test <> false` should match only is_test=1
// rows, NOT NULL rows. Symmetric to the bool-zero rule in #606.
func TestExecute_BoolNotEqualsFalse_ExcludesNull(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	if _, err := db.Exec(`INSERT INTO symbols(id, project_id, file_path, name, qualified_name, kind, language, is_test, start_byte, end_byte, start_line, end_line) VALUES
		('a','proj1','f.go','Prod','Prod','Function','Go',0, 0,10,1,2),
		('b','proj1','f.go','Unset','Unset','Function','Go',NULL, 11,20,3,4),
		('c','proj1','f_test.go','TestX','TestX','Function','Go',1, 21,30,5,6)`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (n:Function) WHERE n.is_test <> false RETURN n.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.Total != 1 {
		t.Fatalf("is_test <> false should match only the test row; got %d: %v", r.Total, r.Rows)
	}
	if got, _ := r.Rows[0]["n.name"].(string); got != "TestX" {
		t.Errorf("expected TestX; got %q", got)
	}
}
