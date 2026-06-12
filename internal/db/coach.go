// SPDX-License-Identifier: MIT

package db

// coach.go — read-side telemetry queries backing the `coach` MCP tool.
// Coach mines what the server already records (session_tool_calls per-call
// events, per-session query-failure counters, hook_invocations) and prices
// findings from those recorded numbers. Everything here is reader-routed;
// coach never mutates telemetry.

import (
	"database/sql"
	"time"
)

// ToolCallsSince returns session_tool_calls rows with ts >= since across
// ALL sessions, newest first, capped at limit. Backs coach's 7d window —
// unlike RecentToolCallsForSession, the trailing-week view must span every
// process invocation that flushed events, not just the current one.
// Reader-routed.
func (s *Store) ToolCallsSince(since time.Time, limit int) ([]ToolCallEvent, error) {
	if limit <= 0 {
		limit = 10000
	}
	rows, err := s.ro.Query(
		`SELECT session_id, tool, complexity_tier, response_bytes,
		        tokens_used, tokens_saved, tokens_saved_pct, ts, request_id
		   FROM session_tool_calls
		  WHERE ts >= ?
		  ORDER BY ts DESC
		  LIMIT ?`,
		since.UnixNano(), limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ToolCallEvent{}
	for rows.Next() {
		var e ToolCallEvent
		var saved sql.NullInt64
		var pct sql.NullFloat64
		var tsNanos int64
		if err := rows.Scan(
			&e.SessionID, &e.Tool, &e.ComplexityTier, &e.ResponseBytes,
			&e.TokensUsed, &saved, &pct, &tsNanos, &e.RequestID,
		); err != nil {
			return nil, err
		}
		if saved.Valid {
			v := saved.Int64
			e.TokensSaved = &v
		}
		if pct.Valid {
			v := pct.Float64
			e.TokensSavedPct = &v
		}
		e.TS = time.Unix(0, tsNanos)
		out = append(out, e)
	}
	return out, rows.Err()
}

// QueryMetricsSince sums the per-session query-failure counters across
// sessions whose last_seen falls inside the window. Same shape as
// GetAllTimeQueryMetrics but windowed; backs coach's 7d zero-result-churn
// finding. Sessions are attributed by their last flush time (last_seen),
// so a long-lived session that flushed within the window contributes its
// whole-session counters — coach's basis strings document this as an
// approximation. Reader-routed.
func (s *Store) QueryMetricsSince(since time.Time) (QueryMetrics, error) {
	var qm QueryMetrics
	err := s.ro.QueryRow(
		`SELECT COALESCE(SUM(queries_total),0),
		        COALESCE(SUM(queries_zero_result),0),
		        COALESCE(SUM(queries_retried_succeeded),0),
		        COALESCE(SUM(tokens_burned_on_failures),0),
		        COALESCE(SUM(queries_zero_expected),0),
		        COALESCE(SUM(queries_zero_unexpected),0)
		 FROM sessions
		 WHERE last_seen >= ?`,
		since.Unix(),
	).Scan(&qm.QueriesTotal, &qm.QueriesZeroResult, &qm.QueriesRetriedSucceeded, &qm.TokensBurnedOnFailures,
		&qm.QueriesZeroExpected, &qm.QueriesZeroUnexpected)
	return qm, err
}

// HookRedirectOutcomes counts redirect-decision hook invocations in scope
// and how many of the resolved ones the agent ignored
// (took_recommendation = 0). Scope: when sessionID is non-empty the count
// is per-session; otherwise it is windowed by ts >= since. `resolved` is
// the denominator population (non-NULL took_recommendation) — redirects
// with no subsequent calls observed stay unresolved and are excluded, the
// same exclusion HookOverrideRate7d applies. Reader-routed.
func (s *Store) HookRedirectOutcomes(sessionID string, since time.Time) (redirects, resolved, ignored int, err error) {
	q := `SELECT
	         COALESCE(SUM(CASE WHEN decision IN ('redirect','redirect_advisory') THEN 1 ELSE 0 END), 0),
	         COALESCE(SUM(CASE WHEN decision IN ('redirect','redirect_advisory') AND took_recommendation IS NOT NULL THEN 1 ELSE 0 END), 0),
	         COALESCE(SUM(CASE WHEN decision IN ('redirect','redirect_advisory') AND took_recommendation = 0 THEN 1 ELSE 0 END), 0)
	       FROM hook_invocations`
	var row *sql.Row
	if sessionID != "" {
		row = s.ro.QueryRow(q+` WHERE session_id = ?`, sessionID)
	} else {
		row = s.ro.QueryRow(q+` WHERE ts >= ?`, since.UnixNano())
	}
	err = row.Scan(&redirects, &resolved, &ignored)
	return redirects, resolved, ignored, err
}

// HookTaskSpawns counts the Task-tool hook_invocations rows in scope —
// the PreToolUse hook's record of subagent spawns. This is coach's
// closest recorded proxy for Make-stage task units (router-loop plan
// §A4): the A1 metric's true denominator is Make-stage task units in
// the loop ledger, but checkpoint stage tags are free text inside
// `claim` and not session-joinable, while every hook-observed Task
// spawn already wrote a queryable row. Counts ALL Task rows regardless
// of decision (pass_through and advise_route rows are both spawns).
// Scope semantics match HookRedirectOutcomes: non-empty sessionID ⇒
// per-session, else windowed by ts >= since. Reader-routed.
func (s *Store) HookTaskSpawns(sessionID string, since time.Time) (int, error) {
	q := `SELECT COUNT(1) FROM hook_invocations WHERE tool_name = 'Task'`
	var row *sql.Row
	if sessionID != "" {
		row = s.ro.QueryRow(q+` AND session_id = ?`, sessionID)
	} else {
		row = s.ro.QueryRow(q+` AND ts >= ?`, since.UnixNano())
	}
	var n int
	err := row.Scan(&n)
	return n, err
}

// HookRouteAdvisories counts advise_route recruitment-advisory rows in
// scope (the router-loop §A2 / #2025 one-time advisory; decision =
// 'advise_route'). Backs coach's routing adoption section — the
// advisory count is the recruitment half of the take-rate signal; the
// full advisory→consult join is an offline analysis (hook rows carry
// the host session key, tool calls the pincher one). Scope semantics
// match HookRedirectOutcomes. Reader-routed.
func (s *Store) HookRouteAdvisories(sessionID string, since time.Time) (int, error) {
	q := `SELECT COUNT(1) FROM hook_invocations WHERE decision = 'advise_route'`
	var row *sql.Row
	if sessionID != "" {
		row = s.ro.QueryRow(q+` AND session_id = ?`, sessionID)
	} else {
		row = s.ro.QueryRow(q+` AND ts >= ?`, since.UnixNano())
	}
	var n int
	err := row.Scan(&n)
	return n, err
}

// HookTokenColumnsPresent reports whether the per-row token-estimate
// columns (est_tokens_served + baseline_tokens, the v41 hook-redirect-v2
// shape; the branch originally anticipated these as a v40-on-#1983
// est_tokens_* pair) exist on hook_invocations. Coach gates its hook
// fall-through pricing on this: with the columns it can sum measured
// estimates; on a pre-v41 schema it degrades to counts-only coaching
// instead of inventing a number. Reader-routed.
func (s *Store) HookTokenColumnsPresent() bool {
	rows, err := s.ro.Query(`PRAGMA table_info(hook_invocations)`)
	if err != nil {
		return false
	}
	defer rows.Close()
	found := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false
		}
		found[name] = true
	}
	return found["est_tokens_served"] && found["baseline_tokens"]
}

// HookRedirectTokensLeftOnTable sums max(baseline_tokens −
// est_tokens_served, 0) across ignored redirects in scope — the measured
// estimate of tokens the agent paid by bypassing the suggested call.
// Only meaningful when HookTokenColumnsPresent() is true; callers MUST
// gate on it (the query references the v41 columns directly).
// Scope semantics match HookRedirectOutcomes. Reader-routed.
func (s *Store) HookRedirectTokensLeftOnTable(sessionID string, since time.Time) (int64, error) {
	q := `SELECT COALESCE(SUM(MAX(baseline_tokens - est_tokens_served, 0)), 0)
	        FROM hook_invocations
	       WHERE decision IN ('redirect','redirect_advisory')
	         AND took_recommendation = 0`
	var row *sql.Row
	if sessionID != "" {
		row = s.ro.QueryRow(q+` AND session_id = ?`, sessionID)
	} else {
		row = s.ro.QueryRow(q+` AND ts >= ?`, since.UnixNano())
	}
	var n int64
	err := row.Scan(&n)
	return n, err
}
