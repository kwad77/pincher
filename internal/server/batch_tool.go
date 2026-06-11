// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/kwad77/pincher/internal/db"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// batch (loop-substrate): one call carrying N read-only sub-queries,
// answered in one envelope under a shared token budget.
//
// The cost being removed is per-call ceremony, not per-call work: every
// MCP response pays one watermark + capabilities + transport round-trip
// regardless of how small the answer is. A loop agent that needs three
// greps' worth of answers per iteration pays that ceremony three times.
// `batch` pays it once: sub-queries run in order against the same
// in-process handlers, each entry ships a slim per-entry `_meta`
// ({empty_reason, tokens_used, warnings_v2} only), and the outer
// envelope carries the single full `_meta` — the one watermark, the one
// capabilities slice, the one stats accumulation for the whole batch.
//
// Sub-queries are isolated: one bad sub-query (unknown symbol ID, typo'd
// pinchQL) yields a per-entry `error` field and the rest of the batch
// proceeds. The shared `max_tokens` budget (default 4000) is decremented
// by each successful sub-result's approximate token cost; once exhausted,
// remaining sub-queries are skipped with `skipped:"budget_exhausted"`
// and a `budget_truncated` warning — deterministic, input-order
// degradation, same contract as the per-tool budgets from PR-5.

// batchMaxQueries caps the number of sub-queries per call. Twelve is
// deliberately roomy for a loop iteration's probe set (3-6 is typical)
// while still bounding worst-case latency for one MCP round-trip.
const batchMaxQueries = 12

// batchDefaultBudget is the shared response budget (approximate tokens,
// same chars/4 heuristic as _meta.tokens_used) when the caller omits
// max_tokens. Sized so a typical 3-5 sub-query batch fits unclipped.
const batchDefaultBudget = 4000

// batchAllowedSubTools is the read-only dispatch whitelist. No
// batch-in-batch (recursion is ceremony, not savings) and no writers
// (the outer tool declares idempotent=true; a write-capable sub-tool
// would silently break the router retry contract).
var batchAllowedSubTools = map[string]bool{
	"search":       true,
	"symbol":       true,
	"symbols":      true,
	"context":      true,
	"trace":        true,
	"query":        true,
	"neighborhood": true,
	"changes":      true,
}

// batchSlimMetaKeys are the only sub-`_meta` fields that survive into
// the per-entry `_meta`. Everything else (capabilities, watermark,
// complexity_tier, baseline_method, latency, ...) is the per-call
// chrome the batch envelope exists to deduplicate — the outer `_meta`
// carries it once.
var batchSlimMetaKeys = []string{"empty_reason", "tokens_used", "warnings_v2"}

