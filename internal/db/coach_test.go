// SPDX-License-Identifier: MIT

package db

import (
	"testing"
	"time"
)

// coach_test.go — read-side telemetry queries backing the `coach` tool.
//
//   - ToolCallsSince windows across sessions (RecentToolCallsForSession
//     can't — it is session-scoped by design).
//   - QueryMetricsSince sums only sessions flushed inside the window.
//   - HookRedirectOutcomes scopes by session OR by window and counts
//     ignored redirects without leaking unresolved rows into the
//     denominator.
//   - HookTokenColumnsPresent gates the per-row token-estimate columns
//     (est_tokens_served + baseline_tokens, shipped in v41 by
//     hook-redirect-v2): true on the shipped schema, false once the
//     columns are dropped (the pre-v41 shape), and
//     HookRedirectTokensLeftOnTable sums the measured estimates.

func TestToolCallsSince_WindowsAcrossSessions(t *testing.T) {
	s := openTestStore(t)
	now := time.Now()
	old := now.Add(-8 * 24 * time.Hour) // outside a 7d window
	if err := s.RecordToolCalls([]ToolCallEvent{
		{SessionID: "sess-a", Tool: "search", ComplexityTier: "lite", TokensUsed: 100, TS: now},
		{SessionID: "sess-b", Tool: "context", ComplexityTier: "standard", TokensUsed: 2500, TS: now.Add(-time.Hour)},
		{SessionID: "sess-a", Tool: "trace", ComplexityTier: "standard", TokensUsed: 90, TS: old},
	}); err != nil {
		t.Fatalf("RecordToolCalls: %v", err)
	}

	got, err := s.ToolCallsSince(now.Add(-7*24*time.Hour), 0)
	if err != nil {
		t.Fatalf("ToolCallsSince: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ToolCallsSince returned %d rows, want 2 (the 8-day-old row must be excluded)", len(got))
	}
	// Newest first; spans both sessions.
	if got[0].SessionID != "sess-a" || got[1].SessionID != "sess-b" {
		t.Errorf("rows = [%s:%s, %s:%s], want newest-first across sessions",
			got[0].SessionID, got[0].Tool, got[1].SessionID, got[1].Tool)
	}
}

