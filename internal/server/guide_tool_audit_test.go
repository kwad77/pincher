// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"
)

// #497: classifyTaskShape recognizes "find FPs in dead_code" as a
// tool-output audit, not a generic find/fix.
func TestClassifyTaskShape_ToolAuditPattern(t *testing.T) {
	t.Parallel()
	cases := []struct {
		task string
		want guideShape
	}{
		// Tool name + audit keyword combinations.
		{"find false positives in dead_code", shapeToolAudit},
		{"audit search results", shapeToolAudit},
		{"characterize trace failures on cypher engine", shapeToolAudit},
		{"verify output of dead_code on this repo", shapeToolAudit},
		{"is the search tool noisy?", shapeToolAudit},
		{"check precision of dead_code", shapeToolAudit},
		{"find FPs in dead_code", shapeToolAudit},

		// Tool name without audit keyword → falls through to other shapes.
		{"find dead_code in this repo", shapeFind},
		{"how does dead_code work?", shapeUnderstand},

		// Audit keyword without tool name → existing shapeAudit / shapeFix paths.
		{"find undocumented exported functions", shapeAudit},
		{"audit the missing docstrings", shapeAudit},
	}
	for _, tc := range cases {
		t.Run(tc.task, func(t *testing.T) {
			if got := classifyTaskShape(tc.task); got != tc.want {
				t.Errorf("classifyTaskShape(%q) = %q; want %q", tc.task, got, tc.want)
			}
		})
	}
}

func TestExtractAuditedTool_RecognizesToolNames(t *testing.T) {
	t.Parallel()
	cases := []struct {
		task string
		want string
	}{
		{"find FPs in dead_code", "dead_code"},
		{"audit search results", "search"},
		{"verify trace output on the cypher engine", "trace"},
		{"check the neighborhood tool", "neighborhood"},
		{"nothing tool-shaped here", ""},
		// Word boundary: "search" must not match inside "researching".
		{"researching the codebase", ""},
		// Longer tool name should win over a hypothetical prefix conflict.
		{"check the dead_code FPs", "dead_code"},
	}
	for _, tc := range cases {
		t.Run(tc.task, func(t *testing.T) {
			got := extractAuditedTool(strings.ToLower(tc.task))
			if got != tc.want {
				t.Errorf("extractAuditedTool(%q) = %q; want %q", tc.task, got, tc.want)
			}
		})
	}
}

// End-to-end: handleGuide on a tool-audit task returns a recipe whose
// FIRST step is calling the audited tool itself, not `search`.
func TestHandleGuide_ToolAudit_RecommendsRunningTheTool(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	srv.sessionID = "p1"

	req := makeReq(map[string]any{
		"task": "find false positives in dead_code on this codebase",
	})
	req.Params.Name = "guide"
	result, err := srv.handleGuide(context.Background(), req)
	if err != nil {
		t.Fatalf("handleGuide: %v", err)
	}
	body := decode(t, result)

	if got, _ := body["shape"].(string); got != string(shapeToolAudit) {
		t.Errorf("shape=%q; want %q", got, shapeToolAudit)
	}
	recs, _ := body["recommended_next_tools"].([]any)
	if len(recs) == 0 {
		t.Fatalf("expected recommendations; got none")
	}
	first, _ := recs[0].(map[string]any)
	firstTool, _ := first["tool"].(string)
	if firstTool != "dead_code" {
		t.Errorf("first recommendation must be the audited tool; got %q", firstTool)
	}
	// Second step should be `trace` for verification.
	if len(recs) >= 2 {
		second, _ := recs[1].(map[string]any)
		secondTool, _ := second["tool"].(string)
		if secondTool != "trace" {
			t.Errorf("second recommendation should be 'trace'; got %q", secondTool)
		}
	}
}

// When the tool-audit task doesn't name a specific tool, the
// recommendation falls back to a `<tool-name>` placeholder rather
// than guessing.
func TestHandleGuide_ToolAuditWithoutNamedTool_PlaceholderInRecipe(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	srv.sessionID = "p1"

	// "audit precision" alone won't classify as tool_audit because
	// extractAuditedTool returns "" — falls through to shapeAudit or
	// shapeUnknown depending on other keywords. Confirm we don't
	// route to shapeToolAudit on ambiguous inputs.
	req := makeReq(map[string]any{
		"task": "audit precision of the system",
	})
	req.Params.Name = "guide"
	result, err := srv.handleGuide(context.Background(), req)
	if err != nil {
		t.Fatalf("handleGuide: %v", err)
	}
	body := decode(t, result)
	got, _ := body["shape"].(string)
	if got == string(shapeToolAudit) {
		t.Errorf("ambiguous 'audit precision' should NOT route to tool_audit without a tool name; got shape=%q", got)
	}
}