func (s *Server) handleBatch(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	start, tool, args := beginCall(req)
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	rawQueries, _ := args["queries"].([]any)
	if len(rawQueries) == 0 {
		return s.errResultRich("batch requires `queries`: a non-empty array of {tool, args} sub-queries", []map[string]string{
			{"tool": "batch", "args": `{"queries":[{"tool":"search","args":{"query":"processOrder"}},{"tool":"trace","args":{"name":"processOrder"}}]}`,
				"why": "ask several read-only questions in one envelope under a shared max_tokens budget"},
		}), nil
	}
	if len(rawQueries) > batchMaxQueries {
		return s.errResultRich(
			fmt.Sprintf("batch accepts at most %d sub-queries per call (got %d) — split into multiple batch calls, or narrow the probe set: a loop iteration rarely needs more than a handful of answers at once", batchMaxQueries, len(rawQueries)),
			[]map[string]string{
				{"tool": "batch", "args": fmt.Sprintf(`{"queries":[/* first %d */]}`, batchMaxQueries),
					"why": "re-issue with the first chunk; follow up with the remainder in a second call"},
			}), nil
	}

	project := str(args, "project")
	maxTokens := maxTokensArg(args)
	if maxTokens == 0 {
		maxTokens = batchDefaultBudget
	}

	remaining := maxTokens
	results := make([]map[string]any, 0, len(rawQueries))
	skipped := 0
	var errIdx []int
	// Honest savings attribution: sum the numeric tokens_saved each
	// sub-handler computed (vs its own Read/Grep baseline) BEFORE the
	// sub-_meta is stripped, and flow the total up through the outer
	// jsonResultWithMeta — the single stats writer for batched traffic.
	summedSubTokensSaved := 0

	for i, rq := range rawQueries {
		qm, _ := rq.(map[string]any)
		subTool, _ := qm["tool"].(string)

		if !batchAllowedSubTools[subTool] {
			results = append(results, map[string]any{
				"index": i,
				"tool":  subTool,
				"error": fmt.Sprintf("sub-tool %q is not batchable — batch dispatches read-only query tools only: search, symbol, symbols, context, trace, query, neighborhood, changes", subTool),
			})
			errIdx = append(errIdx, i)
			continue
		}
		if remaining <= 0 {
			results = append(results, map[string]any{
				"index":   i,
				"tool":    subTool,
				"skipped": "budget_exhausted",
			})
			skipped++
			continue
		}

		subArgs, _ := qm["args"].(map[string]any)
		if subArgs == nil {
			subArgs = map[string]any{}
		}
		// Default the outer project into each sub-query lacking one so
		// callers don't repeat it N times.
		if project != "" {
			if _, ok := subArgs["project"]; !ok {
				subArgs["project"] = project
			}
		}
		// _nested marks the sub-call so jsonResultWithMeta skips session
		// stats accumulation — the outer batch call is the single source
		// of stats truth (no double counting).
		subArgs["_nested"] = true
		// Budget-aware sub-tools degrade gracefully inside what's left
		// of the shared budget instead of blowing past it.
		switch subTool {
		case "context", "symbols":
			if _, ok := subArgs["max_tokens"]; !ok {
				subArgs["max_tokens"] = remaining
			}
		}

		marshaled, err := json.Marshal(subArgs)
		if err != nil {
			results = append(results, map[string]any{
				"index": i,
				"tool":  subTool,
				"error": fmt.Sprintf("could not marshal sub-args: %v", err),
			})
			errIdx = append(errIdx, i)
			continue
		}
		subReq := &mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{
				Name:      subTool,
				Arguments: json.RawMessage(marshaled),
			},
		}

		var res *mcp.CallToolResult
		var herr error
		switch subTool {
		case "search":
			res, herr = s.handleSearch(ctx, subReq)
		case "symbol":
			res, herr = s.handleSymbol(ctx, subReq)
		case "symbols":
			res, herr = s.handleSymbols(ctx, subReq)
		case "context":
			res, herr = s.handleContext(ctx, subReq)
		case "trace":
			res, herr = s.handleTrace(ctx, subReq)
		case "query":
			res, herr = s.handleQuery(ctx, subReq)
		case "neighborhood":
			res, herr = s.handleNeighborhood(ctx, subReq)
		case "changes":
			res, herr = s.handleChanges(ctx, subReq)
		}
		if herr != nil {
			// Handler-level Go errors are transport-shaped (context
			// cancellation) — propagate rather than mask per-entry.
			return nil, herr
		}

		text := ""
		if res != nil && len(res.Content) > 0 {
			if tc, ok := res.Content[0].(*mcp.TextContent); ok {
				text = tc.Text
			}
		}
		if res == nil || res.IsError {
			// Isolation: one bad sub-query never kills the batch.
			results = append(results, map[string]any{
				"index": i,
				"tool":  subTool,
				"error": text,
			})
			errIdx = append(errIdx, i)
			continue
		}

		var body map[string]any
		if err := json.Unmarshal([]byte(text), &body); err != nil {
			results = append(results, map[string]any{
				"index": i,
				"tool":  subTool,
				"error": fmt.Sprintf("could not decode sub-result JSON: %v", err),
			})
			errIdx = append(errIdx, i)
			continue
		}

		// Slim the sub-_meta: keep only the load-bearing per-answer
		// fields; the duplicated ceremony (capabilities / watermark /
		// complexity chrome) is exactly what batch removes.
		slim := map[string]any{}
		if subMeta, ok := body["_meta"].(map[string]any); ok {
			if ts, ok := subMeta["tokens_saved"].(float64); ok && ts > 0 {
				summedSubTokensSaved += int(ts)
			}
			for _, k := range batchSlimMetaKeys {
				if v, ok := subMeta[k]; ok {
					slim[k] = v
				}
			}
			delete(body, "_meta")
		}
		entry := map[string]any{
			"index":  i,
			"tool":   subTool,
			"result": body,
		}
		if len(slim) > 0 {
			entry["_meta"] = slim
		}
		results = append(results, entry)

		remarshaled, _ := json.Marshal(body)
		remaining -= db.ApproxTokens(string(remarshaled))
	}

	data := map[string]any{
		"results": results,
		"count":   len(results),
		"budget": map[string]any{
			"max_tokens":      maxTokens,
			"spent_approx":    maxTokens - remaining,
			"skipped_queries": skipped,
		},
	}
	if skipped > 0 {
		attachWarningStructured(data, "budget_truncated", WarningSeverityWarning,
			fmt.Sprintf("%d sub-quer%s skipped after the %d-token shared budget was exhausted — raise max_tokens, trim earlier sub-queries with fields=, or split the batch", skipped, pluralIES(skipped), maxTokens),
			map[string]any{"skipped_queries": skipped, "max_tokens": maxTokens})
	}
	if len(errIdx) > 0 {
		attachWarningStructured(data, "batch_sub_errors", WarningSeverityWarning,
			fmt.Sprintf("sub-quer%s at index %v returned errors — see the per-entry `error` field; the rest of the batch completed normally", pluralIES(len(errIdx)), errIdx),
			map[string]any{"error_indexes": errIdx})
	}

	return s.jsonResultWithMeta(data, start, tool, args, summedSubTokensSaved), nil
}

// pluralIES returns "y" for 1 and "ies" otherwise — for "sub-query" /
// "sub-queries" message grammar without fmt gymnastics at call sites.
func pluralIES(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
