// SPDX-License-Identifier: MIT

package server

// les.go — LES, the Loop Efficiency Score (ADR LOOP_EFFICIENCY_METRIC):
// pincher's success metric, computed entirely from telemetry the server
// already records. Free local compute, conclusions-only surfacing —
// LES is diagnostic, never a per-call gate.
//
// Four sub-metrics:
//
//  1. iteration_cost — recorded tokens_used in the window ÷ loop
//     checkpoints written in the window. Anti-gaming (per the ADR):
//     only checkpoints with a non-empty decision count toward the
//     denominator, so checkpoint-stuffing can't deflate the cost.
//
//  2. waste_rate — (queries_zero_unexpected + error envelopes +
//     budget_truncated warnings + ignored hook redirects) ÷ total
//     calls. Session scope has all four components (the error/warning
//     counters are in-memory, recorded at the request middleware and
//     the envelope hot path). 7d scope computes from what IS persisted
//     (zero-unexpected counters + hook_invocations) — error envelopes
//     and warning codes are not persisted across sessions, and the
//     basis string documents that omission rather than faking a number.
//
//  3. recovery_load — count of plan_stale + unpredicted_impact +
//     index_moved_since_checkpoint warnings + investigate_failure
//     calls. Session scope counts all four (in-memory warning-code
//     counters); 7d scope counts only investigate_failure (the one
//     persisted signal — session_tool_calls records tool names),
//     documented in the basis. Persisting warning-code occurrences
//     would need a schema change and is deliberately deferred (v2).
//
//  4. continuation_fidelity — sessions opening with a loop resume ÷
//     sessions on a ledger-bearing install. Approximated from
//     session_tool_calls ordering: events record tool names, not
//     arguments, so "opens with `loop` among the first 3 calls" stands
//     in for "opens with loop resume". The diff-context unchanged-hit
//     ratio the ADR mentions is not recorded anywhere — omitted, not
//     invented.
//
// Surfaces: `stats` (one compact LES line for the session + one for
// the trailing 7d), `coach` (a priced les_regression finding when a
// sub-metric moved week-over-week), and the `loop resume` brief (a
// one-line les_hint when the loop's own iteration_cost is computable).
// No new tool; no schema change — every input already existed.

import (
	"fmt"
	"sync/atomic"
	"time"
)

// lesOpeningCallWindow: a session "opens with" the loop ledger when the
// `loop` tool appears among its first this-many recorded calls — one
// orientation call (architecture/list) before the resume still counts
// as resuming first.
const lesOpeningCallWindow = 3

// lesSnapshot carries one window's LES sub-metrics plus the raw counts
// they were computed from, so renderers and coach can show arithmetic
// instead of bare conclusions. Each *OK flag marks the sub-metric
// computable: a nil-shaped value renders as "—", never as a fake zero.
type lesSnapshot struct {
	// iteration_cost
	iterationCost      float64 // tokens per counted checkpoint
	iterationCostOK    bool
	tokensUsed         int64
	checkpointsCounted int
	checkpointsTotal   int

	// waste_rate
	wasteRate   float64
	wasteRateOK bool
	wastedCalls int64
	totalCalls  int64
	wasteBasis  string

	// recovery_load (always computable; zero is a real answer)
	recoveryLoad  int64
	recoveryBasis string

	// continuation_fidelity
	fidelity      float64
	fidelityOK    bool
	fidelityBasis string
}

// recordLESSignals accumulates LES's in-memory session counters from
// one response envelope. Called on the jsonResultWithMeta hot path for
// non-nested calls only — batch sub-calls don't double-count (the
// outer envelope's own warnings are counted once). Cost: a map lookup
// plus a scan of warnings_v2 only when the response carries one.
func (s *Server) recordLESSignals(tool string, meta map[string]any, callSeq int64) {
	if tool == "investigate_failure" {
		atomic.AddInt64(&s.statsInvestigateFailureCalls, 1)
	}
	if tool == "loop" && callSeq <= lesOpeningCallWindow {
		atomic.StoreInt32(&s.lesLoopOpenedSession, 1)
	}
	v2, _ := meta["warnings_v2"].([]map[string]any)
	for _, w := range v2 {
		switch w["code"] {
		case "budget_truncated":
			atomic.AddInt64(&s.statsWarnBudgetTruncated, 1)
		case "plan_stale":
			atomic.AddInt64(&s.statsWarnPlanStale, 1)
		case "unpredicted_impact":
			atomic.AddInt64(&s.statsWarnUnpredictedImpact, 1)
		case "index_moved_since_checkpoint":
			atomic.AddInt64(&s.statsWarnIndexMoved, 1)
		}
	}
}

