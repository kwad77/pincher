// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"
)

// #1645 v0.86: BY TOOL row layout must fit inside the 44-char box border.
// Pre-fix the row format `"1×, 4500ms tot, 4500ms avg, 4500ms max"` was
// 38+ chars and overflowed the box's right border. New compact format
// uses fixed-width columns and `compactMs` for >9999ms values.

// Positive: BY TOOL section is rendered with column-aligned rows when
// at least one tool call has been recorded.
func TestHandleStats_ByToolRenderWidth_ColumnsAlign(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	// Seed three tool latencies that span the compactMs branches:
	// a small value (raw ms), a 5-second value (raw ms still), and
	// a 30-second value (gets the "Ns" collapse).
	srv.recordToolLatency("trace", 4500)
	srv.recordToolLatency("health", 23)
	srv.recordToolLatency("search", 30_000)

	result, err := srv.handleStats(context.Background(), makeReq(nil))
	if err != nil {
		t.Fatalf("handleStats: %v", err)
	}
	text := textOf(t, result)

	if !strings.Contains(text, "BY TOOL (top-5 by total ms)") {
		t.Fatalf("expected BY TOOL header; got:\n%s", text)
	}
	// Column header row.
	if !strings.Contains(text, "tool") || !strings.Contains(text, "total") {
		t.Errorf("expected column header line with `tool` + `total`; got:\n%s", text)
	}
	// Each row must contain the tool name and the appropriate
	// compactMs format. 30_000ms → "30s"; 4500ms → "4,500"; 23ms → "23".
	for _, want := range []string{"search", "30s", "trace", "4,500", "health", "23"} {
		if !strings.Contains(text, want) {
			t.Errorf("BY TOOL row missing %q; got:\n%s", want, text)
		}
	}
	// Width invariant: every line in the rendered box should be exactly
	// the same byte length. Pre-#1645 the BY TOOL rows were 65+ chars
	// while the surrounding rows were 46 (the 44 inner + 2 borders);
	// width-walking each line pins the fix.
	lines := strings.Split(text, "\n")
	var ref int
	for _, l := range lines {
		if strings.HasPrefix(l, "│") && strings.HasSuffix(l, "│") {
			if ref == 0 {
				ref = len(l)
				continue
			}
			if len(l) != ref {
				t.Errorf("box-line width mismatch: ref=%d, got=%d on line %q", ref, len(l), l)
			}
		}
	}
}

// Pre-#1645 regression guard: the old format with "ms tot, ms avg, ms max"
// labels would have overflowed the border. The new format MUST NOT
// contain that legacy phrasing.
func TestHandleStats_ByToolRenderWidth_NoLegacyFormat(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	srv.recordToolLatency("trace", 4500)
	result, err := srv.handleStats(context.Background(), makeReq(nil))
	if err != nil {
		t.Fatalf("handleStats: %v", err)
	}
	text := textOf(t, result)
	for _, banned := range []string{"ms tot", "ms avg", "ms max"} {
		if strings.Contains(text, banned) {
			t.Errorf("rendered output contains legacy fragment %q — new format must drop the verbose suffixes; got:\n%s", banned, text)
		}
	}
}

// Direct unit: compactMs branches. The helper is defined inside
// handleStats so we re-implement the contract inline; this test
// documents what the format renderer commits to.
func TestHandleStats_ByToolRenderWidth_MinuteCollapse(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	// 2 minutes of total time on one tool.
	srv.recordToolLatency("heavy", 120_000)
	result, err := srv.handleStats(context.Background(), makeReq(nil))
	if err != nil {
		t.Fatalf("handleStats: %v", err)
	}
	text := textOf(t, result)
	// 120000ms → "2m" via compactMs(60_000+ branch).
	if !strings.Contains(text, "2m") {
		t.Errorf("expected minute-collapse `2m` for 120000ms; got:\n%s", text)
	}
}

// #1645 — tool names longer than 12 chars get truncated with "…" so the
// column doesn't grow. Currently no real tool exceeds 12 chars but
// pinning the contract defends against future drift.
func TestHandleStats_ByToolRenderWidth_LongToolNameTruncated(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	srv.recordToolLatency("very_long_tool_name", 100)
	result, err := srv.handleStats(context.Background(), makeReq(nil))
	if err != nil {
		t.Fatalf("handleStats: %v", err)
	}
	text := textOf(t, result)
	// First 11 chars + "…" = "very_long_t…".
	if !strings.Contains(text, "very_long_t…") {
		t.Errorf("expected long tool name truncated to `very_long_t…`; got:\n%s", text)
	}
}
