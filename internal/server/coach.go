// SPDX-License-Identifier: MIT

package server

// coach.go — `coach` turns pincher's own usage telemetry into priced,
// retro-coaching findings: "you did X N times; pattern Y would have cost
// ~Z fewer tokens." Every number is computed from recorded telemetry
// (session_tool_calls events, the #241 query-failure counters, the #626
// hook_invocations table) and every finding carries a `basis` string
// documenting the arithmetic. No vibes: when a pattern can't be priced
// honestly from what's recorded, coach either degrades to counts-only or
// drops the pattern entirely.
//
// Pattern catalogue (what shipped and what didn't):
//
//  1. single_fact_burst — ≥3 search/symbol/trace calls each under 600
//     tokens_used. A single batched call (`symbols` with an ids array, or
//     one broader `search`) carries the same facts while paying the _meta
//     envelope once. Price: (N−1) × the measured _meta envelope overhead —
//     see measuredMetaOverheadTokens for the measurement.
//
//  2. unbudgeted_heavy_context — context/symbols responses over 2000
//     tokens_used. Field projection (`fields=`, `lite=true`) caps these
//     near 800 tokens. Price: Σ(tokens_used − 800) over the offending
//     events; tokens_used is the recorded per-call estimate, not a guess.
//
//  3. re-read of the same target — DROPPED. session_tool_calls stores
//     tool + tier + bytes + tokens + ts + request_id but no arguments
//     hash, so "same target twice" cannot be detected from the recorded
//     data. Honest rule: drop the finding rather than fake it. If a
//     future schema adds an args fingerprint, this pattern slots in here.
//
//  4. zero_result_churn — the #241/#1632 queries_zero_unexpected counter:
//     calls whose empty result surprised the caller. Price:
//     tokens_burned_on_failures, the recorded sum of tokens_used across
//     zero-result calls (an upper bound — it includes expected-empty
//     audit queries; the basis string says so).
//
//  5. hook_fall_through — hook_invocations redirect rows the agent saw
//     and bypassed (took_recommendation = 0). Priced from the v40 #1983
//     est_tokens_baseline/est_tokens_served columns when present; on a
//     pre-v40 schema (master today) the finding degrades to counts-only
//     with est_tokens_left_on_table = 0 and a basis that says counts-only.
//
// Fewer than coachMinCallsForFindings calls in the window returns empty
// findings plus a note — small samples would price noise, not patterns.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync/atomic"
	"time"

	"github.com/kwad77/pincher/internal/db"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// coachBurstTokenCeiling: a call under this many tokens_used is a
	// "single fact" — the response was mostly envelope, not payload.
	coachBurstTokenCeiling = 600
	// coachBurstMinCalls: bursts shorter than this aren't worth a finding.
	coachBurstMinCalls = 3
	// coachHeavyTokenFloor: a context/symbols response above this is
	// "unbudgeted" — the caller didn't project fields.
	coachHeavyTokenFloor = 2000
	// coachHeavyCapTarget: the response size field projection typically
	// lands near; the savings formula prices down to this floor.
	coachHeavyCapTarget = 800
	// coachMinCallsForFindings: below this many calls in the window,
	// coach reports "not enough telemetry" instead of extrapolating.
	coachMinCallsForFindings = 10
	coachDefaultLimit        = 5
	coachMaxLimit            = 20
	// coachEventScanCap bounds how many recorded events one coach call
	// reads back — generous (weeks of heavy use) but not unbounded.
	coachEventScanCap = 20000
)

// measuredMetaOverheadTokens measures the per-call _meta envelope cost
// this server stamps on every response. It builds the same field set
// jsonResultWithMeta writes on the hot path (tokens accounting, baseline
// method, latency, tier, plus the live capabilities slice when per-call
// advertisement is on) and runs it through db.ApproxTokens — the exact
// estimator that produced the recorded tokens_used values being mined.
// Measured from this process's real envelope, not a hardcoded guess;
// TestCoach_MetaOverheadIsMeasuredFromRealEnvelope pins the two paths
// against each other.
func (s *Server) measuredMetaOverheadTokens() int {
	meta := map[string]any{
		"tokens_used":      8888,
		"tokens_saved":     8888,
		"tokens_saved_pct": 88.8,
		"baseline_method":  baselineMethodFullFileRead,
		"latency_ms":       int64(88),
		"complexity_tier":  "standard",
	}
	if s.includeCapabilitiesPerCall {
		meta["capabilities"] = s.capabilities
	}
	b, err := json.Marshal(map[string]any{"_meta": meta})
	if err != nil {
		return 0
	}
	return db.ApproxTokens(string(b))
}

