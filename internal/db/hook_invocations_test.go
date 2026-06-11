// SPDX-License-Identifier: MIT

package db

import (
	"testing"
	"time"
)

// #626: hook_invocations data layer. These tests live in the internal/db
// package so the coverage tool attributes the hits correctly — the
// cmd/pinch hook_check_test.go exercises the same helpers but goes
// through the binary boundary which doesn't credit internal/db.

func TestLogHookInvocation_AndConversionRate(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UnixNano()

	// Three redirects, two taken. Conversion = 2/3 = 66.67%.
	if err := store.LogHookInvocation(HookInvocation{
		TS: now, SessionID: "s1", ToolName: "Read",
		FilePath: "f.go", FileBytes: 50000,
		Decision: "redirect", SuggestedTool: "context",
		SuggestedArgs: `{"id":"x"}`,
	}); err != nil {
		t.Fatalf("log 1: %v", err)
	}
	if err := store.LogHookInvocation(HookInvocation{
		TS: now + 1, SessionID: "s1", ToolName: "Grep",
		FilePath: "", FileBytes: 0,
		Decision: "redirect", SuggestedTool: "search",
		SuggestedArgs: `{"query":"Foo"}`,
	}); err != nil {
		t.Fatalf("log 2: %v", err)
	}
	if err := store.LogHookInvocation(HookInvocation{
		TS: now + 2, SessionID: "s1", ToolName: "Read",
		FilePath: "g.go", FileBytes: 60000,
		Decision: "redirect", SuggestedTool: "context",
		SuggestedArgs: `{"id":"y"}`,
	}); err != nil {
		t.Fatalf("log 3: %v", err)
	}
	// Pass-through (shouldn't count as a redirect).
	if err := store.LogHookInvocation(HookInvocation{
		TS: now + 3, SessionID: "s1", ToolName: "Read",
		FilePath: "tiny.txt", FileBytes: 100,
		Decision: "pass_through",
	}); err != nil {
		t.Fatalf("log 4: %v", err)
	}

	// Resolve. Two of the three redirects suggested `context`; the
	// third suggested `search`. The agent's next-3 calls include both
	// context AND search, so all three redirects resolve as taken.
	// (The metric is "did the agent end up calling the suggested tool"
	// — it doesn't try to attribute each individual call to a single
	// redirect.)
	calls := []HookSessionCall{
		{TS: now + 10, ToolName: "context"},
		{TS: now + 11, ToolName: "search"},
		{TS: now + 12, ToolName: "Edit"},
	}
	updated, err := store.ResolveHookInvocationsForSession("s1", calls)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if updated != 3 {
		t.Errorf("updated rows = %d, want 3", updated)
	}

	pct, redirects, taken, err := store.HookConversionRate7d()
	if err != nil {
		t.Fatalf("conversion rate: %v", err)
	}
	if redirects != 3 {
		t.Errorf("redirects = %d, want 3", redirects)
	}
	if taken != 3 {
		t.Errorf("taken = %d, want 3", taken)
	}
	if pct < 99.9 || pct > 100.1 {
		t.Errorf("pct = %.2f, want ~100", pct)
	}
}

