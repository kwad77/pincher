// SPDX-License-Identifier: MIT

package server

import (
	"strings"
	"testing"
)

// #924: natural threshold/audit phrasings that #912 missed because they
// drop the "every|all|any" article ("find functions longer than 100
// lines") or use a standalone audit adjective ("find untested exported
// functions"). The latter was being silently routed to shapeTest by
// the bare "test" substring check; the former fell to shapeFind and
// got recommended as a BM25 search of the literal phrase.

func TestClassifyTaskShape_AuditLooseThreshold(t *testing.T) {
	t.Parallel()
	cases := []string{
		"find functions longer than 100 lines",
		"list functions longer than 50 lines",
		"surface methods bigger than 200 lines",
		"show classes deeper than 5 levels",
		"find handlers slower than 100ms",
		"find handlers faster than the median",
	}
	for _, task := range cases {
		t.Run(task, func(t *testing.T) {
			if got := classifyTaskShape(task); got != shapeAudit {
				t.Errorf("classifyTaskShape(%q) = %v, want shapeAudit", task, got)
			}
		})
	}
}

func TestClassifyTaskShape_AuditAdjective(t *testing.T) {
	t.Parallel()
	cases := []string{
		"find untested exported functions",
		"list untested methods",
		"surface undocumented public APIs",
		"find uncovered handlers",
		"show me untyped exports",
		"list unhandled errors",
		"find unauthenticated endpoints",
	}
	for _, task := range cases {
		t.Run(task, func(t *testing.T) {
			if got := classifyTaskShape(task); got != shapeAudit {
				t.Errorf("classifyTaskShape(%q) = %v, want shapeAudit", task, got)
			}
		})
	}
}

// Regression guards: existing routes should still claim their tasks.
// "unused" stays with shapeDeadCode (more specific); plain "test" /
// "coverage" without an audit adjective stays with shapeTest.
func TestClassifyTaskShape_AuditAdjectiveDoesNotStealNonAudits(t *testing.T) {
	t.Parallel()
	cases := map[string]guideShape{
		"find unused functions":                 shapeDeadCode,
		"write tests for the new handler":       shapeTest,
		"add test coverage for the auth module": shapeTest,
	}
	for task, want := range cases {
		t.Run(task, func(t *testing.T) {
			if got := classifyTaskShape(task); got != want {
				t.Errorf("classifyTaskShape(%q) = %v, want %v", task, got, want)
			}
		})
	}
}

// End-to-end: a loose-threshold task gets a complexity / lines query,
// an adjective task gets the right narrowed template, and none of
// them get the docstring fallback by accident.
func TestGuideRecommendations_LooseThresholdGetsLinesQuery(t *testing.T) {
	t.Parallel()
	recs := guideRecommendations(shapeAudit, "longer than 100 lines", "",
		"find functions longer than 100 lines")
	if len(recs) == 0 {
		t.Fatal("audit shape should produce at least one recommendation")
	}
	first := recs[0]
	if first["tool"] != "query" {
		t.Errorf("first tool = %q, want query", first["tool"])
	}
	// #928: pinchQL doesn't yet support arithmetic in WHERE/RETURN.
	// Until then, the line-count template returns start_line +
	// end_line for client-side diff computation rather than emitting
	// a query the engine can't parse.
	if !strings.Contains(first["args"], "n.start_line") || !strings.Contains(first["args"], "n.end_line") {
		t.Errorf("line-count audit must project start_line + end_line; got %q", first["args"])
	}
	// Must NOT emit the broken arithmetic form pre-#928.
	if strings.Contains(first["args"], "end_line - n.start_line") || strings.Contains(first["args"], "(n.end_line-n.start_line)") {
		t.Errorf("line-count audit must NOT emit arithmetic until #928 lands; got %q", first["args"])
	}
}

