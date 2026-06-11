// SPDX-License-Identifier: MIT

package db

// les.go — read-side telemetry queries backing LES, the Loop Efficiency
// Score (ADR LOOP_EFFICIENCY_METRIC). LES is computed entirely from
// telemetry the server already records: session_tool_calls per-call
// events, the per-session query-failure counters, hook_invocations, and
// the loop ledger. Everything here is reader-routed; LES never mutates
// what it measures.
//
// All windowed methods take [from, to) half-open intervals so a 7d
// window and its prior-7d comparison window never double-count a row.
//
// Anti-gaming (per the ADR): checkpoint counts used as iteration_cost
// denominators only include rows with a non-empty (post-TRIM) decision —
// checkpoint-stuffing with empty records doesn't move the score.

import "time"

// CountLoopCheckpointsBetween counts loop checkpoints across ALL
// projects with created_at in [from, to). Returns both the anti-gaming
// `counted` figure (non-empty decision after TRIM) and the raw `total`
// so basis strings can document the exclusion. created_at is stored as
// RFC3339 UTC text (AppendLoopCheckpoint), so lexicographic comparison
// is chronological. Reader-routed.
func (s *Store) CountLoopCheckpointsBetween(from, to time.Time) (counted, total int, err error) {
	err = s.ro.QueryRow(
		`SELECT COALESCE(SUM(CASE WHEN TRIM(decision) != '' THEN 1 ELSE 0 END), 0),
		        COUNT(*)
		   FROM loop_checkpoints
		  WHERE created_at >= ? AND created_at < ?`,
		from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339),
	).Scan(&counted, &total)
	return counted, total, err
}

// TokensUsedBetween sums recorded tokens_used and counts rows across
// session_tool_calls with ts in [from, to). This is LES's "what did the
// window cost" input — recorded per-call estimates, not modeled.
// Reader-routed.
func (s *Store) TokensUsedBetween(from, to time.Time) (tokens int64, calls int64, err error) {
	err = s.ro.QueryRow(
		`SELECT COALESCE(SUM(tokens_used), 0), COUNT(*)
		   FROM session_tool_calls
		  WHERE ts >= ? AND ts < ?`,
		from.UnixNano(), to.UnixNano(),
	).Scan(&tokens, &calls)
	return tokens, calls, err
}

// CountToolCallsBetween counts recorded calls to one tool with ts in
// [from, to). Backs LES recovery_load's investigate_failure component —
// the only recovery signal persisted across sessions (warning-code
// occurrences are in-memory counters; see server les.go). Reader-routed.
func (s *Store) CountToolCallsBetween(tool string, from, to time.Time) (int64, error) {
	var n int64
	err := s.ro.QueryRow(
		`SELECT COUNT(*) FROM session_tool_calls
		  WHERE tool = ? AND ts >= ? AND ts < ?`,
		tool, from.UnixNano(), to.UnixNano(),
	).Scan(&n)
	return n, err
}

// QueryMetricsBetween is QueryMetricsSince with a bounded window: sums
// the per-session query-failure counters across sessions whose
// last_seen falls inside [from, to). Same attribution caveat as
// QueryMetricsSince (whole-session counters keyed by last flush time) —
// LES basis strings document it. Reader-routed.
func (s *Store) QueryMetricsBetween(from, to time.Time) (QueryMetrics, error) {
	var qm QueryMetrics
	err := s.ro.QueryRow(
		`SELECT COALESCE(SUM(queries_total),0),
		        COALESCE(SUM(queries_zero_result),0),
		        COALESCE(SUM(queries_retried_succeeded),0),
		        COALESCE(SUM(tokens_burned_on_failures),0),
		        COALESCE(SUM(queries_zero_expected),0),
		        COALESCE(SUM(queries_zero_unexpected),0)
		 FROM sessions
		 WHERE last_seen >= ? AND last_seen < ?`,
		from.Unix(), to.Unix(),
	).Scan(&qm.QueriesTotal, &qm.QueriesZeroResult, &qm.QueriesRetriedSucceeded, &qm.TokensBurnedOnFailures,
		&qm.QueriesZeroExpected, &qm.QueriesZeroUnexpected)
	return qm, err
}

// HookRedirectIgnoredBetween counts redirect-decision hook invocations
// with ts in [from, to) that the agent saw and bypassed
// (took_recommendation = 0). Unresolved redirects (NULL
// took_recommendation) are excluded — same population rule as
// HookRedirectOutcomes. Reader-routed.
func (s *Store) HookRedirectIgnoredBetween(from, to time.Time) (int, error) {
	var n int
	err := s.ro.QueryRow(
		`SELECT COUNT(*) FROM hook_invocations
		  WHERE decision IN ('redirect','redirect_advisory')
		    AND took_recommendation = 0
		    AND ts >= ? AND ts < ?`,
		from.UnixNano(), to.UnixNano(),
	).Scan(&n)
	return n, err
}

// LoopOpeningSessionsBetween reports continuation fidelity's raw
// counts over session_tool_calls rows with ts in [from, to): how many
// distinct sessions recorded at least one call, and how many of those
// had the `loop` tool among their first firstN recorded calls.
//
// Approximations (documented in LES basis strings): events record tool
// names, not arguments, so a `loop` resume is indistinguishable from a
// `loop` checkpoint here; and a session spanning the window boundary
// has its "first calls" measured from the window edge. Reader-routed.
func (s *Store) LoopOpeningSessionsBetween(from, to time.Time, firstN int) (sessions, opening int, err error) {
	if firstN <= 0 {
		firstN = 3
	}
	err = s.ro.QueryRow(
		`WITH ranked AS (
		    SELECT session_id, tool,
		           ROW_NUMBER() OVER (PARTITION BY session_id ORDER BY ts ASC) AS rn
		      FROM session_tool_calls
		     WHERE ts >= ? AND ts < ?
		 )
		 SELECT COUNT(DISTINCT session_id),
		        COUNT(DISTINCT CASE WHEN tool = 'loop' AND rn <= ? THEN session_id END)
		   FROM ranked`,
		from.UnixNano(), to.UnixNano(), firstN,
	).Scan(&sessions, &opening)
	return sessions, opening, err
}

// LoopIterationSpan returns one loop's iteration_cost inputs: the
// number of checkpoints that count toward the denominator (non-empty
// decision after TRIM — the ADR's anti-gaming rule) and the loop's
// first checkpoint created_at (RFC3339), "" when the loop has no rows.
// Reader-routed.
func (s *Store) LoopIterationSpan(projectID, loopName string) (counted int, firstCreatedAt string, err error) {
	err = s.ro.QueryRow(
		`SELECT COALESCE(SUM(CASE WHEN TRIM(decision) != '' THEN 1 ELSE 0 END), 0),
		        COALESCE(MIN(created_at), '')
		   FROM loop_checkpoints
		  WHERE project_id = ? AND loop_name = ?`,
		projectID, loopName,
	).Scan(&counted, &firstCreatedAt)
	return counted, firstCreatedAt, err
}
