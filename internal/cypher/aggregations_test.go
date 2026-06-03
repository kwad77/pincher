// SPDX-License-Identifier: MIT

package cypher

import (
	"context"
	"database/sql"
	"testing"
)

// #432: avg / min / max / sum aggregations used to return 200 NULL
// rows because parseReturn only knew about COUNT — other function
// names were parsed as raw column references that resolved to nil
// per row. They now compute correctly.

func TestExecute_AVG_SingleRowAggregate(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSymWithComplexity(t, db, "f1", "A", 10)
	insertSymWithComplexity(t, db, "f2", "B", 20)
	insertSymWithComplexity(t, db, "f3", "C", 30)

	r := exec(t, db, `MATCH (n:Function) RETURN avg(n.complexity)`)
	if r.Total != 1 {
		t.Fatalf("avg should produce 1 row, got %d", r.Total)
	}
	got, _ := r.Rows[0]["AVG(n.complexity)"].(float64)
	if got != 20.0 {
		t.Errorf("avg = %v, want 20.0", got)
	}
}

func TestExecute_MIN_MAX_Combined(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSymWithComplexity(t, db, "f1", "A", 5)
	insertSymWithComplexity(t, db, "f2", "B", 50)
	insertSymWithComplexity(t, db, "f3", "C", 25)

	r := exec(t, db, `MATCH (n:Function) RETURN min(n.complexity), max(n.complexity)`)
	if r.Total != 1 {
		t.Fatalf("min+max should produce 1 row, got %d", r.Total)
	}
	row := r.Rows[0]
	if row["MIN(n.complexity)"].(float64) != 5 {
		t.Errorf("min = %v, want 5", row["MIN(n.complexity)"])
	}
	if row["MAX(n.complexity)"].(float64) != 50 {
		t.Errorf("max = %v, want 50", row["MAX(n.complexity)"])
	}
}

func TestExecute_SUM(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSymWithComplexity(t, db, "f1", "A", 5)
	insertSymWithComplexity(t, db, "f2", "B", 7)
	insertSymWithComplexity(t, db, "f3", "C", 8)

	r := exec(t, db, `MATCH (n:Function) RETURN sum(n.complexity)`)
	if r.Total != 1 {
		t.Fatalf("sum should produce 1 row, got %d", r.Total)
	}
	if r.Rows[0]["SUM(n.complexity)"].(float64) != 20 {
		t.Errorf("sum = %v, want 20", r.Rows[0]["SUM(n.complexity)"])
	}
}

// AVG with GROUP BY: RETURN n.kind, avg(n.complexity) groups by kind
// and emits one avg per kind. Mirrors the COUNT-with-GROUP-BY (#348)
// behaviour for the new aggregators.
func TestExecute_AVG_GroupedByKind(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSymWithComplexity(t, db, "f1", "A", 10)
	insertSymWithComplexity(t, db, "f2", "B", 20)
	insertSymMethodWithComplexity(t, db, "m1", "C", 30)
	insertSymMethodWithComplexity(t, db, "m2", "D", 50)

	r := exec(t, db, `MATCH (n) RETURN n.kind, avg(n.complexity)`)
	if r.Total != 2 {
		t.Fatalf("expected 2 groups (Function, Method), got %d", r.Total)
	}
	byKind := map[string]float64{}
	for _, row := range r.Rows {
		k := row["n.kind"].(string)
		byKind[k] = row["AVG(n.complexity)"].(float64)
	}
	if byKind["Function"] != 15 {
		t.Errorf("Function avg = %v, want 15", byKind["Function"])
	}
	if byKind["Method"] != 40 {
		t.Errorf("Method avg = %v, want 40", byKind["Method"])
	}
}

func TestExecute_AVG_EmptySet_NULL(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	// No matching rows — avg should return NULL.
	r := exec(t, db, `MATCH (n:Function) WHERE n.name="DoesNotExist" RETURN avg(n.complexity)`)
	if r.Total != 1 {
		t.Fatalf("agg over empty set still produces 1 row")
	}
	if r.Rows[0]["AVG(n.complexity)"] != nil {
		t.Errorf("avg over empty = %v, want nil", r.Rows[0]["AVG(n.complexity)"])
	}
}

func TestExecute_AVG_Aliased(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSymWithComplexity(t, db, "f1", "A", 10)
	insertSymWithComplexity(t, db, "f2", "B", 20)

	r := exec(t, db, `MATCH (n:Function) RETURN avg(n.complexity) AS mean`)
	if r.Rows[0]["mean"].(float64) != 15 {
		t.Errorf("aliased mean = %v, want 15", r.Rows[0]["mean"])
	}
}

func insertSymWithComplexity(t *testing.T, db *sql.DB, id, name string, complexity int) {
	t.Helper()
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO symbols(id, project_id, file_path, name, qualified_name, kind, language,
			start_byte, end_byte, start_line, end_line, complexity) VALUES (?,?,?,?,?,?,?,0,100,1,5,?)`,
		id, "proj1", "f.go", name, name, "Function", "Go", complexity,
	)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
}

func insertSymMethodWithComplexity(t *testing.T, db *sql.DB, id, name string, complexity int) {
	t.Helper()
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO symbols(id, project_id, file_path, name, qualified_name, kind, language,
			start_byte, end_byte, start_line, end_line, complexity) VALUES (?,?,?,?,?,?,?,0,100,1,5,?)`,
		id, "proj1", "f.go", name, name, "Method", "Go", complexity,
	)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
}