// lesSessionSnapshot computes LES for the current session: live atomic
// counters for tokens/waste/recovery, the loop ledger windowed from
// sessionStartedAt for iteration_cost, and the in-memory opened-with-
// loop flag for fidelity.
func (s *Server) lesSessionSnapshot(now time.Time) lesSnapshot {
	var snap lesSnapshot

	snap.tokensUsed = atomic.LoadInt64(&s.statsTokensUsed)
	if counted, total, err := s.store.CountLoopCheckpointsBetween(s.sessionStartedAt, now.Add(time.Second)); err == nil {
		snap.checkpointsCounted = counted
		snap.checkpointsTotal = total
		if counted > 0 {
			snap.iterationCost = float64(snap.tokensUsed) / float64(counted)
			snap.iterationCostOK = true
		}
	}

	zu := atomic.LoadInt64(&s.statsQueriesZeroUnexpected)
	errs := atomic.LoadInt64(&s.statsErrorEnvelopes)
	bt := atomic.LoadInt64(&s.statsWarnBudgetTruncated)
	var ignored int64
	if _, _, ig, err := s.store.HookRedirectOutcomes(s.persistentSessionID, time.Time{}); err == nil {
		ignored = int64(ig)
	}
	snap.wastedCalls = zu + errs + bt + ignored
	snap.totalCalls = atomic.LoadInt64(&s.statsCalls) + errs // error envelopes don't increment statsCalls
	if snap.totalCalls > 0 {
		snap.wasteRate = float64(snap.wastedCalls) / float64(snap.totalCalls)
		snap.wasteRateOK = true
	}
	snap.wasteBasis = fmt.Sprintf(
		"(queries_zero_unexpected %d + error envelopes %d + budget_truncated warnings %d + ignored hook redirects %d) / %d calls (%d success + %d error; error envelopes are counted at the request middleware, top-level calls only)",
		zu, errs, bt, ignored, snap.totalCalls, snap.totalCalls-errs, errs)

	ps := atomic.LoadInt64(&s.statsWarnPlanStale)
	ui := atomic.LoadInt64(&s.statsWarnUnpredictedImpact)
	im := atomic.LoadInt64(&s.statsWarnIndexMoved)
	inv := atomic.LoadInt64(&s.statsInvestigateFailureCalls)
	snap.recoveryLoad = ps + ui + im + inv
	snap.recoveryBasis = fmt.Sprintf(
		"plan_stale %d + unpredicted_impact %d + index_moved_since_checkpoint %d warnings + investigate_failure calls %d, all from this session's live counters",
		ps, ui, im, inv)

	if s.store.LoopLedgerNonEmpty(s.sessionID) {
		snap.fidelityOK = true
		if atomic.LoadInt32(&s.lesLoopOpenedSession) == 1 {
			snap.fidelity = 1
		}
		snap.fidelityBasis = fmt.Sprintf(
			"this session %s with a loop call inside its first %d calls (events record tool names, not actions — resume vs checkpoint indistinguishable); ledger-bearing install. The diff-context unchanged-hit ratio is not recorded — omitted.",
			map[bool]string{true: "opened", false: "did NOT open"}[snap.fidelity == 1], lesOpeningCallWindow)
	} else {
		snap.fidelityBasis = "no loop checkpoints recorded for this install — continuation_fidelity not applicable"
	}
	return snap
}

