// SPDX-License-Identifier: MIT

package db

// precompact-hook: LoopLedgerStats backs the PreCompact compaction
// advisory — per-loop {name, checkpoint count, latest seq, open
// reopen-trigger count, latest receipt} in ONE grouped query so the
// hook stays inside its <50ms / ≤3-query budget. CountADRs is its
// companion point query.

import "testing"

func TestLoopLedgerStats_AggregatesPerLoop(t *testing.T) {
	t.Parallel()
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	const project = "p-stats"
	seed := []LoopCheckpoint{
		{ProjectID: project, LoopName: "alpha", Claim: "frame alpha", Decision: "started", CreatedAt: "2026-06-10T08:00:00Z"},
		{ProjectID: project, LoopName: "alpha", Claim: "", Decision: "keep", ReopenTrigger: "reopen on regression", CreatedAt: "2026-06-10T09:00:00Z"},
		{ProjectID: project, LoopName: "beta", Claim: "frame beta", Decision: "started", ReopenTrigger: "reopen on flake", CreatedAt: "2026-06-10T10:00:00Z"},
		{ProjectID: project, LoopName: "beta", Claim: "latest beta claim", Decision: "keep", CreatedAt: "2026-06-10T11:00:00Z"},
		{ProjectID: project, LoopName: "beta", Claim: "", Decision: "", ReopenTrigger: "   ", CreatedAt: "2026-06-10T12:00:00Z"}, // whitespace trigger ≠ open
	}
	for _, cp := range seed {
		if _, err := store.AppendLoopCheckpoint(cp); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	// A different project's rows must not leak in.
	if _, err := store.AppendLoopCheckpoint(LoopCheckpoint{
		ProjectID: "p-other", LoopName: "alpha", Claim: "other project", CreatedAt: "2026-06-10T13:00:00Z",
	}); err != nil {
		t.Fatalf("append other: %v", err)
	}

	stats, err := store.LoopLedgerStats(project)
	if err != nil {
		t.Fatalf("LoopLedgerStats: %v", err)
	}
	if len(stats) != 2 {
		t.Fatalf("stats len = %d, want 2", len(stats))
	}
	// Most recently touched first.
	if stats[0].LoopName != "beta" || stats[1].LoopName != "alpha" {
		t.Fatalf("order = [%s, %s], want [beta, alpha]", stats[0].LoopName, stats[1].LoopName)
	}

	beta := stats[0]
	if beta.Checkpoints != 3 || beta.LatestSeq != 3 {
		t.Errorf("beta = %+v, want 3 checkpoints latest seq 3", beta)
	}
	if beta.OpenTriggers != 1 {
		t.Errorf("beta open triggers = %d, want 1 (whitespace-only trigger must not count)", beta.OpenTriggers)
	}
	// Latest beta row has empty claim AND empty decision → receipt "".
	if beta.LatestReceipt != "" {
		t.Errorf("beta receipt = %q, want empty (latest row has no claim/decision)", beta.LatestReceipt)
	}

	alpha := stats[1]
	if alpha.Checkpoints != 2 || alpha.LatestSeq != 2 || alpha.OpenTriggers != 1 {
		t.Errorf("alpha = %+v, want 2 checkpoints, seq 2, 1 open trigger", alpha)
	}
	// Latest alpha row: claim empty → falls back to decision.
	if alpha.LatestReceipt != "keep" {
		t.Errorf("alpha receipt = %q, want decision fallback %q", alpha.LatestReceipt, "keep")
	}
}

func TestLoopLedgerStats_EmptyProject(t *testing.T) {
	t.Parallel()
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	stats, err := store.LoopLedgerStats("p-empty")
	if err != nil {
		t.Fatalf("LoopLedgerStats: %v", err)
	}
	if len(stats) != 0 {
		t.Errorf("stats = %+v, want empty", stats)
	}
}

func TestCountADRs(t *testing.T) {
	t.Parallel()
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	n, err := store.CountADRs("p-adr")
	if err != nil || n != 0 {
		t.Fatalf("empty count = %d, %v; want 0, nil", n, err)
	}
	if err := store.SetADR("p-adr", "k1", "v1"); err != nil {
		t.Fatalf("SetADR: %v", err)
	}
	if err := store.SetADR("p-adr", "k2", "v2"); err != nil {
		t.Fatalf("SetADR: %v", err)
	}
	if err := store.SetADR("p-unrelated", "k1", "v1"); err != nil {
		t.Fatalf("SetADR: %v", err)
	}
	n, err = store.CountADRs("p-adr")
	if err != nil || n != 2 {
		t.Errorf("count = %d, %v; want 2, nil", n, err)
	}
}
