// SPDX-License-Identifier: MIT

package server

import (
	"strings"
	"testing"
)

// #951: classifyTaskShape's audit detector missed "over N" / "more
// than N" / "at least N" — the most common audit phrasings in code
// review. The existing auditThresholdPattern required "with/having/
// whose" scaffolding; auditLooseThresholdPattern required a
// comparative adjective ("longer/bigger than"). "find functions over
// 100 lines" matched neither and fell through to shapeFind, which
// recommended BM25 search on the literal words.

func TestClassifyTaskShape_BareThresholdOverN(t *testing.T) {
	cases := []struct {
		task string
		want guideShape
	}{
		// Headline case from #951.
		{"find all functions over 100 lines", shapeAudit},
		{"list functions over 200 lines", shapeAudit},
		{"show methods over 150 lines", shapeAudit},
		{"count functions under 5 lines", shapeAudit},
		// "more than N units" — same intent, different preposition.
		{"find functions more than 100 lines", shapeAudit},
		{"list functions less than 5 lines", shapeAudit},
		// "at least" / "at most".
		{"find functions at least 200 lines", shapeAudit},
		{"list functions at most 10 lines", shapeAudit},
		// Operator-shaped variants.
		{"find functions > 100 lines", shapeAudit},
		{"list functions >= 100 lines", shapeAudit},
	}
	for _, c := range cases {
		got := classifyTaskShape(c.task)
		if got != c.want {
			t.Errorf("classifyTaskShape(%q) = %v, want %v", c.task, got, c.want)
		}
	}
}

// Anti-test: prose "over there" without a digit must NOT route to
// shapeAudit. The digit-anchor in auditBareThresholdPattern is the
// guard; if it ever weakens, this test surfaces the regression.
func TestClassifyTaskShape_BareThreshold_NoDigitDoesNotMatchAudit(t *testing.T) {
	cases := []string{
		"look over there in main.go",
		"find functions over here",
		"list methods above me",
	}
	for _, task := range cases {
		got := classifyTaskShape(task)
		if got == shapeAudit {
			t.Errorf("classifyTaskShape(%q) = shapeAudit; expected non-audit (no digit anchors a threshold)", task)
		}
	}
}

// Pin the routing: now that shapeAudit fires, inferAuditPinchQL must
// pick the line-count template (the one #921 shipped + #928 worked
// around the missing arithmetic for).
func TestInferAuditPinchQL_OverNLines_RoutesToLineCountTemplate(t *testing.T) {
	pql, _ := inferAuditPinchQL("find all functions over 100 lines")
	if pql == "" {
		t.Fatal("inferAuditPinchQL returned empty pinchQL")
	}
	// Line-count template projects start_line + end_line (the #928
	// client-side-diff workaround). The docstring template doesn't
	// return either.
	if !strings.Contains(pql, "start_line") || !strings.Contains(pql, "end_line") {
		t.Errorf("expected line-count template (returns start_line + end_line); got %q", pql)
	}
	if strings.Contains(pql, "docstring IS NULL") {
		t.Errorf("routed to docstring fallback, not line-count template; got %q", pql)
	}
}
