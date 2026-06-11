// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// les_test.go — LES, the Loop Efficiency Score (ADR
// LOOP_EFFICIENCY_METRIC). Telemetry is seeded through the production
// write paths (AppendLoopCheckpoint, RecordToolCalls,
// jsonResultWithMeta + attachWarningStructured for warning counters,
// the wrapped-handler middleware for error envelopes), and every
// asserted number is pinned with its exact arithmetic. Coverage:
//
//   - the stats SESSION LES line renders the documented arithmetic
//     (incl. the anti-gaming empty-decision checkpoint exclusion)
//   - the stats 7d LES line computes from persisted telemetry
//   - both lines are suppressed on a fresh server
//   - error envelopes are counted at the request middleware
//   - coach fires les_regression on a synthetic week-over-week
//     iteration_cost regression with the exact priced est + basis
//   - loop resume carries les_hint when iteration_cost is computable,
//     and omits it when the loop has no counting checkpoints

// statsText invokes handleStats and returns the rendered box.
func statsText(t *testing.T, srv *Server) string {
	t.Helper()
	res, err := srv.handleStats(context.Background(), makeReq(map[string]any{}))
	if err != nil || res.IsError {
		t.Fatalf("handleStats: err=%v isErr=%v", err, res.IsError)
	}
	return textOf(t, res)
}

func TestLES_StatsSessionLine_ExactArithmetic(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)

	// Two counting checkpoints + one empty-decision row (anti-gaming:
	// the stuffed row must not move the denominator). Written through
	// the production append path, in the session window.
	for _, d := range []string{"Accept — suite green", "Defer — trigger set", ""} {
		if _, err := store.AppendLoopCheckpoint(db.LoopCheckpoint{
			ProjectID: "p-les", LoopName: "ship", Decision: d,
		}); err != nil {
			t.Fatalf("AppendLoopCheckpoint: %v", err)
		}
	}

	// One budget_truncated + one plan_stale warning through the
	// production envelope path (the same attachWarningStructured +
	// jsonResultWithMeta route every handler takes).
	for _, code := range []string{"budget_truncated", "plan_stale"} {
		data := map[string]any{}
		attachWarningStructured(data, code, WarningSeverityWarning, "synthetic", nil)
		srv.jsonResultWithMeta(data, time.Now(), "batch", map[string]any{}, 0)
	}

	// One error envelope through the registered middleware chain — the
	// production chokepoint that counts IsError responses.
	res, err := srv.handlers["loop"](context.Background(), makeReq(map[string]any{"action": "bogus"}))
	if err != nil {
		t.Fatalf("wrapped loop call: %v", err)
	}
	if !res.IsError {
		t.Fatal("loop action=bogus must produce an error envelope")
	}

	// Pin the live counters AFTER the seeding calls above so the
	// asserted arithmetic is exact:
	//   iteration_cost = 5000 tokens / 2 counted checkpoints = 2500 → "2.5k"
	//   waste = (1 zero_unexpected + 1 error envelope + 1 budget_truncated
	//            + 0 ignored redirects) / (10 success + 1 error) = 3/11 → "27%"
	//   recovery = 1 plan_stale + 1 investigate_failure = 2
	//   fidelity: ledger-bearing, no loop call among the first 3 → "no"
	atomic.StoreInt64(&srv.statsTokensUsed, 5000)
	atomic.StoreInt64(&srv.statsCalls, 10)
	atomic.StoreInt64(&srv.statsQueriesZeroUnexpected, 1)
	atomic.StoreInt64(&srv.statsInvestigateFailureCalls, 1)
	if got := atomic.LoadInt64(&srv.statsErrorEnvelopes); got != 1 {
		t.Fatalf("statsErrorEnvelopes = %d after one IsError middleware pass, want 1", got)
	}

	text := statsText(t, srv)
	if !strings.Contains(text, "LES (session):") {
		t.Fatalf("SESSION box missing LES line:\n%s", text)
	}
	want := "i:2.5k w:27% r:2 f:no"
	if !strings.Contains(text, want) {
		t.Errorf("LES session value wrong, want %q:\n%s", want, text)
	}
}

func TestLES_StatsLinesSuppressedOnFreshServer(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	if text := statsText(t, srv); strings.Contains(text, "LES (") {
		t.Errorf("LES line rendered with zero recorded calls:\n%s", text)
	}
}

