// SPDX-License-Identifier: MIT

package db

import (
	"testing"
	"time"
)

// Sev-2 fix (v1.5.0 adversarial review): handoff manifests carry fat
// server-composed decisions but are not iterations — they must not
// count toward LES iteration_cost denominators.
func TestLES_HandoffCheckpointsExcludedFromDenominator(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	pid := "les-handoff-proj"
	for _, cp := range []LoopCheckpoint{
		{ProjectID: pid, LoopName: "l", Claim: "iteration 1", Decision: "Accept — real work"},
		{ProjectID: pid, LoopName: "l", Claim: "HANDOFF — wrapping up", Decision: "watermark: g3\nopen[1]: ..."},
		{ProjectID: pid, LoopName: "l", Claim: "HANDOFF", Decision: "manifest body"},
		{ProjectID: pid, LoopName: "l", Claim: "iteration 2", Decision: ""},
	} {
		if _, err := store.AppendLoopCheckpoint(cp); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	counted, total, err := store.CountLoopCheckpointsBetween(
		time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if total != 4 || counted != 1 {
		t.Errorf("want counted=1 (handoffs + empty excluded) total=4; got counted=%d total=%d", counted, total)
	}
	spanCounted, _, err := store.LoopIterationSpan(pid, "l")
	if err != nil {
		t.Fatalf("span: %v", err)
	}
	if spanCounted != 1 {
		t.Errorf("LoopIterationSpan want 1, got %d", spanCounted)
	}
}
