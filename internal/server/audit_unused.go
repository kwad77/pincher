// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// #1391 v0.83: audit_unused — dead-code composite with deep-trace
// confirmation. Phase 4 composite #3.
//
// Today (atomic). `dead_code` already bundles the in-graph caller
// check + the #493 interface-dispatch and #519/#561 receiver-type
// suppression. What it CAN'T tell you: whether a candidate is really
// unreachable past depth 1, or whether it's reachable via a dynamic
// caller the static call graph missed (function-value-as-field,
// reflection, interface dispatch the receiver-type check didn't
// catch). The agent today has to fire `trace direction=in depth=4`
// for each candidate to verify — N+1 round trips, N round-trip costs.
//
// Composite. `audit_unused(language, kinds, max_results, confirm_depth)`
// runs the existing dead_code path then, per candidate, fires a
// scoped inbound CALLS trace and classifies the result:
//
//   - confidence=high  → deep trace returned ZERO callers at confirm_depth.
//                        The candidate is unreachable past the existing
//                        suppression layer; safe to recommend deletion.
//   - confidence=medium → deep trace surfaced caller(s) but the depth-1
//                         direct caller is absent. Likely a dynamic
//                         path (function-value, interface-dispatch the
//                         receiver-type check missed). NOT safe to delete
//                         without reading the surfaced caller(s).
//   - confidence=low    → deep trace surfaced a depth-1 caller, meaning
//                         the SQL-level suppression let this through but
//                         a real CALLS edge exists. Almost always a
//                         resolver bug — file an issue rather than delete.
//
// Contract per docs/integrations/composite-tool-roadmap.md:
//   - Additive: dead_code stays callable unchanged.
//   - No internal MCP round-trips.
//   - Single envelope, single _meta block.
//   - empty_reason mandatory on zero-candidate / zero-result branches.
//   - Idempotent: read-only.

// auditUnusedCandidate is one ranked entry in the audit_unused response.
type auditUnusedCandidate struct {
	SymbolID      string                 `json:"symbol_id"`
	Name          string                 `json:"name"`
	QualifiedName string                 `json:"qualified_name"`
	Kind          string                 `json:"kind"`
	FilePath      string                 `json:"file_path"`
	StartLine     int                    `json:"start_line"`
	Language      string                 `json:"language"`
	Confidence    string                 `json:"confidence"` // high | medium | low
	TraceSummary  map[string]any         `json:"trace_summary"`
	Evidence      []string               `json:"evidence"`
}

// auditUnusedSummary is the rollup that accompanies the per-candidate list.
type auditUnusedSummary struct {
	CandidatesAudited                int `json:"candidates_audited"`
	DeepTraceConfirmedUnused         int `json:"deep_trace_confirmed_unused"`
	DeepTraceSurfacedDynamicCallers  int `json:"deep_trace_surfaced_dynamic_callers"`
	DeepTraceSurfacedDirectCallers   int `json:"deep_trace_surfaced_direct_callers"`
}