func TestLES_Stats7dLine_FromPersistedTelemetry(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	now := time.Now()

	// 7d window inputs, all through production write paths:
	//   4 calls × 250 tokens = 1000 tokens; 1 counting checkpoint
	//     → iteration_cost = 1000/1 = 1000 → "1.0k"
	//   1 ignored hook redirect → waste = 1/4 = 25%
	//   session A opens with loop, session B doesn't → fidelity 50%
	//   no investigate_failure rows → recovery 0
	if err := store.RecordToolCalls([]db.ToolCallEvent{
		{SessionID: "A", Tool: "loop", TokensUsed: 250, TS: now.Add(-2 * time.Hour)},
		{SessionID: "A", Tool: "search", TokensUsed: 250, TS: now.Add(-2*time.Hour + time.Minute)},
		{SessionID: "B", Tool: "search", TokensUsed: 250, TS: now.Add(-time.Hour)},
		{SessionID: "B", Tool: "context", TokensUsed: 250, TS: now.Add(-time.Hour + time.Minute)},
	}); err != nil {
		t.Fatalf("RecordToolCalls: %v", err)
	}
	if _, err := store.AppendLoopCheckpoint(db.LoopCheckpoint{
		ProjectID: "p", LoopName: "l", Decision: "Accept — measured",
	}); err != nil {
		t.Fatalf("AppendLoopCheckpoint: %v", err)
	}
	hookTS := now.Add(-time.Hour).UnixNano()
	if err := store.LogHookInvocation(db.HookInvocation{
		TS: hookTS, SessionID: "A", ToolName: "Read", FilePath: "/x/main.go",
		Decision: "redirect", SuggestedTool: "context",
	}); err != nil {
		t.Fatalf("LogHookInvocation: %v", err)
	}
	if _, err := store.ResolveHookInvocationsForSession("A", []db.HookSessionCall{
		{TS: hookTS + 1, ToolName: "Read"}, // bypassed the suggestion
	}); err != nil {
		t.Fatalf("ResolveHookInvocationsForSession: %v", err)
	}

	text := statsText(t, srv)
	if !strings.Contains(text, "LES (7d):") {
		t.Fatalf("box missing LES 7d line:\n%s", text)
	}
	want := "i:1.0k w:25% r:0 f:50%"
	if !strings.Contains(text, want) {
		t.Errorf("LES 7d value wrong, want %q:\n%s", want, text)
	}
	// The session line stays suppressed — this server made no calls.
	if strings.Contains(text, "LES (session):") {
		t.Errorf("session LES line rendered with zero session calls:\n%s", text)
	}
}

func TestLES_CoachRegressionFinding_PricedFromRecordedNumbers(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	now := time.Now()

	// Prior week: 4 calls × 250 tokens = 1000 tokens over 4 counting
	// checkpoints → 250 tokens/checkpoint.
	prevTS := now.Add(-10 * 24 * time.Hour)
	prevEvents := make([]db.ToolCallEvent, 0, 4)
	for i := 0; i < 4; i++ {
		prevEvents = append(prevEvents, db.ToolCallEvent{
			SessionID: "prev", Tool: "search", TokensUsed: 250,
			TS: prevTS.Add(time.Duration(i) * time.Minute),
		})
	}
	if err := store.RecordToolCalls(prevEvents); err != nil {
		t.Fatalf("RecordToolCalls prev: %v", err)
	}
	for i := 0; i < 4; i++ {
		if _, err := store.AppendLoopCheckpoint(db.LoopCheckpoint{
			ProjectID: "p", LoopName: "l", Decision: "Accept — measured",
			CreatedAt: prevTS.UTC().Add(time.Duration(i) * time.Minute).Format(time.RFC3339),
		}); err != nil {
			t.Fatalf("AppendLoopCheckpoint prev: %v", err)
		}
	}

	// Current week: 10 calls × 300 tokens = 3000 tokens over 2 counting
	// checkpoints → 1500 tokens/checkpoint. Regression est =
	// (1500 − 250) × 2 checkpoints = 2500 tokens.
	curTS := now.Add(-2 * 24 * time.Hour)
	curEvents := make([]db.ToolCallEvent, 0, 10)
	for i := 0; i < 10; i++ {
		curEvents = append(curEvents, db.ToolCallEvent{
			SessionID: "cur", Tool: "search", TokensUsed: 300,
			TS: curTS.Add(time.Duration(i) * time.Minute),
		})
	}
	if err := store.RecordToolCalls(curEvents); err != nil {
		t.Fatalf("RecordToolCalls cur: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := store.AppendLoopCheckpoint(db.LoopCheckpoint{
			ProjectID: "p", LoopName: "l", Decision: "Defer — trigger set",
			CreatedAt: curTS.UTC().Add(time.Duration(i) * time.Minute).Format(time.RFC3339),
		}); err != nil {
			t.Fatalf("AppendLoopCheckpoint cur: %v", err)
		}
	}

	body := callCoach(t, srv, map[string]any{"window": "7d"})
	f := findingByPattern(t, body, "les_regression")
	if f == nil {
		t.Fatalf("les_regression finding missing; findings = %v", body["findings"])
	}
	if got, _ := f["sub_metric"].(string); got != "iteration_cost" {
		t.Errorf("sub_metric = %q, want iteration_cost", got)
	}
	if got, _ := f["est_tokens_left_on_table"].(float64); got != 2500 {
		t.Errorf("est = %v, want 2500 = (1500−250) tokens/checkpoint × 2 checkpoints", got)
	}
	basis, _ := f["basis"].(string)
	for _, want := range []string{"250", "1500", "week-over-week", "non-empty-decision", "diagnostic"} {
		if !strings.Contains(basis, want) {
			t.Errorf("basis missing %q:\n%s", want, basis)
		}
	}
}

