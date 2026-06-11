// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/kwad77/pincher/internal/db"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// loop-substrate PR-10: verify_change — the loop's post-edit gate in
// one call.
//
// The post-edit checkpoint of an agent loop asks three questions:
// "did my edit do what I planned, what do I run, what broke." Before
// this composite that was a `changes` call (blast radius + ranked
// tests), a mental diff against the `plan_change` output that's long
// gone from the context window, and a `dead_code` sweep the agent
// almost never remembers to run. verify_change answers all three in
// ONE envelope:
//
//  1. The changes analysis — the same diff → changed-symbols →
//     blast-radius → tests_to_run core handleChanges uses, extracted
//     into AnalyzeChanges (changes_analysis.go, also shared by the
//     `pincher test-impacted` CLI) so the consumers share one
//     implementation instead of one calling the other (mirrors how
//     plan_change composes store primitives, not handlers).
//  2. tests_to_run, ranked by overlap — produced by the same core.
//  3. Predicted-vs-actual blast radius — plan_change stashes its full
//     depth-1 caller set in a bounded in-memory plan cache; pass
//     `target` and verify_change reports predicted_callers /
//     actual_impacted / unpredicted_impact, warning via warnings_v2
//     code=unpredicted_impact when the edit reached callers the plan
//     never saw. A plan captured at a different index generation is
//     reported stale (code=plan_stale) instead of producing a bogus
//     diff.
//  4. New-dead-symbols check — the dead_code SQL path restricted to
//     the changed files; symbols with zero inbound edges NOW are
//     labelled possibly_orphaned_by_change (advisory).
//
// Contract per docs/integrations/composite-tool-roadmap.md:
//   - Additive: atomic tools stay callable unchanged.
//   - No internal MCP round-trips.
//   - Single envelope, single _meta block.
//   - Idempotent: read-only.

// ─────────────────────────────────────────────────────────────────────────────
// Plan cache (written by plan_change, read by verify_change)
// ─────────────────────────────────────────────────────────────────────────────

// planCacheMax bounds the in-memory plan cache to the newest N entries.
// 32 plans comfortably covers a long agent session (one plan per edit
// site); the cache is advisory, so silent eviction of older plans just
// degrades verify_change to "no plan cached" — never wrong data.
const planCacheMax = 32

// planCacheEntry is one stashed plan_change run.
type planCacheEntry struct {
	target     string   // raw plan_change `target` arg
	file       string   // resolved file path
	primaryID  string   // first resolved symbol id
	depth1IDs  []string // FULL depth-1 inbound caller ids (pre-transport-truncation)
	generation int64    // index generation at plan time (the watermark's gN)
}

// stashPlan appends a plan to the bounded cache, evicting oldest-first.
// Allocation-light by design: ids were already materialised by the
// plan_change caller; no maps, no per-entry goroutines, one small slice.
func (s *Server) stashPlan(e planCacheEntry) {
	s.planMu.Lock()
	defer s.planMu.Unlock()
	s.planCache = append(s.planCache, e)
	if len(s.planCache) > planCacheMax {
		s.planCache = s.planCache[len(s.planCache)-planCacheMax:]
	}
}

// lookupPlan returns the NEWEST cached plan whose raw target, resolved
// primary symbol id, or resolved file path matches target. Linear scan
// is fine — the cache is ≤32 entries by construction.
func (s *Server) lookupPlan(target string) (planCacheEntry, bool) {
	s.planMu.Lock()
	defer s.planMu.Unlock()
	for i := len(s.planCache) - 1; i >= 0; i-- {
		e := s.planCache[i]
		if e.target == target || e.primaryID == target || e.file == target {
			return e, true
		}
	}
	return planCacheEntry{}, false
}

// ─────────────────────────────────────────────────────────────────────────────
// Handler
// ─────────────────────────────────────────────────────────────────────────────

// verifyMaxOrphans caps the possibly_orphaned list. The check is
// advisory; past a handful of rows the agent should run dead_code /
// audit_unused properly rather than read a long tail here.
const verifyMaxOrphans = 10