func (s *Server) handleAuditUnused(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	start, tool, args := beginCall(req)
	// #1579 v0.82: composite cancellation contract. Entry-point check.
	if err := ctx.Err(); err != nil {
		return s.errResultRich("audit_unused: ctx canceled before candidate query", nil), nil
	}

	projectID, errRes := s.mustProject(args)
	if errRes != nil {
		return errRes, nil
	}

	// #1847: a dead-code verdict is only sound against a COMPLETE call
	// graph. During the resolve pass, inbound CALLS edges are not yet
	// populated — so every function (including live entry points like
	// `runDoctorCLI`, reachable only from `func main()`) looks
	// unreachable. Pre-fix, audit_unused emitted `confidence: high` +
	// `deep_trace_confirms_unreachable` mid-pass, surfacing the
	// `index_in_progress` warning in `_meta` but not letting it touch
	// the confidence field — and `confidence` is what an agent acts on.
	// Refuse outright rather than ship a confidently-wrong verdict.
	if s.indexer != nil {
		if done, total, active := s.indexer.GetProgress(projectID); active {
			return s.errResultRich(
				fmt.Sprintf("audit_unused: indexer is mid-pass (%d/%d files) for this project — the call graph is incomplete, so every function looks unreachable. A dead-code verdict now would be confidently wrong; re-run once indexing finishes.", done, total),
				[]map[string]string{
					{"tool": "audit_unused", "args": "{}",
						"why": "retry after the resolve pass completes — the call graph is whole then"},
					{"tool": "health", "args": "{}",
						"why": "check indexing status (project.staleness_seconds, index_in_progress)"},
				},
			), nil
		}
	}

	language := str(args, "language")
	kindsRaw := str(args, "kinds")
	kinds := []string{}
	if kindsRaw != "" {
		for _, k := range strings.Split(kindsRaw, ",") {
			if k = strings.TrimSpace(k); k != "" {
				kinds = append(kinds, k)
			}
		}
	}
	if len(kinds) == 0 {
		kinds = []string{"Function", "Method"}
	}
	// #1780: Class/Type/Interface/Enum have no inbound type-reference
	// edges in the graph — a struct used only as `&T{}` looks dead. When
	// the caller widens `kinds` to a type-shaped kind the resulting
	// candidates are high-false-positive; flag it so a `high` confidence
	// row on a type isn't read as a safe-to-delete verdict.
	typeKindsRequested := []string{}
	for _, k := range kinds {
		switch k {
		case "Class", "Type", "Interface", "Enum":
			typeKindsRequested = append(typeKindsRequested, k)
		}
	}
	maxResults := intArg(args, "max_results", 20)
	if maxResults <= 0 {
		maxResults = 20
	}
	if maxResults > 100 {
		maxResults = 100
	}
	confirmDepth := intArg(args, "confirm_depth", 2)
	if confirmDepth < 1 {
		confirmDepth = 1
	}
	if confirmDepth > 4 {
		confirmDepth = 4
	}
	minConfidence := floatArg(args, "min_confidence", 0.95)
	minConfidence, _ = clampMinConfidence(minConfidence)

	// ── Step 1: get dead-code candidates from the existing path ─────────
	// GetDeadCode already applies the SQL-level suppression (no inbound
	// edges, non-entry-point, non-test, not exported, kind filter,
	// language filter, interface-dispatch suppression for Methods).
	raw, err := s.store.GetDeadCode(projectID, kinds, language, minConfidence, maxResults)
	if err != nil {
		return s.errResultRich(
			fmt.Sprintf("audit_unused: GetDeadCode failed: %v", err),
			[]map[string]string{
				{"tool": "doctor", "args": `{}`,
					"why": "check if the project has indexed any symbols at all + review extraction_failures"},
			},
		), nil
	}

	if len(raw) == 0 {
		meta := map[string]any{}
		diag := fmt.Sprintf(
			"no dead-code candidates surfaced for project %q with kinds=%v language=%q min_confidence=%.2f. Either the project really is clean (great!) or the filters are too tight — drop min_confidence to 0.85 to include stable-regex extractors. (Widening kinds to Class/Type is possible but those have no inbound type-reference edges, so type-kind candidates are high-false-positive and advisory-only.)",
			projectID, kinds, language, minConfidence)
		stampEmpty(meta, EmptyReasonNoResultsInCorpus, diag)
		meta["next_steps"] = []map[string]string{
			{"tool": "dead_code", "args": fmt.Sprintf(`{"language":%q,"min_confidence":0.85}`, language),
				"why": "widen the candidate set with the atomic tool to confirm 'clean' vs 'filter too tight'"},
			{"tool": "doctor", "args": `{}`,
				"why": "confirm the project's extraction succeeded — ghost projects produce zero dead-code candidates because the resolver pass never ran"},
		}
		data := map[string]any{
			"candidates": []auditUnusedCandidate{},
			"summary":    auditUnusedSummary{},
			"_meta":      meta,
		}
		return s.jsonResultWithMeta(data, start, tool, args, 0), nil
	}

	// ── Step 2: per-candidate deep-trace confirmation ───────────────────
	candidates := make([]auditUnusedCandidate, 0, len(raw))
	summary := auditUnusedSummary{
		CandidatesAudited: len(raw),
	}

	for _, sym := range raw {
		// #1579: per-iteration cancellation check. Each trace is depth-
		// bounded but a 100-candidate × depth-4 loop is real CPU work
		// the agent might cancel mid-flight.
		if err := ctx.Err(); err != nil {
			break
		}
		hops, err := s.store.TraceViaCTEScoped(projectID, sym.ID, "inbound", []string{"CALLS"}, confirmDepth)
		if err != nil {
			// Degrade gracefully — skip the deep-trace step but still
			// report the candidate at "medium" confidence with the
			// failure noted.
			candidates = append(candidates, auditUnusedCandidate{
				SymbolID:      sym.ID,
				Name:          sym.Name,
				QualifiedName: sym.QualifiedName,
				Kind:          sym.Kind,
				FilePath:      sym.FilePath,
				StartLine:     sym.StartLine,
				Language:      sym.Language,
				Confidence:    "medium",
				TraceSummary:  map[string]any{"error": err.Error()},
				Evidence: []string{
					"no_inbound_CALLS_edges_at_depth_0",
					"sql_suppression_passed",
					"deep_trace_failed_classification_indeterminate",
				},
			})
			continue
		}

		// Bucket hops by depth — excluding the seed itself.
		depth1Callers := 0
		var deeperCallers []string
		for _, h := range hops {
			if h.SymbolID == sym.ID {
				continue
			}
			if h.Depth == 1 {
				depth1Callers++
				deeperCallers = append(deeperCallers, h.SymbolID)
			} else {
				deeperCallers = append(deeperCallers, h.SymbolID)
			}
		}
		deeperCount := len(hops)
		// Subtract the seed if it appeared.
		for _, h := range hops {
			if h.SymbolID == sym.ID {
				deeperCount--
				break
			}
		}

		traceSummary := map[string]any{
			"confirm_depth":      confirmDepth,
			"depth_1_callers":    depth1Callers,
			"total_callers_seen": deeperCount,
		}
		if deeperCount > 0 && deeperCount <= 5 {
			// Surface up to 5 caller ids so the agent can read them
			// without a follow-up trace call. Bounded so the payload
			// doesn't blow up on widely-called false positives.
			ids := make([]string, 0, len(deeperCallers))
			seen := map[string]bool{}
			for _, id := range deeperCallers {
				if !seen[id] && id != sym.ID {
					seen[id] = true
					ids = append(ids, id)
				}
			}
			sort.Strings(ids)
			if len(ids) > 5 {
				ids = ids[:5]
			}
			traceSummary["caller_sample"] = ids
		}

		confidence := "high"
		evidence := []string{
			"no_inbound_CALLS_edges_at_depth_0",
			"sql_suppression_passed",
		}
		switch {
		case depth1Callers > 0:
			// SQL-level NOT EXISTS check said no inbound edges, but the
			// recursive CTE found a depth-1 caller. Almost always a
			// resolver bug — file rather than delete.
			confidence = "low"
			evidence = append(evidence, "deep_trace_surfaced_depth_1_caller_resolver_inconsistency")
			summary.DeepTraceSurfacedDirectCallers++
		case deeperCount > 0:
			// No depth-1 caller but deeper trace surfaced something —
			// dynamic path the static call graph missed.
			confidence = "medium"
			evidence = append(evidence, fmt.Sprintf("deep_trace_surfaced_depth_2_plus_caller_count_%d", deeperCount))
			summary.DeepTraceSurfacedDynamicCallers++
		default:
			// Deep trace confirms unreachable. Safe to recommend.
			confidence = "high"
			evidence = append(evidence, "deep_trace_confirms_unreachable")
			summary.DeepTraceConfirmedUnused++
		}

		candidates = append(candidates, auditUnusedCandidate{
			SymbolID:      sym.ID,
			Name:          sym.Name,
			QualifiedName: sym.QualifiedName,
			Kind:          sym.Kind,
			FilePath:      sym.FilePath,
			StartLine:     sym.StartLine,
			Language:      sym.Language,
			Confidence:    confidence,
			TraceSummary:  traceSummary,
			Evidence:      evidence,
		})
	}

	// Sort confidence high → medium → low → so the agent reads the
	// safest-to-delete first.
	confOrder := map[string]int{"high": 0, "medium": 1, "low": 2}
	sort.SliceStable(candidates, func(i, j int) bool {
		ci, cj := confOrder[candidates[i].Confidence], confOrder[candidates[j].Confidence]
		if ci != cj {
			return ci < cj
		}
		return candidates[i].QualifiedName < candidates[j].QualifiedName
	})

	meta := map[string]any{}
	meta["next_steps"] = []map[string]string{
		{"tool": "context", "args": fmt.Sprintf(`{"id":%q}`, candidates[0].SymbolID),
			"why": "read the top high-confidence candidate before deleting"},
	}
	warningsV2 := []map[string]any{}
	if len(typeKindsRequested) > 0 {
		warningsV2 = append(warningsV2, map[string]any{
			"code":     "type_kinds_no_reference_edges",
			"severity": "warning",
			"message":  fmt.Sprintf("kinds %v are type-shaped — the graph tracks no inbound type-reference edges (only CALLS/READS/WRITES/IMPORTS), so a struct used only as `&T{}` looks dead. Type-kind candidates here are high-false-positive and advisory-only; do not treat a `high` confidence row on a type as a safe-to-delete verdict.", typeKindsRequested),
		})
	}
	if summary.DeepTraceSurfacedDirectCallers > 0 {
		warningsV2 = append(warningsV2, map[string]any{
			"code":     "resolver_inconsistency_detected",
			"severity": "warning",
			"message":  fmt.Sprintf("%d candidate(s) had no inbound edges per the SQL check but deep trace surfaced a depth-1 caller — likely a resolver bug; file an issue rather than delete the symbol(s).", summary.DeepTraceSurfacedDirectCallers),
		})
	}
	if len(warningsV2) > 0 {
		meta["warnings_v2"] = warningsV2
	}

	data := map[string]any{
		"candidates": candidates,
		"summary":    summary,
		"_meta":      meta,
	}
	return s.jsonResultWithMeta(data, start, tool, args, 0), nil
}