func (s *Server) handleCoach(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	start, tool, args := beginCall(req)
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	window := str(args, "window")
	if window == "" {
		window = "session"
	}
	if window != "session" && window != "7d" {
		return s.errResultRich(
			fmt.Sprintf("coach: unknown window %q — use \"session\" (default) or \"7d\"", window),
			[]map[string]string{
				{"tool": "coach", "args": `{"window":"session"}`, "why": "analyze the current session's recorded calls"},
				{"tool": "coach", "args": `{"window":"7d"}`, "why": "analyze the trailing 7 days across sessions"},
			},
		), nil
	}

	limit := intArg(args, "limit", coachDefaultLimit)
	if limit < 1 {
		limit = 1
	}
	if limit > coachMaxLimit {
		limit = coachMaxLimit
	}

	// `project` is accepted for interface consistency with the rest of
	// the surface, but telemetry rows are session-scoped, not
	// project-scoped — validate the name so a typo errors loudly
	// instead of being silently ignored.
	if p := str(args, "project"); p != "" {
		if _, err := s.resolveProjectID(p); err != nil {
			return s.errResultRich(err.Error(), []map[string]string{
				{"tool": "list", "args": `{}`, "why": "see every indexed project with its id + on-disk path"},
			}), nil
		}
	}

	// Make buffered events visible to the readers before mining — the
	// flush ticker only drains every 10s and coach should see the calls
	// the agent just made.
	s.drainToolCallEvents()

	now := time.Now()
	var since time.Time // zero for session scope; HookRedirectOutcomes keys off sessionID instead
	var events []db.ToolCallEvent
	var err error
	if window == "7d" {
		since = now.Add(-7 * 24 * time.Hour)
		events, err = s.store.ToolCallsSince(since, coachEventScanCap)
	} else {
		events, err = s.store.RecentToolCallsForSession(s.persistentSessionID, coachEventScanCap)
	}
	if err != nil {
		return errResult(fmt.Sprintf("coach: read telemetry: %v", err)), nil
	}

	data := map[string]any{
		"window":         window,
		"calls_analyzed": len(events),
		"findings":       []map[string]any{},
	}

	if len(events) < coachMinCallsForFindings {
		data["note"] = fmt.Sprintf(
			"not enough telemetry yet — %d call(s) recorded in the %s window; coach needs at least %d before patterns are worth pricing. Keep working; ask again later.",
			len(events), window, coachMinCallsForFindings)
		return s.jsonResultWithMeta(data, start, tool, args, 0), nil
	}

	findings := []map[string]any{}
	if f := s.coachSingleFactBurst(events); f != nil {
		findings = append(findings, f)
	}
	if f := coachUnbudgetedHeavyContext(events); f != nil {
		findings = append(findings, f)
	}
	if f := s.coachZeroResultChurn(window, since); f != nil {
		findings = append(findings, f)
	}
	if f := s.coachHookFallThrough(window, since); f != nil {
		findings = append(findings, f)
	}

	// Biggest measured win first; ties broken by pattern name so the
	// order is deterministic for tests and diffs.
	sort.SliceStable(findings, func(i, j int) bool {
		ei, _ := findings[i]["est_tokens_left_on_table"].(int64)
		ej, _ := findings[j]["est_tokens_left_on_table"].(int64)
		if ei != ej {
			return ei > ej
		}
		pi, _ := findings[i]["pattern"].(string)
		pj, _ := findings[j]["pattern"].(string)
		return pi < pj
	})
	if len(findings) > limit {
		findings = findings[:limit]
	}
	data["findings"] = findings
	return s.jsonResultWithMeta(data, start, tool, args, 0), nil
}

// coachSingleFactBurst prices pattern 1: many tiny single-fact lookups
// that one batched call would have carried.
func (s *Server) coachSingleFactBurst(events []db.ToolCallEvent) map[string]any {
	burstTools := map[string]bool{"search": true, "symbol": true, "trace": true}
	n := 0
	for _, e := range events {
		if burstTools[e.Tool] && e.TokensUsed < coachBurstTokenCeiling {
			n++
		}
	}
	if n < coachBurstMinCalls {
		return nil
	}
	overhead := s.measuredMetaOverheadTokens()
	est := int64(n-1) * int64(overhead)
	return map[string]any{
		"pattern":                  "single_fact_burst",
		"occurrences":              n,
		"est_tokens_left_on_table": est,
		"recommendation": fmt.Sprintf(
			"Batch the next cluster: one `symbols {\"ids\":[...]}` call (or one broader `search`) carries the same facts as %d separate sub-%d-token lookups while paying the response envelope once.",
			n, coachBurstTokenCeiling),
		"basis": fmt.Sprintf(
			"%d recorded search/symbol/trace calls each under %d tokens_used; a single batch pays the _meta envelope once instead of %d times. est = (N−1) × %d, where %d is this server's _meta envelope measured by serializing the same field set jsonResultWithMeta stamps (incl. live capabilities) through db.ApproxTokens.",
			n, coachBurstTokenCeiling, n, overhead, overhead),
	}
}

