// SPDX-License-Identifier: MIT

package cypher

import (
	"strings"
	"testing"
)

// #294: when an agent reaches for a SQL/Cypher operator that pinchQL
// doesn't support, the parse error should nudge them toward the supported
// spelling instead of just saying "unsupported operator".
func TestParse_UnsupportedOperator_LIKE_Hint(t *testing.T) {
	tokens := tokenize("MATCH (n:Function) WHERE n.name LIKE '%handle%' RETURN n.name")
	p := &parser{tokens: tokens}
	_, err := p.parseQuery()
	if err == nil {
		t.Fatal("expected parse error for LIKE")
	}
	msg := err.Error()
	if !strings.Contains(msg, "LIKE") {
		t.Errorf("error %q must mention the offending operator", msg)
	}
	if !strings.Contains(msg, "CONTAINS") {
		t.Errorf("error %q must suggest CONTAINS", msg)
	}
}

func TestParse_UnsupportedOperator_StartsWithUnderscore_Hint(t *testing.T) {
	tokens := tokenize("MATCH (n:Function) WHERE n.name STARTS_WITH 'handle' RETURN n.name")
	p := &parser{tokens: tokens}
	_, err := p.parseQuery()
	if err == nil {
		t.Fatal("expected parse error for STARTS_WITH")
	}
	msg := err.Error()
	if !strings.Contains(msg, "STARTS WITH") {
		t.Errorf("error %q must suggest STARTS WITH (no underscore)", msg)
	}
}

// #1406: the underscore-form ENDS_WITH (what most Cypher tutorials
// and adapters spell) must get the same redirect hint as its
// STARTS_WITH sibling. Pre-fix users hit a bare "unsupported operator:
// ENDS_WITH" with no recovery affordance — the canonical ENDS WITH
// (two words, #340) was a first-class operator but the hint had been
// removed thinking the underscore form was dead.
func TestParse_UnsupportedOperator_EndsWithUnderscore_Hint(t *testing.T) {
	tokens := tokenize("MATCH (n:Function) WHERE n.name ENDS_WITH 'Advisory' RETURN n.name")
	p := &parser{tokens: tokens}
	_, err := p.parseQuery()
	if err == nil {
		t.Fatal("expected parse error for ENDS_WITH")
	}
	msg := err.Error()
	if !strings.Contains(msg, "ENDS WITH") {
		t.Errorf("error %q must suggest ENDS WITH (no underscore)", msg)
	}
}

func TestParse_UnsupportedOperator_NotEquals_Hint(t *testing.T) {
	// `!=` is two punct tokens (`!` then `=`); the parser must still
	// produce a hint pointing at the supported `<>`.
	tokens := tokenize("MATCH (n:Function) WHERE n.name != 'foo' RETURN n.name")
	p := &parser{tokens: tokens}
	_, err := p.parseQuery()
	if err == nil {
		t.Fatal("expected parse error for !=")
	}
	msg := err.Error()
	if !strings.Contains(msg, "<>") {
		t.Errorf("error %q must suggest <>", msg)
	}
}

func TestParse_UnsupportedOperator_REGEXP_Hint(t *testing.T) {
	tokens := tokenize("MATCH (n:Function) WHERE n.name REGEXP '.*foo.*' RETURN n.name")
	p := &parser{tokens: tokens}
	_, err := p.parseQuery()
	if err == nil {
		t.Fatal("expected parse error for REGEXP")
	}
	if !strings.Contains(err.Error(), "=~") {
		t.Errorf("error %q must suggest =~", err.Error())
	}
}

// Negative — supported operators must still parse.
func TestParse_SupportedOperators_StillWork(t *testing.T) {
	cases := []string{
		"MATCH (n) WHERE n.name = 'foo' RETURN n.name",
		"MATCH (n) WHERE n.name <> 'foo' RETURN n.name",
		"MATCH (n) WHERE n.name CONTAINS 'foo' RETURN n.name",
		"MATCH (n) WHERE n.name STARTS WITH 'foo' RETURN n.name",
		"MATCH (n) WHERE n.name =~ '.*foo.*' RETURN n.name",
		"MATCH (n) WHERE n.start_line >= 100 RETURN n.name",
	}
	for _, q := range cases {
		t.Run(q, func(t *testing.T) {
			p := &parser{tokens: tokenize(q)}
			if _, err := p.parseQuery(); err != nil {
				t.Errorf("supported operator query failed: %v", err)
			}
		})
	}
}

// operatorHint table coverage — ensure every entry returns a non-empty
// hint. Mostly a guard against typos in future additions to the map.
func TestOperatorHint_AllEntriesNonEmpty(t *testing.T) {
	// ENDS WITH (two words) is first-class (#340); ENDS_WITH (underscore
	// typo form) still needs the redirect hint per #1406. IN moved
	// off the hint table when it became first-class (#1439) — it's
	// handled by parseOneCondition directly now.
	for _, op := range []string{"LIKE", "like", "REGEXP", "RLIKE", "STARTS_WITH", "ENDS_WITH", "MATCHES"} {
		hint, ok := operatorHint(op)
		if !ok || hint == "" {
			t.Errorf("operatorHint(%q) returned empty/false", op)
		}
	}
	if _, ok := operatorHint("BOGUS"); ok {
		t.Error("operatorHint(BOGUS) should return false")
	}
	// IN is no longer in the operatorHint table per #1439 — the
	// parser handles it as a first-class case. Confirm it doesn't
	// surface a stale hint that points at the old OR workaround.
	if hint, ok := operatorHint("IN"); ok {
		t.Errorf("operatorHint(\"IN\") should not return a hint after #1439 — got %q", hint)
	}
}

// #1439: IN is now first-class. The old "unsupported, use OR" hint
// was removed; this test pins the new shape — `WHERE n.kind IN
// ['Function','Method']` parses cleanly and the condition has the
// expected op and inValues.
func TestParse_INClause_FirstClass(t *testing.T) {
	tokens := tokenize(`MATCH (n) WHERE n.kind IN ["Function","Method"] RETURN n.name`)
	p := &parser{tokens: tokens}
	q, err := p.parseQuery()
	if err != nil {
		t.Fatalf("IN should parse cleanly post-#1439; got: %v", err)
	}
	// Walk the WHERE tree to find the IN leaf. The query has one
	// condition; parseWhereExpr wraps it as a leaf condExpr.
	leaf, ok := q.where.(condExpr)
	if !ok {
		t.Fatalf("expected condition leaf at top of WHERE; got %T", q.where)
	}
	if leaf.c.op != "IN" {
		t.Errorf("op = %q; want IN", leaf.c.op)
	}
	if len(leaf.c.inValues) != 2 {
		t.Fatalf("inValues = %v; want 2 entries", leaf.c.inValues)
	}
	if leaf.c.inValues[0] != "Function" || leaf.c.inValues[1] != "Method" {
		t.Errorf("inValues = %v; want [Function Method]", leaf.c.inValues)
	}
}
