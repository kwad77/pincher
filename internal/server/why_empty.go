// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// #1391 v0.85: why_empty — cross-tool diagnostic composite. Phase 4
// composite #5 (final).
//
// Today (atomic). Agent receives an empty response, has to manually
// inspect _meta.empty_reason + _meta.diagnosis, then decide whether
// to re-issue with widened filters, re-index, or pivot. Today that
// decision requires either prompting the model with the catalog or
// firing several follow-up tools to figure out what went wrong:
//
//   list                            → which projects are indexed?
//   doctor                          → is the index healthy?
//   health                          → is the binary stale?
//   search broader filters          → was the query too narrow?
//
// Composite. why_empty(prior_empty_reason, prior_args?) returns:
//
//   {
//     "empty_reason":         "<the input>",
//     "diagnosis":            "<concrete recovery action>",
//     "recovery_steps":       [{"tool":"...", "args":"...", "why":"..."}],
//     "catalog_url":          "docs/empty-reasons.md#<anchor>",
//     "project_health":       {...}  // optional, when applicable
//   }
//
// The composite has no DB query of its own — it's a stateless
// look-up against the catalog in docs/empty-reasons.md, surfaced via
// the matching recovery-steps array. Read-only, idempotent.
//
// The agent's workflow:
//   1. Some tool returns _meta.empty_reason = "X"
//   2. Agent calls why_empty(prior_empty_reason="X")
//   3. why_empty returns the recovery_steps for that reason
//   4. Agent fires one of the recovery_steps
//
// Replaces the regex-match-prose + N tool calls + catalog-lookup
// sequence with one round trip.

// whyEmptyEntry is the structured recovery information for a single
// empty_reason code. Pre-baked from docs/empty-reasons.md so the
// composite doesn't need to read the markdown at runtime.
type whyEmptyEntry struct {
	Reason         string              `json:"empty_reason"`
	Title          string              `json:"title"`
	WhenItFires    string              `json:"when_it_fires"`
	RecoveryAction string              `json:"recovery_action"`
	RecoverySteps  []map[string]string `json:"recovery_steps"`
	CatalogAnchor  string              `json:"catalog_anchor"`
}

