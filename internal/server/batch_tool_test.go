// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
)

// batch (loop-substrate): the multi-query envelope tool tests.
//
// Coverage follows the project's positive/negative/control/cross-check
// pattern: happy path (aligned results + slim per-entry _meta), budget
// exhaustion (deterministic input-order skip), sub-error isolation,
// whitelist rejection, project-default propagation, and the stats
// no-double-count contract (_nested sub-calls don't increment
// statsCalls — the outer batch call is the single source of truth).

// batchResults extracts the results array from a decoded batch body.
func batchResults(t *testing.T, body map[string]any) []map[string]any {
	t.Helper()
	raw, ok := body["results"].([]any)
	if !ok {
		t.Fatalf("body has no results array; keys: %v body: %#v", projectableKeys(body), body)
	}
	out := make([]map[string]any, 0, len(raw))
	for i, r := range raw {
		m, ok := r.(map[string]any)
		if !ok {
			t.Fatalf("results[%d] is %T, want object", i, r)
		}
		out = append(out, m)
	}
	return out
}

// warningsV2Codes collects the code fields from _meta.warnings_v2.
func warningsV2Codes(body map[string]any) []string {
	meta, _ := body["_meta"].(map[string]any)
	v2, _ := meta["warnings_v2"].([]any)
	var codes []string
	for _, w := range v2 {
		if m, ok := w.(map[string]any); ok {
			if c, ok := m["code"].(string); ok {
				codes = append(codes, c)
			}
		}
	}
	return codes
}

// Positive: a search + context + trace batch returns 3 aligned entries,
// the outer envelope carries the full _meta, and each per-entry _meta
// is the slim form (no capabilities / watermark chrome).
func TestBatch_HappyPath_AlignedResultsAndSlimMeta(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupComposeTestServer(t)

	res, err := srv.handleBatch(context.Background(), makeReq(map[string]any{
		"project": projectID,
		"queries": []any{
			map[string]any{"tool": "search", "args": map[string]any{"query": "Compute"}},
			map[string]any{"tool": "context", "args": map[string]any{"id": "compose.go::compose.Compute#Function"}},
			map[string]any{"tool": "trace", "args": map[string]any{"name": "Compute"}},
		},
	}))
	if err != nil {
		t.Fatalf("handleBatch: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success; got %s", textOf(t, res))
	}
	body := decode(t, res)

	results := batchResults(t, body)
	if len(results) != 3 {
		t.Fatalf("expected 3 results; got %d: %#v", len(results), results)
	}
	if count, _ := body["count"].(float64); int(count) != 3 {
		t.Errorf("count = %v, want 3", body["count"])
	}
	wantTools := []string{"search", "context", "trace"}
	for i, entry := range results {
		if idx, _ := entry["index"].(float64); int(idx) != i {
			t.Errorf("results[%d].index = %v, want %d (alignment broken)", i, entry["index"], i)
		}
		if tool, _ := entry["tool"].(string); tool != wantTools[i] {
			t.Errorf("results[%d].tool = %q, want %q", i, entry["tool"], wantTools[i])
		}
		if _, ok := entry["result"].(map[string]any); !ok {
			t.Errorf("results[%d] has no result body: %#v", i, entry)
		}
		if errText, ok := entry["error"]; ok {
			t.Errorf("results[%d] unexpectedly errored: %v", i, errText)
		}
		// Per-entry _meta is the slim form: the capabilities /
		// watermark chrome lives ONLY on the outer envelope.
		if subMeta, ok := entry["_meta"].(map[string]any); ok {
			if _, has := subMeta["capabilities"]; has {
				t.Errorf("results[%d]._meta carries capabilities — per-entry _meta must be slim", i)
			}
			if _, has := subMeta["watermark"]; has {
				t.Errorf("results[%d]._meta carries watermark — per-entry _meta must be slim", i)
			}
			if _, has := subMeta["tokens_used"]; !has {
				t.Errorf("results[%d]._meta missing tokens_used — the slim keys should survive", i)
			}
		} else {
			t.Errorf("results[%d] has no _meta; want the slim per-entry form", i)
		}
		// The sub-result body itself must not carry a nested _meta —
		// it was hoisted (slimmed) to the entry level.
		if sub, _ := entry["result"].(map[string]any); sub != nil {
			if _, has := sub["_meta"]; has {
				t.Errorf("results[%d].result still carries the full sub-_meta — should be stripped", i)
			}
		}
	}

	// Outer envelope: the one full _meta for the whole batch.
	meta, ok := body["_meta"].(map[string]any)
	if !ok {
		t.Fatal("outer _meta missing")
	}
	if _, has := meta["capabilities"]; !has {
		t.Error("outer _meta missing capabilities — the outer envelope carries the full chrome")
	}

	// Budget block present and coherent.
	budget, ok := body["budget"].(map[string]any)
	if !ok {
		t.Fatal("budget block missing")
	}
	if mt, _ := budget["max_tokens"].(float64); int(mt) != batchDefaultBudget {
		t.Errorf("budget.max_tokens = %v, want default %d", budget["max_tokens"], batchDefaultBudget)
	}
	if sk, _ := budget["skipped_queries"].(float64); int(sk) != 0 {
		t.Errorf("budget.skipped_queries = %v, want 0", budget["skipped_queries"])
	}
}

