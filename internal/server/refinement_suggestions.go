// SPDX-License-Identifier: MIT

package server

import "fmt"

// #1634 v0.85: refinement-suggestion helpers for caller-surprised
// zero-result responses on query-shaped tools (search/trace/query/
// neighborhood). When pincher returns empty, the dominant agent
// behavior is to fall back to Read/Grep — the 0.78% retry-recovery
// rate on dogfood data (#1632) shows the agent almost never refines
// the query itself. The lever is concrete, re-issuable next_steps
// entries that name the exact knob to twist.
//
// Coverage notes per tool:
//   - search:       already covered by suggestEmptySearchNextSteps +
//                   verifyEmptySearchCause + the corpus fallthrough
//                   chain (drop min_confidence, drop kind/language,
//                   add wildcard, list as fallback). Acceptance #A1
//                   of #1634 is met today.
//   - symbol:       not-found path runs through errResultRich with
//                   search-by-name + list recovery. Covered.
//   - trace:        reverse-direction was missing — this file adds it.
//   - query:        pinchQL WHERE-drop deferred as #1634 follow-up
//                   (needs pinchQL parsing — wider scope than this PR).
//   - neighborhood: file-scoped, no widening knob; doesn't apply.

// suggestReverseTraceDirection returns a next_steps entry that
// re-issues the trace with the opposite direction. Only fires when
// the caller used a one-sided direction (inbound or outbound) and
// the BFS came back empty — symbol might still have edges in the
// other direction. Returns false when direction was "both" (already
// searched both sides) or unrecognized (fallback to "both" elsewhere
// in trace already covers that case).
//
// Pre-#1634 the empty-trace next_steps only suggested reading the
// symbol's own source via context — a confidently-wrong move when
// the caller asked "what does X call?" on a hub function that has
// 50 callers but zero callees. The reverse-direction retry converts
// that dead end into a productive next call with zero server-side
// BFS cost on the empty-side traversal.
func suggestReverseTraceDirection(seedID, direction string) (map[string]string, bool) {
	var reverse string
	switch direction {
	case "inbound":
		reverse = "outbound"
	case "outbound":
		reverse = "inbound"
	default:
		return nil, false
	}
	return map[string]string{
		"tool": "trace",
		"args": fmt.Sprintf(`{"id":%q,"direction":%q}`, seedID, reverse),
		"why":  fmt.Sprintf("trace returned 0 hops in direction=%q; try direction=%q — the other edge set is independent (callers and callees are separate)", direction, reverse),
	}, true
}
