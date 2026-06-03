// SPDX-License-Identifier: MIT

package cypher

import (
	"context"
	"testing"
)

// #885: CONTAINS / STARTS WITH / ENDS WITH push down to SQL LIKE. Pre-
// fix the user-supplied literal was wrapped with `%` and bound directly
// — so `CONTAINS "%"` compiled to `LIKE '%%%'` and matched every row,
// because the user's `%` got interpreted as a wildcard. Cypher specifies
// CONTAINS as literal substring match; the divergence was both
// semantically wrong and silent-confidently-wrong.

func TestExecute_ContainsLiteralPercent_DoesNotWildcardMatch(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	// Two functions, neither has a `%` in its name.
	insertSym(t, db, "a", "alpha", "Function", "Go")
	insertSym(t, db, "b", "beta", "Function", "Go")
	// One that actually contains a literal `%` so the positive case is testable.
	insertSym(t, db, "c", "weird%name", "Function", "Go")

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (n:Function) WHERE n.name CONTAINS "%" RETURN n.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.Total != 1 {
		t.Fatalf("CONTAINS \"%%\" must match only the literal-%% function; got %d rows: %v", r.Total, r.Rows)
	}
	got, _ := r.Rows[0]["n.name"].(string)
	if got != "weird%name" {
		t.Errorf("expected the row containing literal %%; got %q", got)
	}
}

// `_` is also a LIKE wildcard (single character). Same treatment.
func TestExecute_ContainsLiteralUnderscore_DoesNotWildcardMatch(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "a", "alpha", "Function", "Go")
	insertSym(t, db, "b", "beta_v2", "Function", "Go")

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (n:Function) WHERE n.name CONTAINS "_v2" RETURN n.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.Total != 1 {
		t.Fatalf("CONTAINS \"_v2\" should match only beta_v2 (literal underscore), not 'al_pha'-style wildcard hits; got %d: %v",
			r.Total, r.Rows)
	}
}

// Sanity: plain non-special-char substring match still works.
func TestExecute_ContainsPlainSubstring_StillMatches(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "a", "ProcessOrder", "Function", "Go")
	insertSym(t, db, "b", "CancelOrder", "Function", "Go")
	insertSym(t, db, "c", "Whatever", "Function", "Go")

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (n:Function) WHERE n.name CONTAINS "Order" RETURN n.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.Total != 2 {
		t.Errorf("CONTAINS \"Order\" should match the 2 *Order rows; got %d: %v", r.Total, r.Rows)
	}
}

// Pure-function unit test for the escape helper.
func TestEscapeLikePattern(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain", "plain"},
		{"50%", `50\%`},
		{"a_b", `a\_b`},
		{`back\slash`, `back\\slash`},
		{`%_\`, `\%\_\\`},
		{"", ""},
	}
	for _, c := range cases {
		got := escapeLikePattern(c.in)
		if got != c.want {
			t.Errorf("escapeLikePattern(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
