// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/kwad77/pincher/internal/index"
)

// PR-8/9 (loop-substrate): loop ledger + resume brief tests.

func loopCall(t *testing.T, srv *Server, args map[string]any) map[string]any {
	t.Helper()
	res, err := srv.handleLoop(context.Background(), makeReq(args))
	if err != nil {
		t.Fatalf("handleLoop(%v): %v", args, err)
	}
	if res.IsError {
		t.Fatalf("handleLoop(%v) errored: %s", args, textOf(t, res))
	}
	return decode(t, res)
}

// Happy path: start → checkpoint × 2 → list → resume round-trips the
// ledger with monotonically increasing seq and surfaces open triggers.
func TestLoop_StartCheckpointListResume_RoundTrip(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupComposeTestServer(t)

	b := loopCall(t, srv, map[string]any{
		"action": "start", "name": "bench-flip", "project": projectID,
		"claim": "the A/B benchmark flips after M2+M3 ship",
	})
	if seq, _ := b["seq"].(float64); int(seq) != 1 {
		t.Fatalf("start should allocate seq=1, got %v", b["seq"])
	}

	loopCall(t, srv, map[string]any{
		"action": "checkpoint", "name": "bench-flip", "project": projectID,
		"claim": "PR-5 caps envelopes", "decision": "Accept — suite green",
		"confidence": "measured", "evidence": "commit 99658ef; TestHandleContext_MaxTokens_*",
	})
	b3 := loopCall(t, srv, map[string]any{
		"action": "checkpoint", "name": "bench-flip", "project": projectID,
		"claim": "deltas land", "decision": "Defer",
		"reopen_trigger": "re-run benchmark B after PR-8/9 merges",
	})
	if seq, _ := b3["seq"].(float64); int(seq) != 3 {
		t.Fatalf("third write should be seq=3, got %v", b3["seq"])
	}

	lst := loopCall(t, srv, map[string]any{"action": "list", "project": projectID})
	loops, _ := lst["loops"].([]any)
	if len(loops) != 1 {
		t.Fatalf("expected 1 loop, got %d", len(loops))
	}

	r := loopCall(t, srv, map[string]any{"action": "resume", "project": projectID})
	if name, _ := r["loop"].(string); name != "bench-flip" {
		t.Errorf("resume without name should pick the latest loop, got %q", name)
	}
	brief, _ := r["brief"].([]any)
	if len(brief) != 3 {
		t.Errorf("expected all 3 checkpoints in the brief, got %d", len(brief))
	}
	triggers, _ := r["open_triggers"].([]any)
	if len(triggers) != 1 {
		t.Fatalf("expected 1 open trigger, got %d", len(triggers))
	}
	tr, _ := triggers[0].(map[string]any)
	if s, _ := tr["reopen_trigger"].(string); !strings.Contains(s, "benchmark B") {
		t.Errorf("trigger content lost: %v", tr)
	}
}

// Budget: a fat ledger trims oldest-first and reports the cut.
func TestLoop_Resume_BudgetTrimsOldestFirst(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupComposeTestServer(t)

	fat := strings.Repeat("evidence padding ", 40) // ~120 tokens each
	for i := 0; i < 12; i++ {
		loopCall(t, srv, map[string]any{
			"action": "checkpoint", "name": "fat-loop", "project": projectID,
			"claim": fmt.Sprintf("iteration %d", i), "evidence": fat,
		})
	}
	r := loopCall(t, srv, map[string]any{
		"action": "resume", "name": "fat-loop", "project": projectID, "max_tokens": 300,
	})
	brief, _ := r["brief"].([]any)
	omitted, _ := r["omitted_checkpoints"].(float64)
	if len(brief) == 0 {
		t.Fatal("budget must keep at least the newest checkpoint")
	}
	if int(omitted) == 0 {
		t.Errorf("12 fat checkpoints under max_tokens=300 must omit some; brief=%d omitted=%v", len(brief), omitted)
	}
	// Newest must survive; it carries the highest claim index.
	last, _ := brief[len(brief)-1].(map[string]any)
	if c, _ := last["claim"].(string); c != "iteration 11" {
		t.Errorf("newest checkpoint must survive the trim, tail claim=%q", c)
	}
}

// Watermark drift: a completed index pass between checkpoint and resume
// flips index_changed_since_last_checkpoint and warns.
func TestLoop_Resume_IndexMovedWarning(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupComposeTestServer(t)
	srv.indexer = index.New(srv.store)

	loopCall(t, srv, map[string]any{
		"action": "start", "name": "drift-loop", "project": projectID, "claim": "x",
	})
	if _, err := srv.indexer.Index(context.Background(), srv.sessionRoot, false); err != nil {
		t.Fatalf("reindex: %v", err)
	}
	r := loopCall(t, srv, map[string]any{
		"action": "resume", "name": "drift-loop", "project": projectID,
	})
	if r["index_changed_since_last_checkpoint"] != true {
		t.Errorf("index pass between checkpoint and resume must flip the drift flag; got %v", r["index_changed_since_last_checkpoint"])
	}
	meta, _ := r["_meta"].(map[string]any)
	raw, _ := meta["warnings_v2"].([]any)
	found := false
	for _, w := range raw {
		if wm, ok := w.(map[string]any); ok {
			if c, _ := wm["code"].(string); c == "index_moved_since_checkpoint" {
				found = true
			}
		}
	}
	if !found {
		t.Error("expected warnings_v2 code=index_moved_since_checkpoint")
	}
}

// Empty resume: no loops recorded → rich error pointing at start.
func TestLoop_Resume_NoLoops_RichError(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupComposeTestServer(t)
	res, err := srv.handleLoop(context.Background(), makeReq(map[string]any{
		"action": "resume", "project": projectID,
	}))
	if err != nil {
		t.Fatalf("handleLoop: %v", err)
	}
	if !res.IsError {
		t.Fatal("resume with no loops must be a rich error, not a success envelope")
	}
}
