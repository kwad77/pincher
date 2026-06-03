// SPDX-License-Identifier: MIT

package cypher

import "testing"

// #362: WHERE supports parenthesized sub-expressions. Pre-fix the parser
// flattened conditions left-to-right with no operator precedence, so the
// only way to express `A AND (B OR C)` was a paren — and parens errored
// with "unsupported operator: f". Post-fix the WHERE clause is parsed as
// a recursive-descent tree (whereExpr) and parens are first-class.

// TestExecute_Parens_IssueRepro is the exact body of #362: a left-side
// OR group ANDed with a single condition. Without parens this is
// impossible to express in pinchQL since composition is left-to-right
// (`A OR B AND C` evaluates as `(A OR B) AND C` only by coincidence
// when the desired groupings happen to align).
func TestExecute_Parens_IssueRepro(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "f1", "Alpha", "Function", "Go")
	insertSym(t, db, "f2", "Beta", "Function", "Go")
	insertSym(t, db, "f3", "Alpha", "Method", "Go")
	insertSym(t, db, "f4", "Gamma", "Function", "Go")

	r := exec(t, db, "MATCH (f) WHERE (f.name='Alpha' OR f.name='Beta') AND f.kind='Function' RETURN f.name, f.kind")
	if r.Total != 2 {
		t.Fatalf("expected 2 rows for (Alpha OR Beta) AND Function, got %d: %+v", r.Total, r.Rows)
	}
	got := map[string]bool{}
	for _, row := range r.Rows {
		got[row["f.name"].(string)+"/"+row["f.kind"].(string)] = true
	}
	if !got["Alpha/Function"] || !got["Beta/Function"] {
		t.Errorf("expected Alpha/Function + Beta/Function, got %v", got)
	}
	if got["Alpha/Method"] || got["Gamma/Function"] {
		t.Errorf("unexpected rows in result: %v", got)
	}
}

// Right-side OR group — `A AND (B OR C)`. Distinct from the issue
// repro because the group sits on the right of the AND; the parser's
// left-leaning tree flattens differently and the tree-eval path must
// not collapse the right child to a leaf.
func TestExecute_Parens_RightSideOrGroup(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "f1", "Alpha", "Function", "Go")
	insertSym(t, db, "f2", "Alpha", "Method", "Go")
	insertSym(t, db, "f3", "Alpha", "Class", "Go")
	insertSym(t, db, "f4", "Beta", "Function", "Go")

	r := exec(t, db, "MATCH (f) WHERE f.name='Alpha' AND (f.kind='Function' OR f.kind='Method') RETURN f.name, f.kind")
	if r.Total != 2 {
		t.Fatalf("expected 2 rows for Alpha AND (Function OR Method), got %d", r.Total)
	}
	for _, row := range r.Rows {
		if row["f.name"] != "Alpha" {
			t.Errorf("unexpected name %v", row["f.name"])
		}
		k := row["f.kind"].(string)
		if k != "Function" && k != "Method" {
			t.Errorf("unexpected kind %q", k)
		}
	}
}

// Nested parens — `((A OR B) AND C) OR D`. Sanity-check the recursive
// descent doesn't confuse depth.
func TestExecute_Parens_Nested(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "f1", "Alpha", "Function", "Go")    // matches inner (Alpha) AND Function
	insertSym(t, db, "f2", "Beta", "Function", "Go")     // matches inner (Beta) AND Function
	insertSym(t, db, "f3", "Alpha", "Method", "Go")      // inner matches but kind!=Function
	insertSym(t, db, "f4", "Gamma", "Method", "Go")      // matches outer-OR Gamma branch
	insertSym(t, db, "f5", "Delta", "Class", "Go")       // matches nothing

	r := exec(t, db, "MATCH (f) WHERE ((f.name='Alpha' OR f.name='Beta') AND f.kind='Function') OR f.name='Gamma' RETURN f.name")
	if r.Total != 3 {
		t.Fatalf("expected 3 rows from nested expression, got %d: %+v", r.Total, r.Rows)
	}
	got := map[string]bool{}
	for _, row := range r.Rows {
		got[row["f.name"].(string)] = true
	}
	for _, want := range []string{"Alpha", "Beta", "Gamma"} {
		if !got[want] {
			t.Errorf("expected %s in result, missing from %v", want, got)
		}
	}
	if got["Delta"] {
		t.Errorf("Delta must not appear: %v", got)
	}
}

