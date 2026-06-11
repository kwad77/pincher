// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// coach_test.go — the `coach` tool mines recorded telemetry and prices
// findings from it. Events are seeded through recordToolCallEvent, the
// exact write path every production tool call takes (handleCoach drains
// the buffer itself before reading). Coverage:
//
//   - each shipped pattern fires with the documented price
//   - every finding carries a non-empty basis string (the honesty gate)
//   - < 10 calls → empty findings + "not enough telemetry" note
//   - limit trims to the biggest measured wins
//   - the 7d window sees flushed events
//   - bad window arg errors with next_steps
//   - the burst price's envelope overhead tracks a real response's _meta

// seedCoachEvents pushes events through the production write path.
func seedCoachEvents(t *testing.T, srv *Server, events []db.ToolCallEvent) {
	t.Helper()
	for _, e := range events {
		srv.recordToolCallEvent(e.Tool, baselineMethodForTool[e.Tool], int(e.TokensUsed), 0, int(e.ResponseBytes), map[string]any{})
	}
}

// callCoach invokes the handler and decodes the body.
func callCoach(t *testing.T, srv *Server, args map[string]any) map[string]any {
	t.Helper()
	res, err := srv.handleCoach(context.Background(), makeReq(args))
	if err != nil {
		t.Fatalf("handleCoach: %v", err)
	}
	if res.IsError {
		t.Fatalf("handleCoach returned error result: %s", textOf(t, res))
	}
	return decode(t, res)
}

func findingByPattern(t *testing.T, body map[string]any, pattern string) map[string]any {
	t.Helper()
	findings, _ := body["findings"].([]any)
	for _, f := range findings {
		m, _ := f.(map[string]any)
		if m["pattern"] == pattern {
			return m
		}
	}
	return nil
}

// seedAllPatterns records 10 events covering bursts and heavy context,
// sets the live zero-result counters, and logs an ignored hook redirect
// — every shipped pattern in one fixture.
func seedAllPatterns(t *testing.T, srv *Server, store *db.Store) {
	t.Helper()
	// 6 single-fact calls (search/symbol/trace, each < 600 tokens) +
	// 2 unbudgeted heavy context calls + 2 neutral fillers = 10 events.
	seedCoachEvents(t, srv, []db.ToolCallEvent{
		{Tool: "search", TokensUsed: 300},
		{Tool: "search", TokensUsed: 250},
		{Tool: "search", TokensUsed: 400},
		{Tool: "symbol", TokensUsed: 200},
		{Tool: "trace", TokensUsed: 150},
		{Tool: "trace", TokensUsed: 100},
		{Tool: "context", TokensUsed: 5000},
		{Tool: "context", TokensUsed: 3000},
		{Tool: "guide", TokensUsed: 1000},
		{Tool: "guide", TokensUsed: 1000},
	})
	// #241/#1632 counters: 4 caller-surprising empty results that burned
	// 900 recorded tokens. Same atomics the query-metrics path increments.
	atomic.StoreInt64(&srv.statsQueriesZeroUnexpected, 4)
	atomic.StoreInt64(&srv.statsTokensBurned, 900)

	// One ignored hook redirect, resolved through the production joiner.
	now := time.Now().UnixNano()
	sid := srv.persistentSessionID
	if err := store.LogHookInvocation(db.HookInvocation{
		TS: now, SessionID: sid, ToolName: "Read", FilePath: "/x/main.go",
		Decision: "redirect", SuggestedTool: "context",
	}); err != nil {
		t.Fatalf("LogHookInvocation: %v", err)
	}
	if _, err := store.ResolveHookInvocationsForSession(sid, []db.HookSessionCall{
		{TS: now + 1, ToolName: "Read"}, // didn't take the suggestion
	}); err != nil {
		t.Fatalf("ResolveHookInvocationsForSession: %v", err)
	}
}