func (s *Server) handleVerifyChange(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	start, tool, args := beginCall(req)
	// Composite cancellation contract (#1579): entry-point check before
	// forking the git subprocess + per-symbol trace loops.
	if err := ctx.Err(); err != nil {
		return s.errResultRich("verify_change: ctx canceled", nil), nil
	}

	projectID, errRes := s.mustProject(args)
	if errRes != nil {
		return errRes, nil
	}
	root, err := s.resolveProjectRoot(projectID)
	if err != nil {
		return errResult(err.Error()), nil
	}

	scope := str(args, "scope")
	if scope == "" {
		scope = "unstaged"
	}
	target := str(args, "target")
	budget := maxTokensArg(args)

	// ── 1+2. Changes analysis: blast radius + ranked tests ──────────────
	analysis, diffErr := AnalyzeChanges(ctx, s.store, s.indexer, projectID, root, scope, 3)
	if diffErr != nil {
		return s.errResultRich(
			fmt.Sprintf("git diff failed: %v", diffErr),
			[]map[string]string{
				{"tool": "verify_change", "args": `{"scope":"unstaged"}`,
					"why": "default: working-tree changes not yet staged"},
				{"tool": "verify_change", "args": `{"scope":"staged"}`,
					"why": "changes added via git add (pre-commit verification)"},
				{"tool": "verify_change", "args": `{"scope":"all"}`,
					"why": "every dirty path including untracked files"},
				{"tool": "verify_change", "args": `{"scope":"base:master"}`,
					"why": "committed-only diff vs master's merge-base. Use the actual base branch name (master/main/develop/…)"},
			}), nil
	}

	var warningsV2 []map[string]any

	// ── 3. Predicted-vs-actual blast radius (plan cache) ────────────────
	var planComparison map[string]any
	if target != "" {
		entry, found := s.lookupPlan(target)
		switch {
		case !found:
			// No plan cached → comparison field absent, with a note so
			// the agent learns the plan→edit→verify contract instead of
			// wondering where the field went.
			warningsV2 = append(warningsV2, map[string]any{
				"code":     "no_plan_cached",
				"severity": "info",
				"message":  fmt.Sprintf("no plan_change plan cached for target %q — plan_comparison omitted. Run plan_change before the edit (same target) to enable predicted-vs-actual verification. The cache holds the newest %d plans for this server process.", target, planCacheMax),
			})
		default:
			var gen int64
			if s.indexer != nil {
				gen = s.indexer.Generation()
			}
			predicted := append([]string(nil), entry.depth1IDs...)
			sort.Strings(predicted)
			if entry.generation != gen {
				// The symbol graph moved between plan and verify — the
				// plan's depth-1 set was computed against a different
				// graph, so an actual-minus-predicted diff would be
				// bogus. Report staleness instead.
				warningsV2 = append(warningsV2, map[string]any{
					"code":     "plan_stale",
					"severity": "warning",
					"message":  fmt.Sprintf("plan for %q was captured at index generation g%d but the index is now at g%d — the symbol graph moved between plan and verify, so a predicted-vs-actual diff would be bogus. Re-run plan_change against the current index for a trustworthy comparison.", entry.target, entry.generation, gen),
				})
				planComparison = map[string]any{
					"target":             entry.target,
					"predicted_callers":  predicted,
					"stale":              true,
					"plan_generation":    entry.generation,
					"current_generation": gen,
				}
			} else {
				// Fresh plan: actual = the depth-1 inbound callers of
				// the changed symbols in the plan's file. Depth-1 on
				// both sides keeps the comparison apples-to-apples —
				// the plan stashed depth-1 predictions.
				actualSet := analysis.DirectCallers[entry.file]
				actual := make([]string, 0, len(actualSet))
				predictedSet := make(map[string]bool, len(predicted))
				for _, id := range predicted {
					predictedSet[id] = true
				}
				unpredicted := []string{}
				for id := range actualSet {
					actual = append(actual, id)
					if !predictedSet[id] {
						unpredicted = append(unpredicted, id)
					}
				}
				sort.Strings(actual)
				sort.Strings(unpredicted)
				planComparison = map[string]any{
					"target":             entry.target,
					"predicted_callers":  predicted,
					"actual_impacted":    actual,
					"unpredicted_impact": unpredicted,
					"stale":              false,
					// False when the plan's file isn't in this diff at
					// all — actual is then trivially empty and the
					// comparison says nothing about the edit.
					"target_file_in_diff": actualSet != nil,
				}
				if len(unpredicted) > 0 {
					warningsV2 = append(warningsV2, map[string]any{
						"code":     "unpredicted_impact",
						"severity": "warning",
						"message":  fmt.Sprintf("the edit reaches %d direct caller(s) the plan never predicted: %v — inspect them before declaring done", len(unpredicted), unpredicted),
					})
				}
			}
		}
	}

	// ── 4. New-dead-symbols check (orphans in the changed files) ────────
	// dead_code's SQL path restricted to the changed files: symbols
	// with zero inbound edges NOW. Method kind is EXCLUDED: the static
	// call graph is dispatch-blind — a Method whose only callers go
	// through an interface value carries zero direct inbound edges and
	// would be reported orphaned even though it's live at runtime.
	// (GetDeadCode's #493 interface-name carve-out softens that class
	// but doesn't eliminate it; the per-symbol interface-satisfaction
	// helper that would make Method results trustworthy lives on a
	// different branch.) Functions only; the label says "possibly" and
	// means it.
	possiblyOrphaned := []map[string]any{}
	orphanTotal := 0
	if len(analysis.ChangedFiles) > 0 {
		orphans, oErr := s.store.GetDeadCodeForFiles(projectID, analysis.ChangedFiles, []string{"Function"}, 0.95, verifyMaxOrphans+1)
		if oErr == nil {
			orphanTotal = len(orphans)
			for _, sym := range orphans {
				if len(possiblyOrphaned) >= verifyMaxOrphans {
					break
				}
				possiblyOrphaned = append(possiblyOrphaned, map[string]any{
					"id":        sym.ID,
					"name":      sym.Name,
					"kind":      sym.Kind,
					"file_path": sym.FilePath,
					"label":     "possibly_orphaned_by_change",
				})
			}
		}
	}

	// ── Envelope assembly ────────────────────────────────────────────────
	impacted := analysis.Impacted
	riskCounts := map[string]int{"CRITICAL": 0, "HIGH": 0, "MEDIUM": 0, "LOW": 0}
	for _, item := range impacted {
		riskCounts[item.Risk]++
	}

	changedSymRows := []map[string]any{}
	for _, sym := range analysis.ChangedSymbols {
		if len(changedSymRows) >= changesMaxList {
			break
		}
		changedSymRows = append(changedSymRows, map[string]any{
			"id": sym.ID, "name": sym.Name, "kind": sym.Kind, "file_path": sym.FilePath,
		})
	}
	testsToRun := analysis.TestsToRun
	if len(testsToRun) > changesMaxList {
		testsToRun = testsToRun[:changesMaxList]
	}

	meta := map[string]any{}
	nextSteps := []map[string]string{}
	if len(testsToRun) > 0 {
		nextSteps = append(nextSteps, map[string]string{
			"tool": "context",
			"args": fmt.Sprintf(`{"id":%q}`, testsToRun[0].ID),
			"why":  fmt.Sprintf("run the top-ranked test first — %s covers %d of the changed symbol(s); read it here, then execute it", testsToRun[0].Name, testsToRun[0].Overlap),
		})
	}
	nextSteps = append(nextSteps, map[string]string{
		"tool": "loop",
		"args": `{"action":"checkpoint","name":"<loop-name>","claim":"<what the edit was supposed to do>","decision":"<Accept/Defer/Reject + why>","evidence":"verify_change: tests_to_run + plan_comparison above"}`,
		"why":  "checkpoint the verified result so the loop's next iteration (or a fresh session) starts from it instead of re-verifying",
	})
	meta["next_steps"] = nextSteps

	if len(analysis.ChangedFiles) == 0 {
		stampEmpty(meta, EmptyReasonNoResultsInCorpus, fmt.Sprintf(
			"scope=%q has no changed files — nothing to verify. If you just committed, use scope=\"base:<branch>\"; if you staged, use scope=\"staged\".", scope))
	}

	data := map[string]any{
		"summary": map[string]any{
			"changed_files":     len(analysis.ChangedFiles),
			"changed_symbols":   len(analysis.ChangedSymbols),
			"total_impacted":    len(impacted),
			"tests_to_run":      len(analysis.TestsToRun),
			"critical":          riskCounts["CRITICAL"],
			"high":              riskCounts["HIGH"],
			"possibly_orphaned": orphanTotal,
		},
		"changed_symbols":   changedSymRows,
		"tests_to_run":      testsToRun,
		"possibly_orphaned": possiblyOrphaned,
	}
	if planComparison != nil {
		data["plan_comparison"] = planComparison
	}

	// ── Budget enforcement ───────────────────────────────────────────────
	// Deterministic degradation order, bulk lists first and decision-
	// critical fields (summary, plan_comparison verdict, warnings) last:
	// changed_symbols → possibly_orphaned → tests_to_run (floor 3) →
	// plan_comparison id lists (counts survive). Same chars/4 token
	// heuristic as _meta.tokens_used (budget.go).
	if budget > 0 {
		overBudget := func() bool {
			b, mErr := json.Marshal(data)
			return mErr == nil && db.ApproxTokens(string(b)) > budget
		}
		trimmedSections := []string{}
		trims := []struct {
			name string
			trim func() bool // returns true if it removed anything
		}{
			{"changed_symbols", func() bool {
				if len(changedSymRows) == 0 {
					return false
				}
				changedSymRows = changedSymRows[:len(changedSymRows)/2]
				data["changed_symbols"] = changedSymRows
				return true
			}},
			{"possibly_orphaned", func() bool {
				if len(possiblyOrphaned) == 0 {
					return false
				}
				possiblyOrphaned = possiblyOrphaned[:len(possiblyOrphaned)/2]
				data["possibly_orphaned"] = possiblyOrphaned
				return true
			}},
			{"tests_to_run", func() bool {
				if len(testsToRun) <= 3 {
					return false
				}
				testsToRun = testsToRun[:max(3, len(testsToRun)/2)]
				data["tests_to_run"] = testsToRun
				return true
			}},
			{"plan_comparison", func() bool {
				if planComparison == nil {
					return false
				}
				trimmed := false
				for _, k := range []string{"predicted_callers", "actual_impacted", "unpredicted_impact"} {
					if ids, ok := planComparison[k].([]string); ok && len(ids) > 0 {
						planComparison[k+"_count"] = len(ids)
						delete(planComparison, k)
						trimmed = true
					}
				}
				return trimmed
			}},
		}
		for overBudget() {
			progressed := false
			for i := range trims {
				if trims[i].trim() {
					if len(trimmedSections) == 0 || trimmedSections[len(trimmedSections)-1] != trims[i].name {
						trimmedSections = append(trimmedSections, trims[i].name)
					}
					progressed = true
					break
				}
			}
			if !progressed {
				break // nothing left to cut; summary + warnings always ship
			}
		}
		if len(trimmedSections) > 0 {
			warningsV2 = append(warningsV2, map[string]any{
				"code":     "budget_truncated",
				"severity": "info",
				"message":  fmt.Sprintf("max_tokens=%d trimmed sections %v — summary keeps the true counts; re-issue without max_tokens (or with a larger budget) for the full lists", budget, trimmedSections),
				"sections": trimmedSections,
			})
		}
	}

	if len(warningsV2) > 0 {
		meta["warnings_v2"] = warningsV2
	}
	data["_meta"] = meta

	// Honest savings baseline (same shape as changes): the agent's
	// alternative was re-reading every changed file plus every impacted
	// symbol's file after the edit.
	responseJSON, _ := json.Marshal(data)
	paths := make([]string, 0, len(analysis.ChangedSymbols)+len(impacted))
	for _, sym := range analysis.ChangedSymbols {
		if sym.FilePath != "" {
			paths = append(paths, sym.FilePath)
		}
	}
	for _, item := range impacted {
		if item.FilePath != "" {
			paths = append(paths, item.FilePath)
		}
	}
	return s.jsonResultWithMeta(data, start, tool, args, s.savedVsFileSizesSession(projectID, root, paths, responseJSON)), nil
}
