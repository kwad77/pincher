// SPDX-License-Identifier: MIT

package cypher

import (
	"context"
	"strings"
	"testing"
)

// #1437 v0.72: COUNT(DISTINCT n.prop) was rejected at parse time
// with a misleading "unbalanced delimiter" error. The canonical
// "how many unique callers does X have" query had no working
// pinchQL spelling — users had to do RETURN DISTINCT then count
// client-side, which doesn't work over hub functions whose
// distinct-set exceeds the row budget.
//
// Cypher restricts DISTINCT to COUNT (SUM/AVG/MIN/MAX over
// duplicates are subtle and Cypher excludes them); pinchQL
// follows the same restriction.
//
// Table: positive (dedup works), negative (non-COUNT rejected),
// control (plain COUNT still works), cross-check (COUNT(DISTINCT *)
// and bare-variable forms surface specific errors instead of
// silently miscounting).

func TestExecute_CountDistinctProperty_DeduplicatesByValue(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	// 5 functions across 3 distinct files — COUNT(DISTINCT file_path)
	// must return 3, not 5.
	if _, err := db.Exec(`INSERT INTO symbols(id, project_id, file_path, name, qualified_name, kind, language, start_byte, end_byte, start_line, end_line) VALUES
		('a','proj1','a.go','A','A','Function','Go', 0,10,1,2),
		('b','proj1','a.go','B','B','Function','Go', 11,20,3,4),
		('c','proj1','b.go','C','C','Function','Go', 21,30,5,6),
		('d','proj1','b.go','D','D','Function','Go', 31,40,7,8),
		('e','proj1','c.go','E','E','Function','Go', 41,50,9,10)`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (n:Function) RETURN COUNT(DISTINCT n.file_path) AS files, COUNT(n) AS total`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(r.Rows) != 1 {
		t.Fatalf("expected single aggregate row; got %d", len(r.Rows))
	}
	row := r.Rows[0]
	files := toIntForTest(t, row["files"])
	total := toIntForTest(t, row["total"])
	if files != 3 {
		t.Errorf("COUNT(DISTINCT n.file_path) over 3 unique files: got %d; want 3", files)
	}
	if total != 5 {
		t.Errorf("COUNT(n) sanity: got %d; want 5", total)
	}
}

// Cross-check — NULL values are excluded from the distinct set
// (matching SQL semantics).
func TestExecute_CountDistinctProperty_ExcludesNulls(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	if _, err := db.Exec(`INSERT INTO symbols(id, project_id, file_path, name, qualified_name, kind, language, docstring, start_byte, end_byte, start_line, end_line) VALUES
		('a','proj1','f.go','A','A','Function','Go','x',  0,10,1,2),
		('b','proj1','f.go','B','B','Function','Go','y',  11,20,3,4),
		('c','proj1','f.go','C','C','Function','Go','x',  21,30,5,6),
		('d','proj1','f.go','D','D','Function','Go',NULL, 31,40,7,8),
		('e','proj1','f.go','E','E','Function','Go',NULL, 41,50,9,10)`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (n:Function) RETURN COUNT(DISTINCT n.docstring) AS unique_docs`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := toIntForTest(t, r.Rows[0]["unique_docs"])
	// 2 unique non-null values ("x", "y") — NULLs excluded.
	if got != 2 {
		t.Errorf("COUNT(DISTINCT n.docstring) over {x,y,x,NULL,NULL}: got %d; want 2 (NULLs excluded)", got)
	}
}

// Control — plain COUNT(n.prop) still counts non-null rows (no
// regression on #906's count-non-null behaviour).
func TestExecute_CountWithoutDistinct_StillCountsAllNonNullRows(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	if _, err := db.Exec(`INSERT INTO symbols(id, project_id, file_path, name, qualified_name, kind, language, start_byte, end_byte, start_line, end_line) VALUES
		('a','proj1','same.go','A','A','Function','Go', 0,10,1,2),
		('b','proj1','same.go','B','B','Function','Go', 11,20,3,4),
		('c','proj1','same.go','C','C','Function','Go', 21,30,5,6)`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (n:Function) RETURN COUNT(n.file_path) AS c`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := toIntForTest(t, r.Rows[0]["c"]); got != 3 {
		t.Errorf("plain COUNT(n.file_path) over 3 rows (all same value, no NULLs): got %d; want 3", got)
	}
}

// Negative — DISTINCT inside non-COUNT aggregates surfaces a
// specific error, not silent acceptance with wrong result.
func TestExecute_DistinctInsideNonCountAggregator_RejectedAtParse(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	cases := []struct {
		query, wantSubstr string
	}{
		{`MATCH (n:Function) RETURN SUM(DISTINCT n.complexity)`, "DISTINCT inside SUM"},
		{`MATCH (n:Function) RETURN AVG(DISTINCT n.complexity)`, "DISTINCT inside AVG"},
		{`MATCH (n:Function) RETURN MIN(DISTINCT n.complexity)`, "DISTINCT inside MIN"},
		{`MATCH (n:Function) RETURN MAX(DISTINCT n.complexity)`, "DISTINCT inside MAX"},
	}
	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	for _, c := range cases {
		_, err := e.Execute(context.Background(), c.query)
		if err == nil {
			t.Errorf("expected parse error for %q; got nil", c.query)
			continue
		}
		if !strings.Contains(err.Error(), c.wantSubstr) {
			t.Errorf("error for %q missing %q: %v", c.query, c.wantSubstr, err)
		}
	}
}

// Cross-check — COUNT(DISTINCT *) and COUNT(DISTINCT n) (bare
// variable) both surface specific errors pointing at the property
// shape, rather than silently returning row count.
func TestExecute_CountDistinct_MeaninglessShapes_RejectedAtParse(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	cases := []struct {
		query, wantSubstr string
	}{
		{`MATCH (n:Function) RETURN COUNT(DISTINCT *)`, "COUNT(DISTINCT *) is not supported"},
		{`MATCH (n:Function) RETURN COUNT(DISTINCT n)`, "without a property is not supported"},
	}
	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	for _, c := range cases {
		_, err := e.Execute(context.Background(), c.query)
		if err == nil {
			t.Errorf("expected parse error for %q; got nil", c.query)
			continue
		}
		if !strings.Contains(err.Error(), c.wantSubstr) {
			t.Errorf("error for %q missing %q: %v", c.query, c.wantSubstr, err)
		}
	}
}

// Cross-check — the existing operatorHint message for DISTINCT-
// in-aggregate inside WHERE still fires (no regression on the
// WHERE-context surface).
func TestExecute_CountDistinct_InWhere_StillRejectedAsBefore(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	_, err := e.Execute(context.Background(),
		`MATCH (n:Function) WHERE COUNT(DISTINCT n.name) > 0 RETURN n.name`)
	if err == nil {
		t.Fatal("WHERE with aggregator must still be rejected")
	}
}