// Negative: a tiny max_tokens exhausts after the first sub-query;
// later entries are skipped:"budget_exhausted" and the outer envelope
// warns with code=budget_truncated.
func TestBatch_BudgetExhaustion_SkipsLaterQueries(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupComposeTestServer(t)

	res, err := srv.handleBatch(context.Background(), makeReq(map[string]any{
		"project":    projectID,
		"max_tokens": 1,
		"queries": []any{
			map[string]any{"tool": "search", "args": map[string]any{"query": "Compute"}},
			map[string]any{"tool": "search", "args": map[string]any{"query": "helperA"}},
			map[string]any{"tool": "trace", "args": map[string]any{"name": "Compute"}},
		},
	}))
	if err != nil {
		t.Fatalf("handleBatch: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success envelope; got %s", textOf(t, res))
	}
	body := decode(t, res)
	results := batchResults(t, body)
	if len(results) != 3 {
		t.Fatalf("expected 3 entries (skips included); got %d", len(results))
	}

	// First query runs (budget > 0 at dispatch time); the rest skip.
	if _, ok := results[0]["result"]; !ok {
		t.Errorf("results[0] should have run inside the budget: %#v", results[0])
	}
	for i := 1; i < 3; i++ {
		if sk, _ := results[i]["skipped"].(string); sk != "budget_exhausted" {
			t.Errorf("results[%d].skipped = %v, want \"budget_exhausted\"", i, results[i]["skipped"])
		}
	}

	budget, _ := body["budget"].(map[string]any)
	if sk, _ := budget["skipped_queries"].(float64); int(sk) != 2 {
		t.Errorf("budget.skipped_queries = %v, want 2", budget["skipped_queries"])
	}

	codes := warningsV2Codes(body)
	found := false
	for _, c := range codes {
		if c == "budget_truncated" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected warnings_v2 code=budget_truncated; got codes %v", codes)
	}
}

// Isolation: a bad symbol ID in the middle errors ONLY its own entry;
// the surrounding sub-queries succeed and the envelope carries a
// batch_sub_errors warning naming the failed index.
func TestBatch_SubErrorIsolation_BadSymbolID(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupComposeTestServer(t)

	res, err := srv.handleBatch(context.Background(), makeReq(map[string]any{
		"project": projectID,
		"queries": []any{
			map[string]any{"tool": "search", "args": map[string]any{"query": "Compute"}},
			map[string]any{"tool": "symbol", "args": map[string]any{"id": "nope.go::nope.Missing#Function"}},
			map[string]any{"tool": "trace", "args": map[string]any{"name": "Compute"}},
		},
	}))
	if err != nil {
		t.Fatalf("handleBatch: %v", err)
	}
	if res.IsError {
		t.Fatalf("one bad sub-query must not fail the batch; got %s", textOf(t, res))
	}
	body := decode(t, res)
	results := batchResults(t, body)
	if len(results) != 3 {
		t.Fatalf("expected 3 entries; got %d", len(results))
	}

	if _, ok := results[0]["result"]; !ok {
		t.Errorf("results[0] (search) should succeed: %#v", results[0])
	}
	if errText, _ := results[1]["error"].(string); errText == "" {
		t.Errorf("results[1] (bad symbol id) should carry a non-empty error: %#v", results[1])
	}
	if _, ok := results[2]["result"]; !ok {
		t.Errorf("results[2] (trace) should succeed despite the middle error: %#v", results[2])
	}

	codes := warningsV2Codes(body)
	found := false
	for _, c := range codes {
		if c == "batch_sub_errors" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected warnings_v2 code=batch_sub_errors; got codes %v", codes)
	}
	// The warning message names the failed index.
	meta, _ := body["_meta"].(map[string]any)
	v2, _ := meta["warnings_v2"].([]any)
	for _, w := range v2 {
		m, _ := w.(map[string]any)
		if c, _ := m["code"].(string); c == "batch_sub_errors" {
			msg, _ := m["message"].(string)
			if !strings.Contains(msg, "1") {
				t.Errorf("batch_sub_errors message should name failed index 1; got %q", msg)
			}
		}
	}
}

