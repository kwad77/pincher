// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"testing"
)

// #1440 v0.72: context_for_task task-driven path called
// SearchSymbolsByCorpus directly, bypassing handleSearch's
// AND→OR fallback (server.go:5469). Prose tasks routinely
// fail to seed because FTS5's default AND semantics required
// every prose token to appear in some symbol's text.
//
// Repro from live dogfood: context_for_task
//   task="classify a corpus file as code or config"
// returned zero seeds even though `search` with the same
// query returns Compute-class hits via and_fallback_to_or.
//
// Table: positive (prose task seeds), control (single-token
// still works — no regression), cross-check (genuinely-empty
// prose still returns the diagnosis envelope).

func TestContextForTask_TaskDriven_ProseQuery_SurfacesSeedsViaAndOrFallback(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupComposeTestServer(t)

	// The fixture has `Compute`, `helperA`, `helperB`, `Caller`,
	// `Widget`, `Render`. A prose query "Compute a helper widget
	// for rendering" would AND-out to zero under default FTS5
	// semantics (no symbol contains every word) but OR-out to
	// surface the obvious matches.
	res, err := srv.handleContextForTask(context.Background(), makeReq(map[string]any{
		"task":    "Compute a helper widget for rendering",
		"project": projectID,
	}))
	if err != nil {
		t.Fatalf("handleContextForTask: %v", err)
	}
	if res.IsError {
		t.Fatalf("prose task should resolve seeds via AND→OR fallback; got error: %s", textOf(t, res))
	}
	body := decode(t, res)
	seeds, _ := body["seeds"].([]any)
	if len(seeds) == 0 {
		t.Errorf("expected seeds for prose query via AND→OR fallback; got 0\nfull body: %+v", body)
	}
}

// Control — single-token tasks (where FTS5 AND semantics
// already work) still return seeds. The fallback path must
// not regress the single-token case.
func TestContextForTask_TaskDriven_SingleToken_StillSeeds(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupComposeTestServer(t)

	res, err := srv.handleContextForTask(context.Background(), makeReq(map[string]any{
		"task":    "Compute",
		"project": projectID,
	}))
	if err != nil {
		t.Fatalf("handleContextForTask: %v", err)
	}
	if res.IsError {
		t.Fatalf("single-token task should resolve seeds; got error: %s", textOf(t, res))
	}
	body := decode(t, res)
	if seeds, _ := body["seeds"].([]any); len(seeds) == 0 {
		t.Errorf("expected seeds for single-token task; got 0\nfull body: %+v", body)
	}
}

// Cross-check — a prose query with no overlap to the corpus
// (intentionally absurd) still hits the empty-seed diagnosis.
// The AND→OR fallback widens recall, but a query that genuinely
// shares no token with any symbol must still report empty
// rather than confabulate.
func TestContextForTask_TaskDriven_TrulyAbsentProse_StillStampsEmpty(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupComposeTestServer(t)

	res, err := srv.handleContextForTask(context.Background(), makeReq(map[string]any{
		"task":    "zzzqq xyzwomp neverappears blarbleflop",
		"project": projectID,
	}))
	if err != nil {
		t.Fatalf("handleContextForTask: %v", err)
	}
	if res.IsError {
		t.Fatalf("absent prose should still return a result envelope (with empty_reason), not an error: %s", textOf(t, res))
	}
	body := decode(t, res)
	if seeds, _ := body["seeds"].([]any); len(seeds) != 0 {
		t.Errorf("absent prose should not invent seeds; got %d", len(seeds))
	}
	meta, _ := body["_meta"].(map[string]any)
	if reason, _ := meta["empty_reason"].(string); reason == "" {
		t.Errorf("absent prose should stamp empty_reason; got meta=%v", meta)
	}
}