func TestCoach_AllShippedPatternsFireWithDocumentedPrices(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	seedAllPatterns(t, srv, store)

	body := callCoach(t, srv, map[string]any{})

	if got, _ := body["window"].(string); got != "session" {
		t.Errorf("window = %q, want default \"session\"", got)
	}
	if got, _ := body["calls_analyzed"].(float64); got != 10 {
		t.Errorf("calls_analyzed = %v, want 10", got)
	}

	// 1. single_fact_burst: 6 calls, est = (6−1) × measured envelope.
	burst := findingByPattern(t, body, "single_fact_burst")
	if burst == nil {
		t.Fatal("single_fact_burst finding missing")
	}
	if got, _ := burst["occurrences"].(float64); got != 6 {
		t.Errorf("burst occurrences = %v, want 6", got)
	}
	wantBurst := float64(5 * srv.measuredMetaOverheadTokens())
	if got, _ := burst["est_tokens_left_on_table"].(float64); got != wantBurst {
		t.Errorf("burst est = %v, want %v ((N−1) × measured overhead)", got, wantBurst)
	}

	// 2. unbudgeted_heavy_context: Σ(tokens_used − 800) = 4200 + 2200.
	heavy := findingByPattern(t, body, "unbudgeted_heavy_context")
	if heavy == nil {
		t.Fatal("unbudgeted_heavy_context finding missing")
	}
	if got, _ := heavy["occurrences"].(float64); got != 2 {
		t.Errorf("heavy occurrences = %v, want 2", got)
	}
	if got, _ := heavy["est_tokens_left_on_table"].(float64); got != 6400 {
		t.Errorf("heavy est = %v, want 6400 (= (5000−800) + (3000−800))", got)
	}

	// 4. zero_result_churn: counters round-trip as recorded.
	churn := findingByPattern(t, body, "zero_result_churn")
	if churn == nil {
		t.Fatal("zero_result_churn finding missing")
	}
	if got, _ := churn["occurrences"].(float64); got != 4 {
		t.Errorf("churn occurrences = %v, want 4", got)
	}
	if got, _ := churn["est_tokens_left_on_table"].(float64); got != 900 {
		t.Errorf("churn est = %v, want 900 (tokens_burned_on_failures as recorded)", got)
	}
	if rec, _ := churn["recommendation"].(string); !strings.Contains(rec, "why_empty") {
		t.Errorf("churn recommendation %q should point at why_empty", rec)
	}

	// 5. hook_fall_through: counts-only on the shipped (pre-v40) schema.
	hook := findingByPattern(t, body, "hook_fall_through")
	if hook == nil {
		t.Fatal("hook_fall_through finding missing")
	}
	if got, _ := hook["occurrences"].(float64); got != 1 {
		t.Errorf("hook occurrences = %v, want 1", got)
	}
	if got, _ := hook["est_tokens_left_on_table"].(float64); got != 0 {
		t.Errorf("hook est = %v, want 0 — pre-v40 schema must degrade to counts-only, not invent a number", got)
	}
	if basis, _ := hook["basis"].(string); !strings.Contains(basis, "counts-only") {
		t.Errorf("hook basis %q should declare counts-only degradation", basis)
	}
}

// Honest rule: every finding documents how its number was computed.
func TestCoach_EveryFindingCarriesBasis(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	seedAllPatterns(t, srv, store)

	body := callCoach(t, srv, map[string]any{})
	findings, _ := body["findings"].([]any)
	if len(findings) != 4 {
		t.Fatalf("findings count = %d, want 4 (all shipped patterns seeded)", len(findings))
	}
	for _, f := range findings {
		m, _ := f.(map[string]any)
		for _, key := range []string{"pattern", "recommendation", "basis"} {
			if v, _ := m[key].(string); v == "" {
				t.Errorf("finding %v missing %s", m["pattern"], key)
			}
		}
		if _, ok := m["occurrences"].(float64); !ok {
			t.Errorf("finding %v missing numeric occurrences", m["pattern"])
		}
		if _, ok := m["est_tokens_left_on_table"].(float64); !ok {
			t.Errorf("finding %v missing numeric est_tokens_left_on_table", m["pattern"])
		}
	}
}

func TestCoach_NotEnoughTelemetryReturnsNoteNotNoise(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	// 3 calls that would be a burst if the sample were big enough.
	seedCoachEvents(t, srv, []db.ToolCallEvent{
		{Tool: "search", TokensUsed: 100},
		{Tool: "search", TokensUsed: 100},
		{Tool: "search", TokensUsed: 100},
	})

	body := callCoach(t, srv, map[string]any{})
	if findings, _ := body["findings"].([]any); len(findings) != 0 {
		t.Errorf("findings = %v, want empty — 3 calls is below the %d-call floor", findings, coachMinCallsForFindings)
	}
	note, _ := body["note"].(string)
	if !strings.Contains(note, "not enough telemetry") {
		t.Errorf("note = %q, want a 'not enough telemetry' explanation", note)
	}
	if got, _ := body["calls_analyzed"].(float64); got != 3 {
		t.Errorf("calls_analyzed = %v, want 3", got)
	}
}