func TestLES_CoachRegression_NilWithoutPriorWindow(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	now := time.Now()
	// Current week only — heavy, but with no prior week there is
	// nothing honest to compare against.
	events := make([]db.ToolCallEvent, 0, 12)
	for i := 0; i < 12; i++ {
		events = append(events, db.ToolCallEvent{
			SessionID: "cur", Tool: "search", TokensUsed: 5000,
			TS: now.Add(-time.Hour).Add(time.Duration(i) * time.Minute),
		})
	}
	if err := store.RecordToolCalls(events); err != nil {
		t.Fatalf("RecordToolCalls: %v", err)
	}
	body := callCoach(t, srv, map[string]any{"window": "7d"})
	if f := findingByPattern(t, body, "les_regression"); f != nil {
		t.Errorf("les_regression fired with no prior-window telemetry: %v", f)
	}
}

func TestLES_LoopResumeHint(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupComposeTestServer(t)
	store := srv.store

	// Loop with 2 counting checkpoints + 1 empty-decision row; first
	// checkpoint 2h ago. 600 recorded tokens since then →
	// iteration_cost = 600/2 = 300 tokens/checkpoint.
	first := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
	for _, cp := range []db.LoopCheckpoint{
		{ProjectID: projectID, LoopName: "les-loop", Decision: "started", CreatedAt: first},
		{ProjectID: projectID, LoopName: "les-loop", Decision: ""},
		{ProjectID: projectID, LoopName: "les-loop", Decision: "Accept — measured"},
	} {
		if _, err := store.AppendLoopCheckpoint(cp); err != nil {
			t.Fatalf("AppendLoopCheckpoint: %v", err)
		}
	}
	if err := store.RecordToolCalls([]db.ToolCallEvent{
		{SessionID: "s", Tool: "search", TokensUsed: 250, TS: time.Now().Add(-time.Hour)},
		{SessionID: "s", Tool: "context", TokensUsed: 350, TS: time.Now().Add(-30 * time.Minute)},
	}); err != nil {
		t.Fatalf("RecordToolCalls: %v", err)
	}

	b := loopCall(t, srv, map[string]any{"action": "resume", "name": "les-loop", "project": projectID})
	hint, _ := b["les_hint"].(string)
	if hint == "" {
		t.Fatalf("resume brief missing les_hint; body = %v", b)
	}
	for _, want := range []string{"300 tokens/checkpoint", "600 recorded tokens", "2 non-empty-decision checkpoints"} {
		if !strings.Contains(hint, want) {
			t.Errorf("les_hint missing %q: %s", want, hint)
		}
	}
}

func TestLES_LoopResumeHint_OmittedWhenNotComputable(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupComposeTestServer(t)
	store := srv.store
	// Only empty-decision checkpoints — anti-gaming leaves a zero
	// denominator, so the brief must carry NO hint, not a fake one.
	for i := 0; i < 2; i++ {
		if _, err := store.AppendLoopCheckpoint(db.LoopCheckpoint{
			ProjectID: projectID, LoopName: "stuffed", Decision: "",
		}); err != nil {
			t.Fatalf("AppendLoopCheckpoint: %v", err)
		}
	}
	b := loopCall(t, srv, map[string]any{"action": "resume", "name": "stuffed", "project": projectID})
	if hint, ok := b["les_hint"]; ok {
		t.Errorf("les_hint present on a loop with zero counting checkpoints: %v", hint)
	}
}
