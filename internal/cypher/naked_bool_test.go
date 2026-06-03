// SPDX-License-Identifier: MIT

package cypher

import (
	"context"
	"strings"
	"testing"
)

// #431: naked boolean column reference (`WHERE n.is_exported`) used
// to return a cryptic `unsupported operator: )`. Now it's a
// first-class shorthand for `= true` (Cypher/Memgraph parity), and
// for non-bool columns the error explicitly lists the operators the
// caller should choose from.

func TestExecute_NakedBoolPredicate_IsExported(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSymWithExported(t, db, "exp", "Apple", "Function", true)
	insertSymWithExported(t, db, "unexp", "banana", "Function", false)

	r := exec(t, db, `MATCH (n:Function) WHERE n.is_exported RETURN n.name`)
	if r.Total != 1 {
		t.Fatalf("WHERE n.is_exported should match exported rows only, got %d", r.Total)
	}
	if r.Rows[0]["n.name"] != "Apple" {
		t.Errorf("expected Apple, got %v", r.Rows[0]["n.name"])
	}
}

func TestExecute_NakedBoolPredicate_InsideAndOr(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSymWithExported(t, db, "exp1", "Apple", "Function", true)
	insertSymWithExported(t, db, "exp2", "Cherry", "Function", true)
	insertSymWithExported(t, db, "unexp", "banana", "Function", false)

	r := exec(t, db, `MATCH (n:Function) WHERE n.kind="Function" AND n.is_exported RETURN n.name`)
	if r.Total != 2 {
		t.Fatalf("Function AND is_exported should match 2 rows, got %d", r.Total)
	}
}

func TestExecute_NakedNonBoolPredicate_GivesHelpfulError(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "f1", "Apple", "Function", "Go")

	// `n.name` isn't bool — naked reference should error with the
	// operator-list message, not the cryptic `unsupported operator: )`.
	exe := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	_, err := exe.Execute(context.Background(), `MATCH (n:Function) WHERE (n.name) RETURN n.name`)
	if err == nil {
		t.Fatal("expected error for naked non-bool column, got nil")
	}
	if !strings.Contains(err.Error(), "needs an operator") {
		t.Errorf("expected helpful 'needs an operator' message, got: %v", err)
	}
	if strings.Contains(err.Error(), "unsupported operator: )") {
		t.Errorf("error still cryptic — got: %v", err)
	}
}