// Whitelist: a non-batchable sub-tool (adr — a writer) yields a
// per-entry error + batch_sub_errors warning; batchable siblings run.
func TestBatch_UnsupportedSubTool_RejectedPerEntry(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupComposeTestServer(t)

	res, err := srv.handleBatch(context.Background(), makeReq(map[string]any{
		"project": projectID,
		"queries": []any{
			map[string]any{"tool": "adr", "args": map[string]any{"action": "list"}},
			map[string]any{"tool": "search", "args": map[string]any{"query": "Compute"}},
		},
	}))
	if err != nil {
		t.Fatalf("handleBatch: %v", err)
	}
	if res.IsError {
		t.Fatalf("unsupported sub-tool must not fail the batch; got %s", textOf(t, res))
	}
	body := decode(t, res)
	results := batchResults(t, body)
	if len(results) != 2 {
		t.Fatalf("expected 2 entries; got %d", len(results))
	}
	errText, _ := results[0]["error"].(string)
	if !strings.Contains(errText, "not batchable") {
		t.Errorf("results[0].error = %q, want a not-batchable rejection", errText)
	}
	if _, ok := results[1]["result"]; !ok {
		t.Errorf("results[1] (search) should succeed: %#v", results[1])
	}

	codes := warningsV2Codes(body)
	found := false
	for _, c := range codes {
		if c == "batch_sub_errors" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected warnings_v2 code=batch_sub_errors; got codes %v", codes)
	}
}

// Project propagation: the top-level project default is merged into
// each sub-query lacking one. Control first — with no session project
// AND no top-level project, the sub-query fails to resolve a scope;
// with the top-level project set, the same sub-query succeeds.
func TestBatch_ProjectDefaultPropagation(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupComposeTestServer(t)
	// Remove the session fallback so success can ONLY come from the
	// propagated top-level project.
	srv.sessionID = ""

	// Control: no project anywhere → the sub-query errors per-entry.
	res, err := srv.handleBatch(context.Background(), makeReq(map[string]any{
		"queries": []any{
			map[string]any{"tool": "trace", "args": map[string]any{"name": "Compute"}},
		},
	}))
	if err != nil {
		t.Fatalf("handleBatch (control): %v", err)
	}
	ctrl := batchResults(t, decode(t, res))
	if errText, _ := ctrl[0]["error"].(string); errText == "" {
		t.Fatalf("control should fail without any project scope: %#v", ctrl[0])
	}

	// Positive: top-level project flows into the same sub-query.
	res, err = srv.handleBatch(context.Background(), makeReq(map[string]any{
		"project": projectID,
		"queries": []any{
			map[string]any{"tool": "trace", "args": map[string]any{"name": "Compute"}},
		},
	}))
	if err != nil {
		t.Fatalf("handleBatch: %v", err)
	}
	results := batchResults(t, decode(t, res))
	if _, ok := results[0]["result"]; !ok {
		t.Errorf("sub-query should succeed via the propagated top-level project: %#v", results[0])
	}
}

// Stats contract: a 3-sub-query batch increments statsCalls by exactly
// 1 — the outer envelope. _nested sub-calls must not double-count.
func TestBatch_StatsNoDoubleCount(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupComposeTestServer(t)

	before := atomic.LoadInt64(&srv.statsCalls)
	res, err := srv.handleBatch(context.Background(), makeReq(map[string]any{
		"project": projectID,
		"queries": []any{
			map[string]any{"tool": "search", "args": map[string]any{"query": "Compute"}},
			map[string]any{"tool": "search", "args": map[string]any{"query": "helperA"}},
			map[string]any{"tool": "trace", "args": map[string]any{"name": "Compute"}},
		},
	}))
	if err != nil {
		t.Fatalf("handleBatch: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success; got %s", textOf(t, res))
	}
	after := atomic.LoadInt64(&srv.statsCalls)
	if got := after - before; got != 1 {
		t.Errorf("statsCalls grew by %d for a 3-query batch; want exactly 1 (outer call is the single source of stats truth)", got)
	}
}

// Empty / oversized queries are rejected with the rich pedagogy error.
func TestBatch_QueryArrayValidation(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupComposeTestServer(t)

	// Missing queries → rich error.
	res, err := srv.handleBatch(context.Background(), makeReq(map[string]any{
		"project": projectID,
	}))
	if err != nil {
		t.Fatalf("handleBatch: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError for missing queries")
	}

	// 13 sub-queries → over the cap → rich error naming the limit.
	over := make([]any, 0, batchMaxQueries+1)
	for i := 0; i <= batchMaxQueries; i++ {
		over = append(over, map[string]any{"tool": "search", "args": map[string]any{"query": "Compute"}})
	}
	res, err = srv.handleBatch(context.Background(), makeReq(map[string]any{
		"project": projectID,
		"queries": over,
	}))
	if err != nil {
		t.Fatalf("handleBatch: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError for > batchMaxQueries sub-queries")
	}
	if msg := textOf(t, res); !strings.Contains(msg, "12") {
		t.Errorf("oversize error should name the cap (12); got %s", msg)
	}
}
