// SPDX-License-Identifier: MIT

package server

import (
	"sort"
	"sync"
	"sync/atomic"
)

// #1630 v0.85: per-tool latency aggregation. Pre-fix `pincher stats`
// reported tokens_saved + tool_calls but NOT total time spent. A user
// investigating "why does pincher feel slow in my workflow?" couldn't
// answer it from pincher's own output.
//
// Cost-per-call without call frequency tells you the slow operation;
// cost × frequency tells you total time spent in that operation across
// a session — what actually impacts user-perceived performance. Some
// session might spend 47s in `search` across 80 calls (mean 590ms,
// p95 1200ms) while a single `doctor` call takes 9s — the dial that
// matters depends on the workflow shape.
//
// Scope this iteration:
//   - In-memory only. Atomic counters live on the Server struct via a
//     sync.Map. Resets on server restart; persistent surfacing across
//     restarts requires a schema migration on session_tool_calls and
//     is filed as a follow-up.
//   - Three aggregates per tool: call count, total ms, max ms. Avg is
//     derived (total / count); percentiles would require keeping
//     per-call samples — deferred to the schema-backed follow-up.
//   - Surfaced in `pincher stats` via a "BY TOOL (top-5 by total
//     time)" section, sorted descending. The top-5 cap matches the
//     existing hotspots truncation pattern.

// toolLatencyStats holds per-tool aggregates. All fields are accessed
// atomically. Stored as *toolLatencyStats in a sync.Map so the
// LoadOrStore path can install a fresh entry without locking the
// whole map.
type toolLatencyStats struct {
	count   int64
	totalMs int64
	maxMs   int64
}

// recordToolLatency accumulates a single tool call's latency into the
// per-tool aggregate. Called from jsonResultWithMeta after latency is
// computed. Empty tool name (direct-handler tests) is a no-op so the
// production map isn't polluted with "" entries.
func (s *Server) recordToolLatency(tool string, latencyMs int64) {
	if tool == "" {
		return
	}
	raw, _ := s.toolLatency.LoadOrStore(tool, &toolLatencyStats{})
	st := raw.(*toolLatencyStats)
	atomic.AddInt64(&st.count, 1)
	atomic.AddInt64(&st.totalMs, latencyMs)
	for {
		cur := atomic.LoadInt64(&st.maxMs)
		if latencyMs <= cur {
			return
		}
		if atomic.CompareAndSwapInt64(&st.maxMs, cur, latencyMs) {
			return
		}
	}
}

// toolLatencyRow is the rendered shape consumed by handleStats and the
// related test surface. Returned sorted descending by totalMs.
type toolLatencyRow struct {
	Tool    string
	Count   int64
	TotalMs int64
	MaxMs   int64
}

// topToolsByTotalTime returns up to `n` tools sorted descending by
// total milliseconds spent. Stable when entries tie on totalMs (sorts
// secondary by tool name lexicographic, third by count desc).
func (s *Server) topToolsByTotalTime(n int) []toolLatencyRow {
	if n <= 0 {
		return nil
	}
	rows := make([]toolLatencyRow, 0)
	s.toolLatency.Range(func(k, v any) bool {
		st := v.(*toolLatencyStats)
		rows = append(rows, toolLatencyRow{
			Tool:    k.(string),
			Count:   atomic.LoadInt64(&st.count),
			TotalMs: atomic.LoadInt64(&st.totalMs),
			MaxMs:   atomic.LoadInt64(&st.maxMs),
		})
		return true
	})
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].TotalMs != rows[j].TotalMs {
			return rows[i].TotalMs > rows[j].TotalMs
		}
		if rows[i].Count != rows[j].Count {
			return rows[i].Count > rows[j].Count
		}
		return rows[i].Tool < rows[j].Tool
	})
	if len(rows) > n {
		rows = rows[:n]
	}
	return rows
}

// (no method needed — sync.Map zero value is ready to use)
var _ sync.Map
