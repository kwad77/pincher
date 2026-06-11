// SPDX-License-Identifier: MIT

package server

// Guide-coaching PR-15/17: adherence-aware ranking. #1631 stamped
// next_steps_adherence on every envelope but nothing consumed it — a
// 0%-followed session went unnoticed. Guide now orders its
// recommendations by each tool's process-lifetime followed-rate once
// the tool has ≥ adherenceRankMinEmitted emissions; tools below the
// floor keep their shape-driven positions. The counters are NOT
// persisted (no schema change) — they reset on restart.

import (
	"fmt"
	"testing"
)

// seedAdherence records `emitted` recommendations for a tool and then
// consumes `followed` of them, producing a deterministic per-tool
// followed-rate in the tracker. Distinct args per emission so each
// CheckAndConsume credits exactly one stashed entry.
func seedAdherence(tr *nextStepsAdherenceTracker, tool string, emitted, followed int) {
	for i := 0; i < emitted; i++ {
		args := fmt.Sprintf(`{"q":"%d"}`, i)
		tr.RecordEmitted("seed-session", []map[string]string{{"tool": tool, "args": args}})
		if i < followed {
			tr.CheckAndConsume("seed-session", tool, map[string]any{"q": fmt.Sprintf("%d", i)})
		}
	}
}

func TestAdherenceTracker_PerToolStats(t *testing.T) {
	t.Parallel()
	var tr nextStepsAdherenceTracker
	seedAdherence(&tr, "trace", 10, 9)
	seedAdherence(&tr, "search", 10, 1)

	if e, f := tr.ToolStats("trace"); e != 10 || f != 9 {
		t.Errorf("ToolStats(trace) = (%d, %d), want (10, 9)", e, f)
	}
	if e, f := tr.ToolStats("search"); e != 10 || f != 1 {
		t.Errorf("ToolStats(search) = (%d, %d), want (10, 1)", e, f)
	}
	if e, f := tr.ToolStats("never-recommended"); e != 0 || f != 0 {
		t.Errorf("ToolStats(never-recommended) = (%d, %d), want (0, 0)", e, f)
	}
	// Totals stay consistent with the per-tool split.
	if e, f := tr.Stats(); e != 20 || f != 10 {
		t.Errorf("Stats() = (%d, %d), want (20, 10)", e, f)
	}
}

func TestRankRecommendationsByAdherence_ReordersByFollowedRate(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	seedAdherence(&srv.nextStepsAdherence, "search", 10, 1) // 10%
	seedAdherence(&srv.nextStepsAdherence, "trace", 10, 9)  // 90%

	recs := []map[string]string{
		{"tool": "search", "args": "{}", "why": "a"},
		{"tool": "trace", "args": "{}", "why": "b"},
		{"tool": "context", "args": "{}", "why": "c"}, // 0 emissions — keeps its slot
	}
	got := srv.rankRecommendationsByAdherence(recs)
	want := []string{"trace", "search", "context"}
	for i, w := range want {
		if got[i]["tool"] != w {
			t.Fatalf("rank order = [%s %s %s], want %v",
				got[0]["tool"], got[1]["tool"], got[2]["tool"], want)
		}
	}
}

func TestRankRecommendationsByAdherence_BelowEmissionFloorKeepsShapeOrder(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	// 9 emissions — one short of adherenceRankMinEmitted. The 0%-vs-100%
	// rate split must NOT reorder.
	seedAdherence(&srv.nextStepsAdherence, "search", 9, 0)
	seedAdherence(&srv.nextStepsAdherence, "trace", 9, 9)

	recs := []map[string]string{
		{"tool": "search", "args": "{}", "why": "a"},
		{"tool": "trace", "args": "{}", "why": "b"},
	}
	got := srv.rankRecommendationsByAdherence(recs)
	if got[0]["tool"] != "search" || got[1]["tool"] != "trace" {
		t.Errorf("order changed below the emission floor: [%s %s]", got[0]["tool"], got[1]["tool"])
	}
}

// End-to-end: computeGuide's final list reflects the adherence
// ordering. The fix shape ships [search, context, trace]; with search
// historically ignored (10%) and trace historically followed (90%),
// the qualifying pair swaps while context (no data) keeps its slot.
func TestComputeGuide_OrdersByAdherence(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	seedAdherence(&srv.nextStepsAdherence, "search", 10, 1)
	seedAdherence(&srv.nextStepsAdherence, "trace", 10, 9)

	_, _, recs, _ := srv.computeGuide("fix the login timeout bug", "")
	if len(recs) != 3 {
		t.Fatalf("expected 3 recommendations, got %d: %v", len(recs), recs)
	}
	want := []string{"trace", "context", "search"}
	for i, w := range want {
		if recs[i]["tool"] != w {
			t.Fatalf("adherence-ranked order = [%s %s %s], want %v",
				recs[0]["tool"], recs[1]["tool"], recs[2]["tool"], want)
		}
	}
}
