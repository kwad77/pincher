// SPDX-License-Identifier: MIT

package db

import (
	"testing"
	"time"
)

// les_test.go — read-side queries backing LES (ADR LOOP_EFFICIENCY_METRIC).
// Telemetry is seeded through the production write paths
// (AppendLoopCheckpoint, RecordToolCalls, RecordSessionWithMetrics,
// LogHookInvocation + ResolveHookInvocationsForSession); window
// backdating uses the same direct-UPDATE pattern coach_test.go
// established for rows whose timestamps the production writer pins to
// time.Now().

func TestCountLoopCheckpointsBetween_AntiGamingAndWindow(t *testing.T) {
	s := openTestStore(t)
	now := time.Now().UTC()

	// Two counting checkpoints, one empty-decision row (stuffing), and
	// one whitespace-only decision (also stuffing) — all in-window.
	for _, cp := range []LoopCheckpoint{
		{ProjectID: "p", LoopName: "l", Decision: "Accept — suite green"},
		{ProjectID: "p", LoopName: "l", Decision: "Defer — trigger set"},
		{ProjectID: "p", LoopName: "l", Decision: ""},
		{ProjectID: "p", LoopName: "l", Decision: "   "},
	} {
		if _, err := s.AppendLoopCheckpoint(cp); err != nil {
			t.Fatalf("AppendLoopCheckpoint: %v", err)
		}
	}
	// One out-of-window row (created 10 days ago, would otherwise count).
	if _, err := s.AppendLoopCheckpoint(LoopCheckpoint{
		ProjectID: "p", LoopName: "l", Decision: "old",
		CreatedAt: now.Add(-10 * 24 * time.Hour).Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("AppendLoopCheckpoint old: %v", err)
	}

	counted, total, err := s.CountLoopCheckpointsBetween(now.Add(-7*24*time.Hour), now.Add(time.Minute))
	if err != nil {
		t.Fatalf("CountLoopCheckpointsBetween: %v", err)
	}
	if counted != 2 || total != 4 {
		t.Errorf("counted=%d total=%d, want counted=2 (empty/whitespace decisions excluded) total=4 (old row windowed out)",
			counted, total)
	}
}

func TestTokensUsedBetween_AndCountToolCallsBetween(t *testing.T) {
	s := openTestStore(t)
	now := time.Now()
	if err := s.RecordToolCalls([]ToolCallEvent{
		{SessionID: "a", Tool: "search", TokensUsed: 250, TS: now.Add(-time.Hour)},
		{SessionID: "a", Tool: "investigate_failure", TokensUsed: 750, TS: now.Add(-time.Hour)},
		{SessionID: "b", Tool: "investigate_failure", TokensUsed: 500, TS: now.Add(-2 * time.Hour)},
		{SessionID: "b", Tool: "search", TokensUsed: 9999, TS: now.Add(-10 * 24 * time.Hour)}, // windowed out
	}); err != nil {
		t.Fatalf("RecordToolCalls: %v", err)
	}

	tokens, calls, err := s.TokensUsedBetween(now.Add(-7*24*time.Hour), now)
	if err != nil {
		t.Fatalf("TokensUsedBetween: %v", err)
	}
	if tokens != 1500 || calls != 3 {
		t.Errorf("tokens=%d calls=%d, want 1500/3 (exact sum of in-window recorded tokens_used)", tokens, calls)
	}

	n, err := s.CountToolCallsBetween("investigate_failure", now.Add(-7*24*time.Hour), now)
	if err != nil {
		t.Fatalf("CountToolCallsBetween: %v", err)
	}
	if n != 2 {
		t.Errorf("investigate_failure count = %d, want 2", n)
	}
}

func TestQueryMetricsBetween_HalfOpenWindow(t *testing.T) {
	s := openTestStore(t)
	now := time.Now()
	if err := s.RecordSessionWithMetrics("cur", now.Add(-time.Hour), 5, 100, 50, 0, "", 0, "",
		QueryMetrics{QueriesZeroUnexpected: 2, TokensBurnedOnFailures: 700}); err != nil {
		t.Fatalf("RecordSessionWithMetrics cur: %v", err)
	}
	if err := s.RecordSessionWithMetrics("prior", now.Add(-10*24*time.Hour), 5, 100, 50, 0, "", 0, "",
		QueryMetrics{QueriesZeroUnexpected: 5, TokensBurnedOnFailures: 900}); err != nil {
		t.Fatalf("RecordSessionWithMetrics prior: %v", err)
	}
	// Backdate the prior session's last_seen into the prior-7d window
	// (RecordSessionWithMetrics pins last_seen to now; same time-travel
	// pattern as TestQueryMetricsSince_ExcludesStaleSessions).
	if _, err := s.db.Exec(`UPDATE sessions SET last_seen = ? WHERE session_id = 'prior'`,
		now.Add(-10*24*time.Hour).Unix()); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	cur, err := s.QueryMetricsBetween(now.Add(-7*24*time.Hour), now.Add(time.Minute))
	if err != nil {
		t.Fatalf("QueryMetricsBetween cur: %v", err)
	}
	if cur.QueriesZeroUnexpected != 2 {
		t.Errorf("current-window zero_unexpected = %d, want 2", cur.QueriesZeroUnexpected)
	}
	prev, err := s.QueryMetricsBetween(now.Add(-14*24*time.Hour), now.Add(-7*24*time.Hour))
	if err != nil {
		t.Fatalf("QueryMetricsBetween prev: %v", err)
	}
	if prev.QueriesZeroUnexpected != 5 {
		t.Errorf("prior-window zero_unexpected = %d, want 5 — half-open windows must not overlap", prev.QueriesZeroUnexpected)
	}
}

func TestHookRedirectIgnoredBetween(t *testing.T) {
	s := openTestStore(t)
	now := time.Now().UnixNano()
	// One ignored redirect (resolved via the production joiner) + one
	// taken redirect + one unresolved redirect (excluded).
	for i, hi := range []HookInvocation{
		{TS: now, SessionID: "sess-x", ToolName: "Read", FilePath: "/a.go", Decision: "redirect", SuggestedTool: "context"},
		{TS: now + 10, SessionID: "sess-x", ToolName: "Grep", FilePath: "/b.go", Decision: "redirect", SuggestedTool: "search"},
		{TS: now + 20, SessionID: "sess-y", ToolName: "Read", FilePath: "/c.go", Decision: "redirect", SuggestedTool: "context"},
	} {
		if err := s.LogHookInvocation(hi); err != nil {
			t.Fatalf("LogHookInvocation %d: %v", i, err)
		}
	}
	if _, err := s.ResolveHookInvocationsForSession("sess-x", []HookSessionCall{
		{TS: now + 1, ToolName: "Read"},    // ignored the first redirect
		{TS: now + 11, ToolName: "search"}, // took the second
	}); err != nil {
		t.Fatalf("ResolveHookInvocationsForSession: %v", err)
	}

	from := time.Unix(0, now).Add(-time.Hour)
	to := time.Unix(0, now).Add(time.Hour)
	ignored, err := s.HookRedirectIgnoredBetween(from, to)
	if err != nil {
		t.Fatalf("HookRedirectIgnoredBetween: %v", err)
	}
	if ignored != 1 {
		t.Errorf("ignored = %d, want 1 (taken + unresolved redirects excluded)", ignored)
	}
}

func TestLoopOpeningSessionsBetween(t *testing.T) {
	s := openTestStore(t)
	now := time.Now()
	if err := s.RecordToolCalls([]ToolCallEvent{
		// Session A opens with loop (resume pattern) — counts.
		{SessionID: "A", Tool: "loop", TokensUsed: 100, TS: now.Add(-3 * time.Hour)},
		{SessionID: "A", Tool: "search", TokensUsed: 100, TS: now.Add(-3*time.Hour + time.Minute)},
		// Session B only reaches loop at its 4th call — does not count
		// against firstN=3.
		{SessionID: "B", Tool: "search", TokensUsed: 100, TS: now.Add(-2 * time.Hour)},
		{SessionID: "B", Tool: "symbol", TokensUsed: 100, TS: now.Add(-2*time.Hour + time.Minute)},
		{SessionID: "B", Tool: "context", TokensUsed: 100, TS: now.Add(-2*time.Hour + 2*time.Minute)},
		{SessionID: "B", Tool: "loop", TokensUsed: 100, TS: now.Add(-2*time.Hour + 3*time.Minute)},
		// Session C never touches loop.
		{SessionID: "C", Tool: "search", TokensUsed: 100, TS: now.Add(-time.Hour)},
	}); err != nil {
		t.Fatalf("RecordToolCalls: %v", err)
	}

	sessions, opening, err := s.LoopOpeningSessionsBetween(now.Add(-7*24*time.Hour), now, 3)
	if err != nil {
		t.Fatalf("LoopOpeningSessionsBetween: %v", err)
	}
	if sessions != 3 || opening != 1 {
		t.Errorf("sessions=%d opening=%d, want 3/1 (only A has loop among its first 3 calls)", sessions, opening)
	}
}

func TestLoopIterationSpan(t *testing.T) {
	s := openTestStore(t)
	first := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
	for _, cp := range []LoopCheckpoint{
		{ProjectID: "p", LoopName: "ship-x", Decision: "started", CreatedAt: first},
		{ProjectID: "p", LoopName: "ship-x", Decision: ""}, // stuffing — excluded
		{ProjectID: "p", LoopName: "ship-x", Decision: "Accept — measured"},
		{ProjectID: "p", LoopName: "other", Decision: "unrelated loop"},
	} {
		if _, err := s.AppendLoopCheckpoint(cp); err != nil {
			t.Fatalf("AppendLoopCheckpoint: %v", err)
		}
	}
	counted, firstAt, err := s.LoopIterationSpan("p", "ship-x")
	if err != nil {
		t.Fatalf("LoopIterationSpan: %v", err)
	}
	if counted != 2 {
		t.Errorf("counted = %d, want 2 (empty decision excluded, other loop excluded)", counted)
	}
	if firstAt != first {
		t.Errorf("firstCreatedAt = %q, want %q", firstAt, first)
	}

	counted, firstAt, err = s.LoopIterationSpan("p", "nonexistent")
	if err != nil {
		t.Fatalf("LoopIterationSpan empty: %v", err)
	}
	if counted != 0 || firstAt != "" {
		t.Errorf("empty loop: counted=%d firstAt=%q, want 0/\"\"", counted, firstAt)
	}
}