// lesWindowSnapshot computes LES over [from, to) from persisted
// telemetry — backs the stats 7d line and coach's week-over-week
// regression comparison. Components that only exist as in-memory
// session counters (error envelopes, warning-code occurrences) are
// omitted and documented, never guessed.
func (s *Server) lesWindowSnapshot(from, to time.Time) lesSnapshot {
	var snap lesSnapshot

	tokens, calls, err := s.store.TokensUsedBetween(from, to)
	if err != nil {
		return snap
	}
	snap.tokensUsed = tokens
	snap.totalCalls = calls
	if counted, total, err := s.store.CountLoopCheckpointsBetween(from, to); err == nil {
		snap.checkpointsCounted = counted
		snap.checkpointsTotal = total
		if counted > 0 {
			snap.iterationCost = float64(tokens) / float64(counted)
			snap.iterationCostOK = true
		}
	}

	var zu int64
	if qm, err := s.store.QueryMetricsBetween(from, to); err == nil {
		zu = qm.QueriesZeroUnexpected
	}
	var ignored int64
	if ig, err := s.store.HookRedirectIgnoredBetween(from, to); err == nil {
		ignored = int64(ig)
	}
	snap.wastedCalls = zu + ignored
	if calls > 0 {
		snap.wasteRate = float64(snap.wastedCalls) / float64(calls)
		snap.wasteRateOK = true
	}
	snap.wasteBasis = fmt.Sprintf(
		"(queries_zero_unexpected %d, summed from sessions rows with last_seen in the window + ignored hook redirects %d) / %d recorded calls. Error envelopes and budget_truncated warnings are session-scoped in-memory counters, not persisted — omitted from the window numerator (v2: persist warning-code counts).",
		zu, ignored, calls)

	inv, _ := s.store.CountToolCallsBetween("investigate_failure", from, to)
	snap.recoveryLoad = inv
	snap.recoveryBasis = fmt.Sprintf(
		"investigate_failure calls %d (session_tool_calls records tool names). plan_stale/unpredicted_impact/index_moved_since_checkpoint warning occurrences are in-memory session counters, not persisted — omitted (v2).",
		inv)

	if s.store.LoopLedgerNonEmpty("") {
		if sessions, opening, err := s.store.LoopOpeningSessionsBetween(from, to, lesOpeningCallWindow); err == nil && sessions > 0 {
			snap.fidelity = float64(opening) / float64(sessions)
			snap.fidelityOK = true
			snap.fidelityBasis = fmt.Sprintf(
				"%d of %d sessions with recorded calls in the window had a loop call inside their first %d calls (tool names only — resume vs checkpoint indistinguishable). The diff-context unchanged-hit ratio is not recorded — omitted.",
				opening, sessions, lesOpeningCallWindow)
		}
	}
	return snap
}

// lesCompactTokens renders a token count for the 21-char stats value
// budget: 833 → "833", 2500 → "2.5k", 12345 → "12k".
func lesCompactTokens(v float64) string {
	switch {
	case v >= 10000:
		return fmt.Sprintf("%.0fk", v/1000)
	case v >= 1000:
		return fmt.Sprintf("%.1fk", v/1000)
	default:
		return fmt.Sprintf("%.0f", v)
	}
}

// lesLineValue renders one snapshot as the compact stats value:
// "i:<tokens/iteration> w:<waste%> r:<recovery count> f:<fidelity>".
// Sub-metrics that aren't computable render "-" — distinguishable from
// a genuine zero. Session scope renders fidelity as yes/no (one
// session is a 0/1 observation, a percentage would overstate it).
func lesLineValue(snap lesSnapshot, sessionScope bool) string {
	i := "-"
	if snap.iterationCostOK {
		i = lesCompactTokens(snap.iterationCost)
	}
	w := "-"
	if snap.wasteRateOK {
		w = fmt.Sprintf("%.0f%%", snap.wasteRate*100)
	}
	f := "-"
	if snap.fidelityOK {
		if sessionScope {
			f = map[bool]string{true: "yes", false: "no"}[snap.fidelity == 1]
		} else {
			f = fmt.Sprintf("%.0f%%", snap.fidelity*100)
		}
	}
	return fmt.Sprintf("i:%s w:%s r:%d f:%s", i, w, snap.recoveryLoad, f)
}