// Asymmetric case: redirect suggests context, but agent never calls
// context within the next 3 tool calls. Resolves as taken=0.
func TestResolveHookInvocations_NotTakenWhenSuggestedToolMissing(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UnixNano()
	if err := store.LogHookInvocation(HookInvocation{
		TS: now, SessionID: "skip", ToolName: "Read",
		Decision: "redirect", SuggestedTool: "context",
	}); err != nil {
		t.Fatalf("log: %v", err)
	}
	// Next 3 calls: agent ignored the redirect.
	updated, err := store.ResolveHookInvocationsForSession("skip", []HookSessionCall{
		{TS: now + 1, ToolName: "Read"},
		{TS: now + 2, ToolName: "Edit"},
		{TS: now + 3, ToolName: "Bash"},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if updated != 1 {
		t.Errorf("updated = %d, want 1", updated)
	}
	_, _, taken, err := store.HookConversionRate7d()
	if err != nil {
		t.Fatalf("conversion rate: %v", err)
	}
	if taken != 0 {
		t.Errorf("taken = %d, want 0 (agent ignored the redirect)", taken)
	}
}

// #629: HookOverrideRate7d isolates "agent saw and rejected" from
// "no signal yet". Three redirects: one taken, one ignored, one with
// no subsequent calls (unresolved). Override rate should be 1/2=50%
// — the unresolved row is excluded from both numerator and denominator.
func TestHookOverrideRate7d(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UnixNano()
	for i, sid := range []string{"o1", "o2", "o3"} {
		if err := store.LogHookInvocation(HookInvocation{
			TS: now + int64(i), SessionID: sid, ToolName: "Read",
			Decision: "redirect", SuggestedTool: "context",
		}); err != nil {
			t.Fatalf("log %s: %v", sid, err)
		}
	}
	// o1: agent took the redirect.
	if _, err := store.ResolveHookInvocationsForSession("o1",
		[]HookSessionCall{{TS: now + 100, ToolName: "context"}}); err != nil {
		t.Fatalf("resolve o1: %v", err)
	}
	// o2: agent rejected — called something else.
	if _, err := store.ResolveHookInvocationsForSession("o2",
		[]HookSessionCall{{TS: now + 100, ToolName: "Edit"}}); err != nil {
		t.Fatalf("resolve o2: %v", err)
	}
	// o3: never resolved (no subsequent calls).
	pct, overrides, resolved, err := store.HookOverrideRate7d()
	if err != nil {
		t.Fatalf("override rate: %v", err)
	}
	if resolved != 2 {
		t.Errorf("resolved = %d, want 2 (o3 still NULL)", resolved)
	}
	if overrides != 1 {
		t.Errorf("overrides = %d, want 1", overrides)
	}
	if pct < 49.9 || pct > 50.1 {
		t.Errorf("override pct = %.2f, want ~50", pct)
	}
}

// Empty store returns (0, 0, 0, nil) — small-N onboarding shape.
func TestHookOverrideRate7d_EmptyStore(t *testing.T) {
	store := newTestStore(t)
	pct, overrides, resolved, err := store.HookOverrideRate7d()
	if err != nil {
		t.Fatalf("override rate: %v", err)
	}
	if pct != 0 || overrides != 0 || resolved != 0 {
		t.Errorf("empty store should be zero-shaped; got pct=%.2f overrides=%d resolved=%d", pct, overrides, resolved)
	}
}

// #629: per-tool breakdown isolates Read-vs-Grep imbalance. Two
// Read redirects (one taken) + one Grep redirect (taken) + one
// pass_through Read (counts toward Read total but not redirects).
func TestHookCountsByTool7d(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UnixNano()
	rows := []HookInvocation{
		{TS: now, SessionID: "t", ToolName: "Read", Decision: "redirect", SuggestedTool: "context"},
		{TS: now + 1, SessionID: "t", ToolName: "Read", Decision: "redirect", SuggestedTool: "context"},
		{TS: now + 2, SessionID: "t", ToolName: "Read", Decision: "pass_through"},
		{TS: now + 3, SessionID: "t", ToolName: "Grep", Decision: "redirect", SuggestedTool: "search"},
	}
	for i, r := range rows {
		if err := store.LogHookInvocation(r); err != nil {
			t.Fatalf("log %d: %v", i, err)
		}
	}
	if _, err := store.ResolveHookInvocationsForSession("t", []HookSessionCall{
		{TS: now + 10, ToolName: "context"},
		{TS: now + 11, ToolName: "search"},
	}); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	out, err := store.HookCountsByTool7d()
	if err != nil {
		t.Fatalf("by tool: %v", err)
	}
	if got := out["Read"]["redirects"]; got != 2 {
		t.Errorf("Read redirects = %d, want 2", got)
	}
	if got := out["Read"]["taken"]; got != 2 {
		// Both Read redirects suggested context; agent called context once,
		// which marks every redirect-with-suggested-tool=context as taken.
		t.Errorf("Read taken = %d, want 2", got)
	}
	if got := out["Grep"]["redirects"]; got != 1 {
		t.Errorf("Grep redirects = %d, want 1", got)
	}
	if got := out["Grep"]["taken"]; got != 1 {
		t.Errorf("Grep taken = %d, want 1", got)
	}
}

func TestResolveHookInvocations_SkipsAlreadyResolved(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UnixNano()
	if err := store.LogHookInvocation(HookInvocation{
		TS: now, SessionID: "s2", ToolName: "Read",
		Decision: "redirect", SuggestedTool: "context",
	}); err != nil {
		t.Fatalf("log: %v", err)
	}
	// First resolve: agent took it.
	if _, err := store.ResolveHookInvocationsForSession("s2",
		[]HookSessionCall{{TS: now + 1, ToolName: "context"}}); err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	// Second resolve with no new calls: should not re-process the
	// already-resolved row (took_recommendation IS NOT NULL filter).
	updated, err := store.ResolveHookInvocationsForSession("s2",
		[]HookSessionCall{{TS: now + 100, ToolName: "Edit"}})
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	if updated != 0 {
		t.Errorf("second resolve updated %d rows; resolved rows must not be re-processed", updated)
	}
}

func TestResolveHookInvocations_SkipsWhenNoSubsequentCalls(t *testing.T) {
	// If the joiner runs before the agent has issued any next calls,
	// took_recommendation stays NULL — we don't have evidence yet.
	store := newTestStore(t)
	now := time.Now().UnixNano()
	if err := store.LogHookInvocation(HookInvocation{
		TS: now, SessionID: "s3", ToolName: "Read",
		Decision: "redirect", SuggestedTool: "context",
	}); err != nil {
		t.Fatalf("log: %v", err)
	}
	// Empty calls list: nothing strictly after `now` to inspect.
	updated, err := store.ResolveHookInvocationsForSession("s3", nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if updated != 0 {
		t.Errorf("no subsequent calls → no resolution; got updated=%d", updated)
	}
}

func TestIsFileIndexed_FileHashesPresence(t *testing.T) {
	store := newTestStore(t)
	if err := store.UpsertProject(Project{ID: "p1", Path: "/tmp/p1", Name: "p1"}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}
	if store.IsFileIndexed("p1", "f.go") {
		t.Error("file not indexed yet — should report false")
	}
	if err := store.SetFileHash("p1", "f.go", "abc"); err != nil {
		t.Fatalf("set hash: %v", err)
	}
	if !store.IsFileIndexed("p1", "f.go") {
		t.Error("after SetFileHash, IsFileIndexed should return true")
	}
}

func TestCountSymbolsInFile(t *testing.T) {
	store := newTestStore(t)
	if err := store.UpsertProject(Project{ID: "p1", Path: "/tmp/p1", Name: "p1"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	syms := []Symbol{
		{ID: "p1::a#Function", ProjectID: "p1", FilePath: "f.go", Name: "a", QualifiedName: "a", Kind: "Function", Language: "Go", ExtractionConfidence: 1.0},
		{ID: "p1::b#Function", ProjectID: "p1", FilePath: "f.go", Name: "b", QualifiedName: "b", Kind: "Function", Language: "Go", ExtractionConfidence: 1.0},
		{ID: "p1::c#Function", ProjectID: "p1", FilePath: "g.go", Name: "c", QualifiedName: "c", Kind: "Function", Language: "Go", ExtractionConfidence: 1.0},
	}
	if err := store.BulkUpsertSymbols(syms); err != nil {
		t.Fatalf("upsert syms: %v", err)
	}

	n, err := store.CountSymbolsInFile("p1", "f.go")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Errorf("count(f.go) = %d, want 2", n)
	}
	n, err = store.CountSymbolsInFile("p1", "g.go")
	if err != nil || n != 1 {
		t.Errorf("count(g.go) = %d (err=%v), want 1", n, err)
	}
	n, err = store.CountSymbolsInFile("p1", "missing.go")
	if err != nil || n != 0 {
		t.Errorf("count(missing.go) = %d (err=%v), want 0", n, err)
	}
}

func TestLargestSymbolInFile(t *testing.T) {
	store := newTestStore(t)
	if err := store.UpsertProject(Project{ID: "p1", Path: "/tmp/p1", Name: "p1"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	syms := []Symbol{
		{ID: "p1::small#Function", ProjectID: "p1", FilePath: "f.go", Name: "small", QualifiedName: "small", Kind: "Function", Language: "Go", StartByte: 0, EndByte: 50, ExtractionConfidence: 1.0},
		{ID: "p1::big#Function", ProjectID: "p1", FilePath: "f.go", Name: "big", QualifiedName: "big", Kind: "Function", Language: "Go", StartByte: 100, EndByte: 5000, ExtractionConfidence: 1.0},
		{ID: "p1::medium#Function", ProjectID: "p1", FilePath: "f.go", Name: "medium", QualifiedName: "medium", Kind: "Function", Language: "Go", StartByte: 6000, EndByte: 6500, ExtractionConfidence: 1.0},
	}
	if err := store.BulkUpsertSymbols(syms); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	id, span, err := store.LargestSymbolInFile("p1", "f.go")
	if err != nil {
		t.Fatalf("largest: %v", err)
	}
	if id != "p1::big#Function" {
		t.Errorf("largest = %q, want p1::big#Function", id)
	}
	// hook-redirect-v2: span feeds est_tokens_served telemetry.
	if span != 4900 {
		t.Errorf("span = %d, want 4900 (5000-100)", span)
	}

	// Empty file: returns ("", 0, nil) — best-effort, not a hard error.
	id, span, err = store.LargestSymbolInFile("p1", "missing.go")
	if err != nil {
		t.Errorf("missing file should not error; got %v", err)
	}
	if id != "" || span != 0 {
		t.Errorf("missing file should return empty id + zero span; got %q / %d", id, span)
	}
}