// coachUnbudgetedHeavyContext prices pattern 2: context/symbols responses
// the caller let run heavy instead of projecting fields.
func coachUnbudgetedHeavyContext(events []db.ToolCallEvent) map[string]any {
	heavyTools := map[string]bool{"context": true, "symbols": true}
	n := 0
	var est int64
	for _, e := range events {
		if heavyTools[e.Tool] && e.TokensUsed > coachHeavyTokenFloor {
			n++
			est += e.TokensUsed - coachHeavyCapTarget
		}
	}
	if n == 0 {
		return nil
	}
	return map[string]any{
		"pattern":                  "unbudgeted_heavy_context",
		"occurrences":              n,
		"est_tokens_left_on_table": est,
		"recommendation": fmt.Sprintf(
			"Project fields on the next heavy lookup: `context {\"id\":...,\"fields\":\"symbol\"}` (or `\"lite\":true`), `symbols {\"ids\":[...],\"fields\":\"id,name,signature\"}` — targets a ≤%d-token response.",
			coachHeavyCapTarget),
		"basis": fmt.Sprintf(
			"%d recorded context/symbols responses each over %d tokens_used. est = Σ(tokens_used − %d) over those events = %d; %d is the response size field projection typically lands under. tokens_used values are the recorded per-call estimates, not modeled.",
			n, coachHeavyTokenFloor, coachHeavyCapTarget, est, coachHeavyCapTarget),
	}
}

// coachZeroResultChurn prices pattern 4 from the #241/#1632 counters:
// queries whose empty result surprised the caller, and the tokens the
// telemetry recorded as burned on zero-result calls.
func (s *Server) coachZeroResultChurn(window string, since time.Time) map[string]any {
	var zeroUnexpected, burned int64
	var source string
	if window == "7d" {
		qm, err := s.store.QueryMetricsSince(since)
		if err != nil {
			return nil
		}
		zeroUnexpected = qm.QueriesZeroUnexpected
		burned = qm.TokensBurnedOnFailures
		source = "summed from flushed sessions rows with last_seen inside the 7d window"
	} else {
		zeroUnexpected = atomic.LoadInt64(&s.statsQueriesZeroUnexpected)
		burned = atomic.LoadInt64(&s.statsTokensBurned)
		source = "read from this session's live counters"
	}
	if zeroUnexpected == 0 {
		return nil
	}
	return map[string]any{
		"pattern":                  "zero_result_churn",
		"occurrences":              int(zeroUnexpected),
		"est_tokens_left_on_table": burned,
		"recommendation":           "After an unexpected empty result, call `why_empty` with the failing query before retrying blind — it returns the verified reshape (drop the kind filter, lower min_confidence, widen to a prefix query).",
		"basis": fmt.Sprintf(
			"queries_zero_unexpected = %d (calls whose empty result surprised the caller, per the #1632 classification); est = tokens_burned_on_failures = %d, the recorded sum of tokens_used across zero-result calls — an upper bound, since it also includes expected-empty audit queries. Counters %s.",
			zeroUnexpected, burned, source),
	}
}

// coachHookFallThrough prices pattern 5 from hook_invocations: redirects
// the agent saw and bypassed. Token pricing is gated on the v40 #1983
// per-row estimate columns; without them (pre-v40 schema) the finding is
// counts-only — a real count beats an invented token figure.
func (s *Server) coachHookFallThrough(window string, since time.Time) map[string]any {
	sessionID := ""
	scope := "trailing 7 days"
	if window == "session" {
		sessionID = s.persistentSessionID
		scope = "this session"
	}
	redirects, resolved, ignored, err := s.store.HookRedirectOutcomes(sessionID, since)
	if err != nil || ignored == 0 {
		return nil
	}
	var est int64
	var basis string
	if s.store.HookTokenColumnsPresent() {
		est, err = s.store.HookRedirectTokensLeftOnTable(sessionID, since)
		if err != nil {
			est = 0
		}
		basis = fmt.Sprintf(
			"%d of %d resolved redirect(s) in %s were bypassed (took_recommendation = 0; %d redirect(s) total, unresolved ones excluded). est = Σ max(est_tokens_baseline − est_tokens_served, 0) over the bypassed rows = %d — per-row estimates recorded by the hook at intercept time.",
			ignored, resolved, scope, redirects, est)
	} else {
		basis = fmt.Sprintf(
			"%d of %d resolved redirect(s) in %s were bypassed (took_recommendation = 0; %d redirect(s) total, unresolved ones excluded). This schema predates the per-row token-estimate columns (est_tokens_served/est_tokens_baseline), so the token figure is 0 — counts-only coaching, not an invented number.",
			ignored, resolved, scope, redirects)
	}
	return map[string]any{
		"pattern":                  "hook_fall_through",
		"occurrences":              ignored,
		"est_tokens_left_on_table": est,
		"recommendation":           "When the PreToolUse hook suggests a redirect (e.g. `context {\"id\":...,\"lite\":true}` instead of Read on an indexed file), take it — the suggestion is only emitted when the index already covers the file.",
		"basis":                    basis,
	}
}