// Group NOT — `NOT (A OR B)`. Pre-#362 the leaf-NOT path (#354) only
// supported a single condition; `NOT (...)` errored. The new parseFactor
// distinguishes leaf-NOT (sets condition.negated) from group-NOT
// (wraps in notExpr). Both paths share the inversion semantics.
func TestExecute_Parens_NotGroup(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "f1", "Alpha", "Function", "Go")
	insertSym(t, db, "f2", "Beta", "Function", "Go")
	insertSym(t, db, "f3", "Gamma", "Function", "Go")

	r := exec(t, db, "MATCH (f:Function) WHERE NOT (f.name='Alpha' OR f.name='Beta') RETURN f.name")
	if r.Total != 1 {
		t.Fatalf("expected 1 row (only Gamma) from NOT (Alpha OR Beta), got %d: %+v", r.Total, r.Rows)
	}
	if r.Rows[0]["f.name"] != "Gamma" {
		t.Errorf("expected Gamma, got %v", r.Rows[0]["f.name"])
	}
}

// Trivial single-condition parens — `(A)` parses identically to `A`.
// The flattenWhere path collapses the group since the inner is a leaf.
func TestExecute_Parens_TrivialSingle(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "f1", "Alpha", "Function", "Go")
	insertSym(t, db, "f2", "Beta", "Function", "Go")

	r := exec(t, db, "MATCH (f) WHERE (f.name='Alpha') RETURN f.name")
	if r.Total != 1 || r.Rows[0]["f.name"] != "Alpha" {
		t.Fatalf("expected single Alpha row, got %+v", r.Rows)
	}
}

// Pure-AND-chain queries with parens around individual leaves should
// still benefit from SQL pushdown: pushdownAllowed treats the
// parser-collapsed tree as flat. This is a perf/regression check —
// `(name='X') AND (kind='Function')` is the same shape post-collapse
// as `name='X' AND kind='Function'`.
func TestExecute_Parens_AndChainStillFlat(t *testing.T) {
	q, err := parse("MATCH (f) WHERE (f.name='X') AND (f.kind='Function') RETURN f.name")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !pushdownAllowed(q) {
		t.Errorf("pushdownAllowed should be true for AND-chain even with leaf-parens")
	}
	if len(q.conditions) != 2 {
		t.Errorf("expected q.conditions to flatten to 2 leaves, got %d", len(q.conditions))
	}
}

// Once parens introduce non-flat shape, pushdown disables and
// q.conditions is empty (callers must use q.where).
func TestParseWhere_NonFlatTreeDisablesPushdown(t *testing.T) {
	q, err := parse("MATCH (f) WHERE f.name='X' AND (f.kind='Function' OR f.kind='Method') RETURN f.name")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if pushdownAllowed(q) {
		t.Error("pushdownAllowed should be false when WHERE contains a non-flat OR group")
	}
	if q.where == nil {
		t.Fatal("q.where must be non-nil after WHERE parsing")
	}
	if len(q.conditions) != 0 {
		t.Errorf("q.conditions should be empty for non-flat tree; got %d entries", len(q.conditions))
	}
}

// Group NOT triggers the same non-flat / no-pushdown path.
func TestParseWhere_NotGroupDisablesPushdown(t *testing.T) {
	q, err := parse("MATCH (f) WHERE NOT (f.name='X' OR f.name='Y') RETURN f.name")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if pushdownAllowed(q) {
		t.Error("pushdownAllowed should be false when WHERE contains NOT-group")
	}
	if _, ok := q.where.(notExpr); !ok {
		t.Errorf("expected q.where to be notExpr, got %T", q.where)
	}
}

// Unbalanced parens surface as a parse error rather than swallowing
// the rest of the query (the legacy code only complained about
// "unsupported operator: f" once the variable token slot was consumed).
func TestParseWhere_UnbalancedParenError(t *testing.T) {
	_, err := parse("MATCH (f) WHERE (f.name='X' AND f.kind='Function' RETURN f.name")
	if err == nil {
		t.Fatal("expected parse error for missing ')', got nil")
	}
}
