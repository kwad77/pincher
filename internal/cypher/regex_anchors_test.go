// SPDX-License-Identifier: MIT

package cypher

import "testing"

// #435: regex anchors `^` and `$` silently broke =~ matches that
// worked without anchors. Reproduce in unit tests so we can pin
// the cause and the fix.
func TestExecute_RegexAnchors_Caret(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "f1", "TestFooDrift", "Function", "Go")
	insertSym(t, db, "f2", "OtherTestDrift", "Function", "Go")
	insertSym(t, db, "f3", "Helper", "Function", "Go")

	r := exec(t, db, `MATCH (n:Function) WHERE n.name =~ "^Test.*Drift" RETURN n.name`)
	if r.Total != 1 {
		t.Fatalf("^Test.*Drift should only match TestFooDrift, got %d rows: %v", r.Total, r.Rows)
	}
	if r.Total > 0 && r.Rows[0]["n.name"] != "TestFooDrift" {
		t.Errorf("expected TestFooDrift, got %v", r.Rows[0]["n.name"])
	}
}

func TestExecute_RegexAnchors_Dollar(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "f1", "TestFooDrift", "Function", "Go")
	insertSym(t, db, "f2", "TestFooDriftPart2", "Function", "Go")

	r := exec(t, db, `MATCH (n:Function) WHERE n.name =~ "Drift$" RETURN n.name`)
	if r.Total != 1 {
		t.Fatalf("Drift$ should only match TestFooDrift (no trailing chars), got %d rows: %v", r.Total, r.Rows)
	}
}

func TestExecute_RegexAnchors_Both(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "f1", "TestFooDrift", "Function", "Go")
	insertSym(t, db, "f2", "TestFooDriftPart2", "Function", "Go")
	insertSym(t, db, "f3", "BeforeTestFooDrift", "Function", "Go")

	r := exec(t, db, `MATCH (n:Function) WHERE n.name =~ "^Test.*Drift$" RETURN n.name`)
	if r.Total != 1 {
		t.Fatalf("^Test.*Drift$ should only match TestFooDrift, got %d rows: %v", r.Total, r.Rows)
	}
}

// #435 actual root cause: anchored regex returned 0 because =~ doesn't
// SQL-push, so the in-Go regex post-filtered AFTER the SQL LIMIT
// clamp (`maxRows()*2 = 400`). On a corpus where matching rows sit
// past row 400, the result was 0 even though the regex was correct.
// Same family as #430 (OR pushdown) and #434 (comparison pushdown).
// Fix: scale the LIMIT clamp when in-Go filtering is required.
func TestExecute_RegexAnchors_PastLimitClamp(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	// 250 noise rows whose names DON'T match the anchor pattern.
	for i := 0; i < 250; i++ {
		insertSym(t, db, "noise"+padInt(i), "OtherName", "Function", "Go")
	}
	// Match rows late in the table (high IDs / late insertion order).
	insertSym(t, db, "zz_match1", "TestAlphaDrift", "Function", "Go")
	insertSym(t, db, "zz_match2", "TestBetaDrift", "Function", "Go")

	r := exec(t, db, `MATCH (n:Function) WHERE n.name =~ "^Test.*Drift$" RETURN n.name`)
	if r.Total != 2 {
		t.Fatalf("^Test.*Drift$ should match the 2 late rows, got %d (pre-fix this returned 0 because the regex evaluator never saw them past the LIMIT clamp)", r.Total)
	}
}