func TestCoach_LimitKeepsBiggestMeasuredWins(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	seedAllPatterns(t, srv, store)

	body := callCoach(t, srv, map[string]any{"limit": 2})
	findings, _ := body["findings"].([]any)
	if len(findings) != 2 {
		t.Fatalf("findings count = %d, want 2 (limit applied)", len(findings))
	}
	// Sorted by est desc: heavy (6400) must be first; hook (0) trimmed.
	first, _ := findings[0].(map[string]any)
	if first["pattern"] != "unbudgeted_heavy_context" {
		t.Errorf("first finding = %v, want unbudgeted_heavy_context (largest measured win)", first["pattern"])
	}
	if f := findingByPattern(t, body, "hook_fall_through"); f != nil {
		t.Error("hook_fall_through (est 0) survived limit=2 — sort order broken")
	}
}

func TestCoach_SevenDayWindowSeesFlushedEvents(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	seedAllPatterns(t, srv, store)

	body := callCoach(t, srv, map[string]any{"window": "7d"})
	if got, _ := body["window"].(string); got != "7d" {
		t.Errorf("window = %q, want 7d", got)
	}
	if got, _ := body["calls_analyzed"].(float64); got != 10 {
		t.Errorf("calls_analyzed = %v, want 10 — events just recorded are inside the 7d window", got)
	}
	if f := findingByPattern(t, body, "unbudgeted_heavy_context"); f == nil {
		t.Error("unbudgeted_heavy_context missing in 7d window")
	}
	// 7d zero-churn reads flushed sessions rows; the session flushed in
	// drainToolCallEvents path doesn't write the sessions row unless
	// flushSession ran — exercise that too.
	atomic.StoreInt32(&srv.mcpConnected, 1)
	atomic.AddInt64(&srv.statsCalls, 10)
	srv.flushSession()
	body = callCoach(t, srv, map[string]any{"window": "7d"})
	if f := findingByPattern(t, body, "zero_result_churn"); f == nil {
		t.Error("zero_result_churn missing in 7d window after flushSession persisted the counters")
	} else if got, _ := f["occurrences"].(float64); got != 4 {
		t.Errorf("7d churn occurrences = %v, want 4", got)
	}
	_ = store
}

func TestCoach_UnknownWindowErrorsWithNextSteps(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	res, err := srv.handleCoach(context.Background(), makeReq(map[string]any{"window": "30d"}))
	if err != nil {
		t.Fatalf("handleCoach: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error result for window=30d")
	}
	if txt := textOf(t, res); !strings.Contains(txt, "session") || !strings.Contains(txt, "7d") {
		t.Errorf("error %q should name the valid windows", txt)
	}
}

// The burst price hinges on the measured _meta envelope overhead. Pin
// it against a REAL response's _meta so the measurement can't silently
// drift from what the server actually stamps: marshal the _meta of a
// live handleList call through the same db.ApproxTokens estimator and
// require the coach-side measurement to land in the same ballpark.
func TestCoach_MetaOverheadIsMeasuredFromRealEnvelope(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)

	measured := srv.measuredMetaOverheadTokens()
	if measured <= 0 {
		t.Fatalf("measuredMetaOverheadTokens = %d, want > 0", measured)
	}

	res, err := srv.handleList(context.Background(), makeReq(map[string]any{}))
	if err != nil {
		t.Fatalf("handleList: %v", err)
	}
	body := decode(t, res)
	meta, ok := body["_meta"].(map[string]any)
	if !ok {
		t.Fatal("handleList response has no _meta envelope")
	}
	b, err := json.Marshal(map[string]any{"_meta": meta})
	if err != nil {
		t.Fatalf("marshal real _meta: %v", err)
	}
	real := db.ApproxTokens(string(b))
	if measured < real/3 || measured > real*3 {
		t.Errorf("measured overhead %d diverges from a real envelope's %d by more than 3× — update measuredMetaOverheadTokens to mirror jsonResultWithMeta", measured, real)
	}
}

// Sanity: the burst basis embeds the same overhead number used in the
// price, so a reader can re-derive the figure.
func TestCoach_BurstBasisEmbedsMeasuredOverhead(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	seedAllPatterns(t, srv, store)

	body := callCoach(t, srv, map[string]any{})
	burst := findingByPattern(t, body, "single_fact_burst")
	if burst == nil {
		t.Fatal("single_fact_burst finding missing")
	}
	basis, _ := burst["basis"].(string)
	if want := fmt.Sprintf("(N−1) × %d", srv.measuredMetaOverheadTokens()); !strings.Contains(basis, want) {
		t.Errorf("basis %q does not embed the formula %q", basis, want)
	}
}