// coachLESRegression compares LES over the trailing 7 days against the
// prior 7-day window and returns one les_regression finding naming the
// sub-metric that moved the most — priced when the regression is
// priceable from recorded numbers, counts-only otherwise. Nil when the
// prior window has no recorded calls (nothing honest to compare
// against) or nothing regressed. LES is diagnostic: this finding is
// retro-coaching, never a gate.
func (s *Server) coachLESRegression(now time.Time) map[string]any {
	// Half-open adjacent windows: prev's `to` is cur's `from`, so a
	// row never lands in both. The one-second slop on cur's upper edge
	// keeps a checkpoint written in the same second as this call
	// inside the window (created_at has second granularity).
	cur := s.lesWindowSnapshot(now.Add(-7*24*time.Hour), now.Add(time.Second))
	prev := s.lesWindowSnapshot(now.Add(-14*24*time.Hour), now.Add(-7*24*time.Hour))
	if prev.totalCalls == 0 {
		return nil
	}

	type candidate struct {
		subMetric      string
		est            int64
		recommendation string
		basis          string
	}
	var cands []candidate

	if cur.iterationCostOK && prev.iterationCostOK && cur.iterationCost > prev.iterationCost {
		delta := cur.iterationCost - prev.iterationCost
		est := int64(delta * float64(cur.checkpointsCounted))
		cands = append(cands, candidate{
			subMetric:      "iteration_cost",
			est:            est,
			recommendation: "Each loop iteration is costing more tokens than last week. Lean on the loop substrate: `loop resume` instead of transcript re-reads, `batch` for multi-probe iterations, `context lite=true`/`max_tokens` to bound probes.",
			basis: fmt.Sprintf(
				"iteration_cost rose %.0f → %.0f tokens/checkpoint week-over-week (recorded tokens_used ÷ non-empty-decision checkpoints: %d/%d this week vs %d/%d prior; empty-decision checkpoints never count, per ADR LOOP_EFFICIENCY_METRIC). est = (%.0f − %.0f) × %d checkpoints = %d.",
				prev.iterationCost, cur.iterationCost,
				cur.tokensUsed, cur.checkpointsCounted, prev.tokensUsed, prev.checkpointsCounted,
				cur.iterationCost, prev.iterationCost, cur.checkpointsCounted, est),
		})
	}
	if cur.wasteRateOK && prev.wasteRateOK && cur.wasteRate > prev.wasteRate {
		extraWasted := (cur.wasteRate - prev.wasteRate) * float64(cur.totalCalls)
		var avgTok int64
		if cur.totalCalls > 0 {
			avgTok = cur.tokensUsed / cur.totalCalls
		}
		est := int64(extraWasted * float64(avgTok))
		cands = append(cands, candidate{
			subMetric:      "waste_rate",
			est:            est,
			recommendation: "A bigger share of calls produced nothing usable this week. After an unexpected empty result call `why_empty` before retrying; take PreToolUse redirects when offered.",
			basis: fmt.Sprintf(
				"waste_rate rose %.1f%% → %.1f%% week-over-week. This week: %s Prior week: %d wasted of %d calls. est = (rate delta) × %d calls × %d avg recorded tokens/call ≈ %d — modeled from recorded averages, documented here.",
				prev.wasteRate*100, cur.wasteRate*100, cur.wasteBasis, prev.wastedCalls, prev.totalCalls,
				cur.totalCalls, avgTok, est),
		})
	}
	if cur.recoveryLoad > prev.recoveryLoad {
		cands = append(cands, candidate{
			subMetric:      "recovery_load",
			est:            0,
			recommendation: "More failure-recovery work than last week. Run `verify_change` right after edits (stale plans surface early) and checkpoint loops so `loop resume` carries state instead of re-investigation.",
			basis: fmt.Sprintf(
				"recovery_load rose %d → %d week-over-week. This week: %s Counts-only — recovery events carry no recorded token price; est is 0, not an invented number.",
				prev.recoveryLoad, cur.recoveryLoad, cur.recoveryBasis),
		})
	}
	if cur.fidelityOK && prev.fidelityOK && cur.fidelity < prev.fidelity {
		cands = append(cands, candidate{
			subMetric:      "continuation_fidelity",
			est:            0,
			recommendation: "Fewer sessions resumed from the loop ledger this week. Open ledger-backed work with `loop resume` — one bounded brief beats re-deriving state from transcripts.",
			basis: fmt.Sprintf(
				"continuation_fidelity fell %.0f%% → %.0f%% week-over-week. This week: %s Counts-only — the tokens burned re-deriving state aren't attributable from recorded data; est is 0.",
				prev.fidelity*100, cur.fidelity*100, cur.fidelityBasis),
		})
	}
	if len(cands) == 0 {
		return nil
	}
	best := cands[0]
	for _, c := range cands[1:] {
		if c.est > best.est {
			best = c
		}
	}
	return map[string]any{
		"pattern":                  "les_regression",
		"sub_metric":               best.subMetric,
		"occurrences":              len(cands),
		"est_tokens_left_on_table": best.est,
		"recommendation":           best.recommendation,
		"basis":                    best.basis + " Windows: trailing 7d vs the 7d before it, computed from persisted telemetry regardless of the coach window. LES is diagnostic — never a per-call gate.",
	}
}

// lesHintForLoop renders the one-line les_hint the resume brief
// carries when this loop's iteration_cost is computable: recorded
// tokens across all sessions since the loop's first checkpoint ÷ the
// loop's non-empty-decision checkpoints. Empty string when the inputs
// aren't recorded — the brief never carries a guessed number.
func (s *Server) lesHintForLoop(projectID, loopName string, now time.Time) string {
	counted, firstAt, err := s.store.LoopIterationSpan(projectID, loopName)
	if err != nil || counted == 0 || firstAt == "" {
		return ""
	}
	t0, err := time.Parse(time.RFC3339, firstAt)
	if err != nil {
		return ""
	}
	tokens, _, err := s.store.TokensUsedBetween(t0, now.Add(time.Second))
	if err != nil || tokens <= 0 {
		return ""
	}
	return fmt.Sprintf(
		"les: iteration_cost ≈ %d tokens/checkpoint (%d recorded tokens across all sessions since this loop's first checkpoint ÷ %d non-empty-decision checkpoints)",
		tokens/int64(counted), tokens, counted)
}