// Regression guard: every audit template inferAuditPinchQL emits must
// be parseable by the cypher engine. Pre-#928 the line-count template
// emitted `(n.end_line - n.start_line) > 100` which crashes with
// "cypher parse: unsupported operator: -". This test pins the
// "templates must round-trip through the parser" invariant so a
// future change can't re-introduce engine-incompatible templates.
func TestInferAuditPinchQL_AllTemplatesParseable(t *testing.T) {
	t.Parallel()
	// Sample tasks covering every branch of inferAuditPinchQL.
	tasks := []string{
		"find every function with cyclomatic complexity above 20",
		"find functions longer than 100 lines",
		"find untested exported functions",
		"find undocumented exported APIs",
		// #1759: the unrecognized-predicate default branch.
		"find every function that writes to the database",
	}
	for _, task := range tasks {
		t.Run(task, func(t *testing.T) {
			pinchql, _ := inferAuditPinchQL(task)
			// We don't have a public parse-only entry point on the
			// engine, so the cheapest check is: ensure no recognised
			// unsupported-operator pattern slipped in.
			for _, bad := range []string{
				" - ", "(n.end_line-n.start_line)", "n.start_line-n.end_line",
				" + ", " * ", " / ",
			} {
				if strings.Contains(pinchql, bad) {
					t.Errorf("template for %q contains arithmetic %q which pinchQL doesn't yet support (#928); got %q", task, bad, pinchql)
				}
			}
		})
	}
}

func TestGuideRecommendations_AdjectiveUntestedGetsCoverageQuery(t *testing.T) {
	t.Parallel()
	recs := guideRecommendations(shapeAudit, "untested exported functions", "",
		"find untested exported functions")
	if len(recs) == 0 {
		t.Fatal("audit shape should produce at least one recommendation")
	}
	first := recs[0]
	if first["tool"] != "query" {
		t.Errorf("first tool = %q, want query", first["tool"])
	}
	args := first["args"]
	if !strings.Contains(args, "is_exported=true AND n.is_test=false") {
		t.Errorf("untested task must emit non-test exported query; got %q", args)
	}
	// #923: must be scoped to Go to avoid regex-tier noise.
	if !strings.Contains(args, `n.language='Go'`) {
		t.Errorf("untested task must scope to Go; got %q", args)
	}
}

// #923: the docstring template scopes to Go + non-test so it doesn't
// recommend a query that returns JS/Bash regex-tier false positives or
// test functions. #1759: the docstring query is now keyed on an
// explicit docstring/undocumented/comment keyword — `audit exported
// APIs` (no such keyword) is a different, unrecognized-predicate case.
func TestInferAuditPinchQL_DocstringTemplateExcludesTestsAndNonGo(t *testing.T) {
	t.Parallel()
	pinchql, _ := inferAuditPinchQL("audit undocumented exported APIs")
	if !strings.Contains(pinchql, "docstring IS NULL") {
		t.Fatalf("an undocumented-API task should emit the docstring template; got %q", pinchql)
	}
	if !strings.Contains(pinchql, "n.is_test=false") {
		t.Errorf("docstring template must exclude test functions; got %q", pinchql)
	}
	if !strings.Contains(pinchql, `n.language='Go'`) {
		t.Errorf("docstring template must scope to Go; got %q", pinchql)
	}
}

// #1759: an audit-shape task whose predicate guide cannot keyword-match
// ("writes to the DB", "lacks auth checks") must NOT get a docstring
// query — it gets the neutral candidate set, and the `why` must say
// guide could not compile the predicate so the agent doesn't trust the
// query as the literal answer.
func TestInferAuditPinchQL_UnrecognizedPredicate_HonestScaffold(t *testing.T) {
	t.Parallel()
	for _, task := range []string{
		"find every function that writes to the database without going through the reader pool",
		"find every HTTP handler that lacks authentication checks",
	} {
		pinchql, why := inferAuditPinchQL(task)
		if strings.Contains(pinchql, "docstring") {
			t.Errorf("task %q must not get a docstring query; got %q", task, pinchql)
		}
		if !strings.Contains(pinchql, "is_exported=true AND n.is_test=false") {
			t.Errorf("task %q should emit the neutral candidate set; got %q", task, pinchql)
		}
		if !strings.Contains(why, "can't compile") && !strings.Contains(why, "filter the results manually") {
			t.Errorf("task %q `why` must signal guide couldn't map the predicate; got %q", task, why)
		}
	}
}
