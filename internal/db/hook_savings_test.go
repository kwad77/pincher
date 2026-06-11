// SPDX-License-Identifier: MIT

package db

import (
	"testing"
	"time"
)

// hook-redirect-v2: v40 savings telemetry (est_tokens_served +
// baseline_tokens) and the repeat-read session point query. Same
// in-package placement rationale as hook_invocations_test.go.

func TestHookSavings7d_SumsOnlyMeasuredRedirects(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UnixNano()

	rows := []HookInvocation{
		// Two measured redirects: served 400+500, baseline 12500+2000.
		{TS: now, SessionID: "s", ToolName: "Read", FilePath: "a.go",
			Decision: "redirect_advisory", SuggestedTool: "context",
			EstTokensServed: 400, BaselineTokens: 12500},
		{TS: now + 1, SessionID: "s", ToolName: "Read", FilePath: "b.go",
			Decision: "redirect_advisory", SuggestedTool: "context",
			EstTokensServed: 500, BaselineTokens: 2000},
		// Pass-through carries zero telemetry — must not contribute.
		{TS: now + 2, SessionID: "s", ToolName: "Read", FilePath: "c.go",
			Decision: "pass_through"},
		// Legacy-shaped redirect (pre-v40 binary would write zeroes):
		// excluded so the ratio never mixes measured and unmeasured eras.
		{TS: now + 3, SessionID: "s", ToolName: "Read", FilePath: "d.go",
			Decision: "redirect_advisory", SuggestedTool: "context"},
	}
	for i, r := range rows {
		if err := store.LogHookInvocation(r); err != nil {
			t.Fatalf("log %d: %v", i, err)
		}
	}

	served, baseline, err := store.HookSavings7d()
	if err != nil {
		t.Fatalf("HookSavings7d: %v", err)
	}
	if served != 900 {
		t.Errorf("estServed = %d, want 900", served)
	}
	if baseline != 14500 {
		t.Errorf("baseline = %d, want 14500", baseline)
	}
}

func TestHookSavings7d_EmptyTable(t *testing.T) {
	store := newTestStore(t)
	served, baseline, err := store.HookSavings7d()
	if err != nil {
		t.Fatalf("HookSavings7d on empty table: %v", err)
	}
	if served != 0 || baseline != 0 {
		t.Errorf("empty table should sum to zero; got served=%d baseline=%d", served, baseline)
	}
}

func TestHookSavings7d_ExcludesStaleRows(t *testing.T) {
	store := newTestStore(t)
	old := time.Now().Add(-8 * 24 * time.Hour).UnixNano()
	if err := store.LogHookInvocation(HookInvocation{
		TS: old, SessionID: "s", ToolName: "Read", FilePath: "a.go",
		Decision: "redirect_advisory", SuggestedTool: "context",
		EstTokensServed: 400, BaselineTokens: 9000,
	}); err != nil {
		t.Fatalf("log: %v", err)
	}
	served, baseline, err := store.HookSavings7d()
	if err != nil {
		t.Fatalf("HookSavings7d: %v", err)
	}
	if served != 0 || baseline != 0 {
		t.Errorf("8-day-old row leaked into the 7d window; got served=%d baseline=%d", served, baseline)
	}
}

func TestHookFileSeenInSession(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UnixNano()
	if err := store.LogHookInvocation(HookInvocation{
		TS: now, SessionID: "sess-a", ToolName: "Read",
		FilePath: "/abs/path/f.go", Decision: "redirect_advisory",
	}); err != nil {
		t.Fatalf("log: %v", err)
	}

	if !store.HookFileSeenInSession("sess-a", "/abs/path/f.go") {
		t.Error("logged (session, file) pair not reported as seen")
	}
	if store.HookFileSeenInSession("sess-b", "/abs/path/f.go") {
		t.Error("other session must not see sess-a's file")
	}
	if store.HookFileSeenInSession("sess-a", "/abs/path/other.go") {
		t.Error("unseen file reported as seen")
	}
	if store.HookFileSeenInSession("", "/abs/path/f.go") {
		t.Error("empty session ID must always report false")
	}
}