// whyEmptyCatalog mirrors docs/empty-reasons.md. Adding a new
// EmptyReason* constant requires bumping THIS table AND the doc; the
// TestWhyEmpty_CatalogCoversEveryConstant gate fails when they drift.
var whyEmptyCatalog = map[string]whyEmptyEntry{
	EmptyReasonNoProjectIndexed: {
		Reason:         EmptyReasonNoProjectIndexed,
		Title:          "Project arg didn't resolve",
		WhenItFires:    "The `project` argument resolved to nothing on disk OR the MCP session has no project and the caller didn't pass one explicitly.",
		RecoveryAction: "Call `list` to see what's indexed. If the intended project isn't there, call `index path=<absolute>` to add it.",
		RecoverySteps: []map[string]string{
			{"tool": "list", "args": "{}", "why": "see indexed projects"},
			{"tool": "index", "args": `{"path":"<absolute-path>"}`, "why": "add the project if it's not indexed"},
		},
		CatalogAnchor: "docs/empty-reasons.md#no_project_indexed",
	},
	EmptyReasonStaleIndex: {
		Reason:         EmptyReasonStaleIndex,
		Title:          "Index is stale vs running binary or working tree",
		WhenItFires:    "The running binary is newer than the project's schema_version_at_index OR the working tree drifted vs index without a re-index.",
		RecoveryAction: "Call `index force=true path=<project>` to refresh against current binary + tree. Pre-empt next time by checking `health` for `binary_stale`.",
		RecoverySteps: []map[string]string{
			{"tool": "index", "args": `{"path":"<project>","force":true}`, "why": "refresh against current binary"},
			{"tool": "health", "args": "{}", "why": "check binary_stale + schema drift"},
		},
		CatalogAnchor: "docs/empty-reasons.md#stale_index",
	},
	EmptyReasonUnsupportedLanguage: {
		Reason:         EmptyReasonUnsupportedLanguage,
		Title:          "Language has no extractor registered",
		WhenItFires:    "File extension was detected but no extractor exists for that language. Currently only Haskell (post-v0.63).",
		RecoveryAction: "Language-support gap, not a workflow bug. Either pick a different file or file an issue requesting the extractor. `architecture` lists which languages have edges.",
		RecoverySteps: []map[string]string{
			{"tool": "architecture", "args": `{"aspects":["languages"]}`, "why": "see supported-language list"},
		},
		CatalogAnchor: "docs/empty-reasons.md#unsupported_language",
	},
	EmptyReasonLowConfidenceExtractor: {
		Reason:         EmptyReasonLowConfidenceExtractor,
		Title:          "min_confidence floor excluded every match",
		WhenItFires:    "Extractor ran but every symbol fell below the min_confidence threshold for this language tier.",
		RecoveryAction: "Lower min_confidence: AST tier = 1.0, stable-regex = 0.85, approximate-regex = 0.70. Default for `search` / `query` / `dead_code` is 0.95 — too strict for regex-tier languages.",
		RecoverySteps: []map[string]string{
			{"tool": "search", "args": `{"query":"<term>","min_confidence":0.85}`, "why": "widen to stable-regex tier"},
			{"tool": "search", "args": `{"query":"<term>","min_confidence":0.70}`, "why": "widen to approximate-regex tier"},
		},
		CatalogAnchor: "docs/empty-reasons.md#low_confidence_extractor",
	},
	EmptyReasonSameFileOnly: {
		Reason:         EmptyReasonSameFileOnly,
		Title:          "Cross-file resolver unavailable for this language",
		WhenItFires:    "Language has same-file CALLS edges but no cross-file resolver yet. trace direction=out returns empty when crossing file boundaries.",
		RecoveryAction: "Scope to same-file (use `neighborhood`). Cross-file resolution is feature work — track via the language's tier-promotion issue.",
		RecoverySteps: []map[string]string{
			{"tool": "neighborhood", "args": `{"id":"<seed-id>"}`, "why": "same-file siblings (no cross-file dependency)"},
		},
		CatalogAnchor: "docs/empty-reasons.md#same_file_only",
	},
	EmptyReasonCrossFileUnavailable: {
		Reason:         EmptyReasonCrossFileUnavailable,
		Title:          "No edge graph for this language",
		WhenItFires:    "Extractor exists but emits zero edges of any kind (regex-tier pre-v0.62 for CALLS). No call graph to traverse.",
		RecoveryAction: "Use direct symbol search instead of edge traversal — `search` and `symbol` don't depend on the call graph.",
		RecoverySteps: []map[string]string{
			{"tool": "search", "args": `{"query":"<term>"}`, "why": "direct symbol lookup, no edges needed"},
		},
		CatalogAnchor: "docs/empty-reasons.md#cross_file_unavailable",
	},
	EmptyReasonQueryTooNarrow: {
		Reason:         EmptyReasonQueryTooNarrow,
		Title:          "Combined filters excluded everything",
		WhenItFires:    "Query is well-formed and corpus is non-empty, but kind + language + corpus + min_confidence + file_pattern filters excluded every match.",
		RecoveryAction: "Drop one filter at a time. The diagnosis names the most-restrictive filter — drop that one first.",
		RecoverySteps: []map[string]string{
			{"tool": "search", "args": `{"query":"<term>"}`, "why": "widest possible — no filters"},
		},
		CatalogAnchor: "docs/empty-reasons.md#query_too_narrow",
	},
	EmptyReasonNoResultsInCorpus: {
		Reason:         EmptyReasonNoResultsInCorpus,
		Title:          "Symbol doesn't appear in the indexed corpus",
		WhenItFires:    "Query is fine, filters are fine, but the symbol genuinely isn't in the index. No filter relaxation will rescue it.",
		RecoveryAction: "(1) Confirm spelling — try a partial query. (2) Confirm the project is scoped. (3) The symbol may live outside the indexed paths — index the parent directory or sibling repo.",
		RecoverySteps: []map[string]string{
			{"tool": "search", "args": `{"query":"<partial-term>"}`, "why": "widen with prefix to catch typos"},
			{"tool": "list", "args": "{}", "why": "confirm scope of indexed projects"},
		},
		CatalogAnchor: "docs/empty-reasons.md#no_results_in_corpus",
	},
	EmptyReasonCapDroppedAll: {
		Reason:         EmptyReasonCapDroppedAll,
		Title:          "Result cap (limit / offset / max_hops) dropped everything",
		WhenItFires:    "Candidates matched but every one was dropped by a cap. Canonical instance: offset past the last result page.",
		RecoveryAction: "Raise the cap or paginate back. The diagnosis names the specific cap that fired.",
		RecoverySteps: []map[string]string{
			{"tool": "search", "args": `{"query":"<term>","offset":0,"limit":50}`, "why": "rewind to start"},
		},
		CatalogAnchor: "docs/empty-reasons.md#cap_dropped_all",
	},
	EmptyReasonIncrementalNoChange: {
		Reason:         EmptyReasonIncrementalNoChange,
		Title:          "Indexer ran with nothing to do",
		WhenItFires:    "index ran but every file was unchanged (incremental fast path) OR reprocessed files had symbol-neutral edits. Not a bug.",
		RecoveryAction: "Expected. If verifying a recent edit, this confirms it didn't change the symbol surface. If re-extraction is genuinely needed (binary upgrade, extractor change), call `index force=true`.",
		RecoverySteps: []map[string]string{
			{"tool": "index", "args": `{"path":"<project>","force":true}`, "why": "force re-extraction if binary/extractor changed"},
		},
		CatalogAnchor: "docs/empty-reasons.md#incremental_no_change",
	},
	EmptyReasonAllFilesBlocked: {
		Reason:         EmptyReasonAllFilesBlocked,
		Title:          "Every file filtered as bloat",
		WhenItFires:    "Every discovered file was filtered by ast.ShouldSkip (lockfiles, minified bundles, source maps, generated code). Expected for vendor-only directories. A .pincherignore file (gitignore semantics) covering every source file produces the same shape — the walker drops ignored files before extraction.",
		RecoveryAction: "Either the path is wrong (you indexed node_modules/ or dist/), the path has nothing extractable, or an over-broad .pincherignore / .gitignore rule excluded everything. Check the path and any ignore files at the project root; if correct, pick a different directory.",
		RecoverySteps: []map[string]string{
			{"tool": "list", "args": "{}", "why": "compare project root against intent"},
		},
		CatalogAnchor: "docs/empty-reasons.md#all_files_blocked",
	},
	EmptyReasonExtractorEmittedNothing: {
		Reason:         EmptyReasonExtractorEmittedNothing,
		Title:          "Files processed but no symbols emitted",
		WhenItFires:    "Files weren't blocked but the extractor returned zero symbols. Usually a language-detection gap (extension not mapped) or malformed source.",
		RecoveryAction: "Call `doctor` to see extraction_failures with reason codes. If extraction_failures is empty too, the file extensions may not be mapped to any extractor.",
		RecoverySteps: []map[string]string{
			{"tool": "doctor", "args": "{}", "why": "extraction_failures lists parse/heuristic failures with reason codes"},
		},
		CatalogAnchor: "docs/empty-reasons.md#extractor_emitted_nothing",
	},
	EmptyReasonTargetNotResolved: {
		Reason:         EmptyReasonTargetNotResolved,
		Title:          "Composite target didn't resolve to a symbol",
		WhenItFires:    "A composite (plan_change / investigate_failure / etc.) accepted input that LOOKED valid for its resolution heuristic — file extension, name, or symbol-id shape — but couldn't find a matching symbol in the index. The data isn't missing; the input was the wrong shape for THIS project's index.",
		RecoveryAction: "Re-issue with a more specific target, or run `search` first to confirm what shape resolves. `list` confirms the project scope.",
		RecoverySteps: []map[string]string{
			{"tool": "search", "args": `{"query":"<your-target>"}`, "why": "confirm what BM25 surfaces — if 0 hits, the symbol isn't indexed; if hits, copy the id"},
			{"tool": "list", "args": "{}", "why": "verify the right project is scoped"},
		},
		CatalogAnchor: "docs/empty-reasons.md#target_not_resolved",
	},
}

