// SPDX-License-Identifier: MIT

package server

import (
	"strings"
	"testing"
)

// #943: pre-fix the docstring + untested audit templates filtered
// MATCH (n:Function), so exported Methods (Go's method-heavy idiom)
// slipped through unaudited. On pincher's own MCP server with its
// many handleX methods, this was hiding the majority of the coverage
// gap. Templates now match Function OR Method via n.kind WHERE clause.

func TestInferAuditPinchQL_DocstringTemplate_IncludesMethods(t *testing.T) {
	pql, _ := inferAuditPinchQL("find undocumented exported functions")
	if !strings.Contains(pql, "Function") || !strings.Contains(pql, "Method") {
		t.Errorf("docstring template must include both Function and Method; got %q", pql)
	}
	if strings.Contains(pql, "(n:Function)") {
		t.Errorf("docstring template still uses Function-only label match; got %q", pql)
	}
	// Project n.kind so the result distinguishes methods from functions.
	if !strings.Contains(pql, "n.kind") {
		t.Errorf("docstring template must RETURN n.kind for caller disambiguation; got %q", pql)
	}
}

func TestInferAuditPinchQL_UntestedTemplate_IncludesMethods(t *testing.T) {
	pql, _ := inferAuditPinchQL("find untested exported functions")
	if !strings.Contains(pql, "Function") || !strings.Contains(pql, "Method") {
		t.Errorf("untested template must include both Function and Method; got %q", pql)
	}
	if strings.Contains(pql, "(n:Function)") {
		t.Errorf("untested template still uses Function-only label match; got %q", pql)
	}
	if !strings.Contains(pql, "n.kind") {
		t.Errorf("untested template must RETURN n.kind for caller disambiguation; got %q", pql)
	}
}

// Complexity / line-count templates intentionally keep n:Function. A
// "function complexity" or "function line count" audit reads as
// Function-only by phrasing; Method overload of those metrics is a
// separate query the user can run explicitly.
func TestInferAuditPinchQL_ComplexityAndLineCountStillFunctionOnly(t *testing.T) {
	for _, task := range []string{
		"find all functions with complexity above 50",
		"find all functions over 100 lines",
	} {
		pql, _ := inferAuditPinchQL(task)
		if !strings.Contains(pql, "(n:Function)") {
			t.Errorf("metric-shaped template should keep Function-only label for task %q; got %q", task, pql)
		}
	}
}
