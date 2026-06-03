// SPDX-License-Identifier: MIT

package cypher

import (
	"context"
	"strings"
	"testing"
)

// #1117: pinchQL is read-only, but pre-fix CREATE / DELETE / SET /
// MERGE / REMOVE got the generic "unexpected token — expected a
// clause keyword" error pointing at WHERE/RETURN/ORDER BY/LIMIT.
// Agents reading that error tried to fix syntax instead of learning
// pinchQL is read-only. Now: a specific message names the read-only
// contract and points at `index force=true` for actual mutations.

func TestExecute_CreateKeyword_RejectedAsWriteOp(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	_, err := e.Execute(context.Background(),
		`CREATE (n:Function {name: "test"}) RETURN n`)
	if err == nil {
		t.Fatal("CREATE must be rejected")
	}
	msg := err.Error()
	if !strings.Contains(msg, "read-only") {
		t.Errorf("error must explain pinchQL is read-only; got %q", msg)
	}
	if !strings.Contains(msg, "CREATE") {
		t.Errorf("error must name the offending keyword; got %q", msg)
	}
	if !strings.Contains(msg, "index") {
		t.Errorf("error should point at the index tool as the actual write path; got %q", msg)
	}
}

func TestExecute_DeleteKeyword_RejectedAsWriteOp(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "a", "A", "Function", "Go")

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	_, err := e.Execute(context.Background(),
		`MATCH (n) DELETE n RETURN n`)
	if err == nil {
		t.Fatal("DELETE must be rejected")
	}
	if !strings.Contains(err.Error(), "read-only") {
		t.Errorf("DELETE error must mention read-only contract; got %q", err)
	}
}

func TestExecute_SetKeyword_RejectedAsWriteOp(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "a", "A", "Function", "Go")

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	_, err := e.Execute(context.Background(),
		`MATCH (n) SET n.name = "new" RETURN n`)
	if err == nil {
		t.Fatal("SET must be rejected")
	}
	if !strings.Contains(err.Error(), "read-only") {
		t.Errorf("SET error must mention read-only contract; got %q", err)
	}
}

func TestExecute_MergeKeyword_RejectedAsWriteOp(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	_, err := e.Execute(context.Background(),
		`MERGE (n:Function {name: "test"}) RETURN n`)
	if err == nil {
		t.Fatal("MERGE must be rejected")
	}
	if !strings.Contains(err.Error(), "read-only") {
		t.Errorf("MERGE error must mention read-only contract; got %q", err)
	}
}

// Control: typo'd clause keywords still get the existing did-you-mean
// path, not the write-op path.
func TestExecute_TypoClauseKeyword_GetsDidYouMean_NotWriteOp(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "a", "A", "Function", "Go")

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	_, err := e.Execute(context.Background(),
		`MATCH (n) WERE n.name = "x" RETURN n.name`)
	if err == nil {
		t.Fatal("typo WERE must be rejected")
	}
	msg := err.Error()
	if strings.Contains(msg, "read-only") {
		t.Errorf("typo'd keyword should NOT hit write-op path; got %q", msg)
	}
	if !strings.Contains(msg, "WHERE") && !strings.Contains(msg, "did you mean") {
		t.Errorf("typo'd keyword should hit did-you-mean path; got %q", msg)
	}
}