func (s *Server) handleWhyEmpty(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	start, tool, args := beginCall(req)
	// #1579 v0.82: composite cancellation contract. why_empty has no
	// SQL loop so the entry-point check is the only check needed; the
	// catalog lookup is in-memory and bounded by the catalog size.
	if err := ctx.Err(); err != nil {
		return s.errResultRich("why_empty: ctx canceled before catalog lookup", nil), nil
	}

	priorReason := str(args, "prior_empty_reason")
	if strings.TrimSpace(priorReason) == "" {
		return s.errResultRich(
			"why_empty requires `prior_empty_reason` — pass the `_meta.empty_reason` value from the previous tool call's response. The enum values are documented in `docs/empty-reasons.md`.",
			[]map[string]string{
				{"tool": "why_empty", "args": `{"prior_empty_reason":"no_results_in_corpus"}`,
					"why": "diagnose 'symbol genuinely not in corpus'"},
				{"tool": "why_empty", "args": `{"prior_empty_reason":"query_too_narrow"}`,
					"why": "diagnose 'filters excluded everything'"},
				{"tool": "why_empty", "args": `{"prior_empty_reason":"stale_index"}`,
					"why": "diagnose 'index needs refresh'"},
			},
		), nil
	}

	entry, ok := whyEmptyCatalog[priorReason]
	if !ok {
		// Unknown reason — list every known reason so the caller can
		// see which they should have passed. Sort first: Go map
		// iteration order is randomised, which would otherwise produce
		// a different "Known values:" string per invocation and break
		// snapshot-stable test assertions (#1580).
		known := make([]string, 0, len(whyEmptyCatalog))
		for k := range whyEmptyCatalog {
			known = append(known, k)
		}
		sort.Strings(known)
		return s.errResultRich(
			fmt.Sprintf("why_empty: unknown empty_reason %q. Known values: %s. The catalog lives in `docs/empty-reasons.md` — add a new constant via internal/server/empty_reason.go if you've shipped one that's not yet listed.",
				priorReason, strings.Join(known, ", ")),
			[]map[string]string{
				{"tool": "doctor", "args": "{}",
					"why": "the empty result you saw may not have stamped empty_reason — check the diagnosis text instead"},
			},
		), nil
	}

	meta := map[string]any{
		"next_steps":     entry.RecoverySteps,
		"catalog_anchor": entry.CatalogAnchor,
	}

	data := map[string]any{
		"empty_reason":    entry.Reason,
		"title":           entry.Title,
		"when_it_fires":   entry.WhenItFires,
		"recovery_action": entry.RecoveryAction,
		"recovery_steps":  entry.RecoverySteps,
		"catalog_anchor":  entry.CatalogAnchor,
		"_meta":           meta,
	}
	return s.jsonResultWithMeta(data, start, tool, args, 0), nil
}