func TestQueryMetricsSince_ExcludesStaleSessions(t *testing.T) {
	s := openTestStore(t)
	now := time.Now()
	// Fresh session: flushed now.
	if err := s.RecordSessionWithMetrics("sess-fresh", now.Add(-time.Hour), 5, 100, 50, 0, "", 0, "",
		QueryMetrics{QueriesTotal: 10, QueriesZeroResult: 3, QueriesZeroUnexpected: 2, TokensBurnedOnFailures: 700}); err != nil {
		t.Fatalf("RecordSessionWithMetrics fresh: %v", err)
	}
	// Stale session: force last_seen outside the window.
	if err := s.RecordSessionWithMetrics("sess-stale", now.Add(-30*24*time.Hour), 5, 100, 50, 0, "", 0, "",
		QueryMetrics{QueriesTotal: 99, QueriesZeroUnexpected: 99, TokensBurnedOnFailures: 9999}); err != nil {
		t.Fatalf("RecordSessionWithMetrics stale: %v", err)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET last_seen = ? WHERE session_id = 'sess-stale'`,
		now.Add(-30*24*time.Hour).Unix()); err != nil {
		t.Fatalf("backdate stale session: %v", err)
	}

	qm, err := s.QueryMetricsSince(now.Add(-7 * 24 * time.Hour))
	if err != nil {
		t.Fatalf("QueryMetricsSince: %v", err)
	}
	if qm.QueriesZeroUnexpected != 2 || qm.TokensBurnedOnFailures != 700 {
		t.Errorf("windowed metrics = {zero_unexpected:%d, burned:%d}, want {2, 700} — stale session leaked in",
			qm.QueriesZeroUnexpected, qm.TokensBurnedOnFailures)
	}
}

func TestHookRedirectOutcomes_SessionAndWindowScopes(t *testing.T) {
	s := openTestStore(t)
	now := time.Now().UnixNano()
	sid := "sess-hooks"

	// Two redirects in-session: one ignored, one taken; plus one
	// unresolved redirect (no subsequent calls) and one pass_through.
	for i, inv := range []HookInvocation{
		{TS: now + 1, SessionID: sid, ToolName: "Read", Decision: "redirect", SuggestedTool: "context"},
		{TS: now + 2, SessionID: sid, ToolName: "Grep", Decision: "redirect", SuggestedTool: "search"},
		{TS: now + 100, SessionID: sid, ToolName: "Grep", Decision: "redirect", SuggestedTool: "search"},
		{TS: now + 3, SessionID: sid, ToolName: "Read", Decision: "pass_through"},
		{TS: now + 4, SessionID: "other-sess", ToolName: "Read", Decision: "redirect", SuggestedTool: "context"},
	} {
		if err := s.LogHookInvocation(inv); err != nil {
			t.Fatalf("LogHookInvocation[%d]: %v", i, err)
		}
	}
	// Resolve through the production joiner: redirect 1 (suggested
	// `context`) never sees its suggestion within the next 3 calls →
	// ignored; redirect 2 (suggested `search`) does → taken. The
	// TS=now+100 redirect has no subsequent calls → stays unresolved.
	if _, err := s.ResolveHookInvocationsForSession(sid, []HookSessionCall{
		{TS: now + 10, ToolName: "Read"},
		{TS: now + 11, ToolName: "Read"},
		{TS: now + 12, ToolName: "search"}, // takes redirect 2, 3rd-and-last look for redirect 1
	}); err != nil {
		t.Fatalf("ResolveHookInvocationsForSession: %v", err)
	}

	redirects, resolved, ignored, err := s.HookRedirectOutcomes(sid, time.Time{})
	if err != nil {
		t.Fatalf("HookRedirectOutcomes(session): %v", err)
	}
	if redirects != 3 || resolved != 2 || ignored != 1 {
		t.Errorf("session scope = {redirects:%d, resolved:%d, ignored:%d}, want {3, 2, 1}", redirects, resolved, ignored)
	}

	// Window scope spans sessions (the other-sess redirect counts).
	redirects, _, _, err = s.HookRedirectOutcomes("", time.Unix(0, now))
	if err != nil {
		t.Fatalf("HookRedirectOutcomes(window): %v", err)
	}
	if redirects != 4 {
		t.Errorf("window scope redirects = %d, want 4 (cross-session)", redirects)
	}
}

func TestHookTokenColumns_GateAndPricedSum(t *testing.T) {
	s := openTestStore(t)

	// Shipped schema (v41, hook-redirect-v2): the per-row estimate
	// columns exist natively, so the gate must discover them.
	if !s.HookTokenColumnsPresent() {
		t.Fatal("HookTokenColumnsPresent = false on the shipped v41 schema — priced coaching would never activate")
	}

	now := time.Now().UnixNano()
	sid := "sess-priced"
	if _, err := s.db.Exec(
		`INSERT INTO hook_invocations(ts, session_id, tool_name, decision, suggested_tool, took_recommendation, est_tokens_served, baseline_tokens)
		 VALUES (?, ?, 'Read', 'redirect', 'context', 0, 200, 1500),
		        (?, ?, 'Read', 'redirect', 'context', 0, 900, 400),
		        (?, ?, 'Read', 'redirect', 'context', 1, 100, 2000)`,
		now+1, sid, now+2, sid, now+3, sid,
	); err != nil {
		t.Fatalf("insert priced rows: %v", err)
	}

	got, err := s.HookRedirectTokensLeftOnTable(sid, time.Time{})
	if err != nil {
		t.Fatalf("HookRedirectTokensLeftOnTable: %v", err)
	}
	// Row 1: 1500−200 = 1300. Row 2: negative → clamped to 0.
	// Row 3: took_recommendation=1 → excluded.
	if got != 1300 {
		t.Errorf("tokens left on table = %d, want 1300 (Σ max(baseline − served, 0) over ignored rows only)", got)
	}

	// Simulate the pre-v41 shape: with either column gone the gate must
	// report absent so coach degrades to counts-only instead of erroring.
	if _, err := s.db.Exec(`ALTER TABLE hook_invocations DROP COLUMN baseline_tokens`); err != nil {
		t.Fatalf("simulate pre-v41 schema: %v", err)
	}
	if s.HookTokenColumnsPresent() {
		t.Fatal("HookTokenColumnsPresent = true after dropping baseline_tokens — gate would price from a column that doesn't exist")
	}
}
