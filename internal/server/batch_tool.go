// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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
//
// M13 adds chain mode (`from` + `quiet`, below): server-side pipelining
// for the dependent case, additive to this contract — independent
// queries behave byte-identically to before.

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

// batchDefaultFields are the conclusion-density field projections
// injected into sub-queries that don't name their own (payload diet,
// 2026-06). Measured motivation: across three trust-tax loopbench
// sessions, batch sub-results totalled ~121 KB; `symbol` sub-queries
// carried the full 17-field standalone shape on 38 of 39 real calls
// (nobody asked for byte offsets, extraction confidence, or
// qualified_name — together ~17% of symbol row bytes), and coached
// agents hand-passed `fields:"id,name,file_path"` on 51 of 52 search
// sub-queries to dodge the default per-row snippet. Inside a batch the
// caller is asking several questions at once and synthesizing — the
// per-row default should be locator + answer, not the full standalone
// record.
//
// Graceful degradation contract: explicit caller args ALWAYS win.
//   - caller sets `fields`        → honored verbatim, no injection
//   - caller sets `fields:"*"`    → full standalone payload (the arg is
//     dropped before dispatch, so the sub-tool's own nil-set = all-fields
//     path runs, with no unknown-field warning)
//   - search caller sets `snippet_lines` → no injection (asking to size
//     snippets is asking for snippets)
// Standalone (non-batch) tool behavior is byte-identical to before.
//
// Sub-tools deliberately absent: `symbols` already defaults to its own
// compact set (compactSymbolsFieldSet), `trace` rows are already lean,
// `context`/`query`/`neighborhood`/`changes` use top-level (not per-row)
// fields semantics where an injected projection could drop whole answer
// sections.
var batchDefaultFields = map[string]string{
	// Locator shape: enough to cite (file+name+kind) and to chain
	// (id feeds from:{select:"top_id"|"ids"}, file_path feeds "files").
	"search": "id,name,kind,file_path",
	// Answer shape: the body the caller asked for (source, with its
	// docstring and signature) plus the citation fields — minus the
	// byte-offset / confidence / export chrome.
	"symbol": "id,name,kind,file_path,start_line,end_line,signature,docstring,source",
}

// batchSlimMetaKeys are the only sub-`_meta` fields that survive into
// the per-entry `_meta`. Everything else (capabilities, watermark,
// complexity_tier, baseline_method, latency, ...) is the per-call
// chrome the batch envelope exists to deduplicate — the outer `_meta`
// carries it once.
var batchSlimMetaKeys = []string{"empty_reason", "tokens_used", "warnings_v2"}

// ─────────────────────────────────────────────────────────────────────────────
// Chain mode (loop-substrate M13): server-side pipelining
// ─────────────────────────────────────────────────────────────────────────────
//
// Locality bias: compute below the envelope is free. Without chaining,
// when sub-query N's input is sub-query N-1's output (search → context
// is the canonical loop step), the agent ferries the intermediate ID
// through its own context window and pays for it twice — once on the
// way out, once on the way back in. A `from` clause keeps the
// intermediate local: the server splices a NAMED selection from an
// earlier result into the dependent sub-query's args, and `quiet:true`
// lets the upstream body be omitted from the response entirely (its
// entry keeps a `selected` pointer so the response stays auditable).
//
// Scope discipline (v1): named selectors only — top_id / ids / files.
// No path language. Multi-value fan-out into single-value args is not
// implemented; `select:"ids"` pairs with the `symbols` sub-tool's
// `ids` arg only.

// chainIDsCap bounds an `ids` selection. Twenty matches the typical
// search/trace page; past that the upstream query should be narrowed,
// not the splice widened.
const chainIDsCap = 20

// chainFilesCap bounds a `files` selection.
const chainFilesCap = 10

// chainIntoDefaults maps the dependent sub-tool to the arg key a
// selection splices into when `from.into` is omitted. Tools whose
// primary arg is plan-shaped free text (search, query, ...) have no
// default — the caller must name `into` explicitly.
var chainIntoDefaults = map[string]string{
	"context": "id",
	"symbol":  "id",
	"symbols": "ids",
	"trace":   "id",
}

// chainSpec is one validated `from` clause: splice the `sel` selection
// over queries[upstream]'s result into this sub-query's args at key
// `into`. multi marks selectors that yield a value LIST.
type chainSpec struct {
	upstream int
	sel      string
	into     string
	multi    bool
}

// parseChainFrom validates the optional `from` clause of sub-query i.
// Returns (nil, "") when absent. A non-empty message is a validation
// failure — the whole batch rich-errors BEFORE any execution, so a
// forward reference never burns budget on sub-queries that ran ahead
// of a doomed chain.
func parseChainFrom(i int, qm map[string]any) (*chainSpec, string) {
	rawFrom, ok := qm["from"]
	if !ok || rawFrom == nil {
		return nil, ""
	}
	fm, ok := rawFrom.(map[string]any)
	if !ok {
		return nil, fmt.Sprintf("queries[%d].from must be an object: {\"query\": <earlier index>, \"select\": \"top_id|ids|files\", \"into\": \"<arg key>\"?}", i)
	}
	subTool, _ := qm["tool"].(string)
	idxF, isNum := fm["query"].(float64)
	if !isNum || idxF != float64(int(idxF)) {
		return nil, fmt.Sprintf("queries[%d].from.query must be the integer index of an earlier sub-query", i)
	}
	upstream := int(idxF)
	if upstream < 0 || upstream >= i {
		return nil, fmt.Sprintf("queries[%d].from.query=%d must reference a LOWER index (0..%d) — sub-queries run strictly in declaration order, so a forward (or self) reference can never have a result to select from", i, upstream, i-1)
	}
	sel, _ := fm["select"].(string)
	switch sel {
	case "top_id", "ids", "files":
	default:
		return nil, fmt.Sprintf("queries[%d].from.select=%q is not a named selector — v1 supports exactly top_id (first result's stable symbol id), ids (all result ids, deduped, capped at %d), files (distinct file_path values, capped at %d); there is no path language", i, sel, chainIDsCap, chainFilesCap)
	}
	into, _ := fm["into"].(string)
	if into == "" {
		into = chainIntoDefaults[subTool]
	}
	if into == "" {
		return nil, fmt.Sprintf("queries[%d].from.into must be named explicitly for sub-tool %q — into defaults exist only for context/symbol/trace (\"id\") and symbols (\"ids\")", i, subTool)
	}
	multi := sel != "top_id"
	if multi && !(subTool == "symbols" && into == "ids") {
		return nil, fmt.Sprintf("queries[%d]: multi-value select %q cannot splice into single-value key %q of %q — fan-out is not implemented in v1; select:\"ids\" pairs with the `symbols` sub-tool's \"ids\" arg only (use select:\"top_id\" to feed a single-value arg)", i, sel, into, subTool)
	}
	return &chainSpec{upstream: upstream, sel: sel, into: into, multi: multi}, ""
}

// chainCandidates walks the known result shapes of the batchable
// sub-tools — search-style `results[]`, trace `hops[].nodes[]`,
// context's `symbol`, and the flat `symbol`-tool body — and returns
// deduped symbol ids and file paths in result order, so top_id is
// exactly the id the agent would have copied by hand from results[0].
func chainCandidates(body map[string]any) (ids, files []string) {
	seenID := map[string]bool{}
	seenFile := map[string]bool{}
	add := func(m map[string]any) {
		if id, _ := m["id"].(string); id != "" && !seenID[id] {
			seenID[id] = true
			ids = append(ids, id)
		}
		if fp, _ := m["file_path"].(string); fp != "" && !seenFile[fp] {
			seenFile[fp] = true
			files = append(files, fp)
		}
	}
	if rows, ok := body["results"].([]any); ok {
		for _, r := range rows {
			if m, ok := r.(map[string]any); ok {
				add(m)
			}
		}
	}
	if hops, ok := body["hops"].([]any); ok {
		for _, h := range hops {
			hm, _ := h.(map[string]any)
			nodes, _ := hm["nodes"].([]any)
			for _, n := range nodes {
				if m, ok := n.(map[string]any); ok {
					add(m)
				}
			}
		}
	}
	if sym, ok := body["symbol"].(map[string]any); ok {
		add(sym)
	}
	if id, _ := body["id"].(string); id != "" {
		add(body)
	}
	return ids, files
}

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

	// Chain validation pre-pass (M13): every `from` clause is checked
	// BEFORE any execution. A forward reference, unknown selector, or
	// multi-into-single splice fails the whole call up front — never
	// after earlier sub-queries already spent budget.
	chains := make([]*chainSpec, len(rawQueries))
	for i, rq := range rawQueries {
		qm, _ := rq.(map[string]any)
		if qm == nil {
			continue
		}
		spec, vErr := parseChainFrom(i, qm)
		if vErr != "" {
			return s.errResultRich(vErr, []map[string]string{
				{"tool": "batch", "args": `{"queries":[{"tool":"search","args":{"query":"processOrder"},"quiet":true},{"tool":"context","from":{"query":0,"select":"top_id"}}]}`,
					"why": "chain shape: a later sub-query splices a named selection (top_id | ids | files) from an EARLIER result into its args server-side, so the intermediate never crosses the token envelope"},
			}), nil
		}
		chains[i] = spec
	}

	project := str(args, "project")
	maxTokens := maxTokensArg(args)
	if maxTokens == 0 {
		maxTokens = batchDefaultBudget
	}

	remaining := maxTokens
	results := make([]map[string]any, 0, len(rawQueries))
	// bodies keeps each sub-result server-side for downstream `from`
	// selection — including quiet entries, whose body never ships.
	// nil = errored / skipped / not yet run; downstream chains off a
	// nil body skip with upstream_empty rather than guessing a call.
	bodies := make([]map[string]any, len(rawQueries))
	quietAt := make([]bool, len(rawQueries))
	var chainTrims []map[string]any
	skipped := 0
	var errIdx []int
	// Honest savings attribution: sum the numeric tokens_saved each
	// sub-handler computed (vs its own Read/Grep baseline) BEFORE the
	// sub-_meta is stripped, and flow the total up through the outer
	// jsonResultWithMeta — the single stats writer for batched traffic.
	summedSubTokensSaved := 0

	// Session-delta interplay (envelope-compression integration): the
	// sub-handlers below run through the same _meta stamping as
	// top-level calls, so the FIRST sub-call would consume the
	// session's one full-capabilities emission — and batch then strips
	// it from the slim per-entry _meta, meaning the consumer would
	// never see the advertisement at all. Restore the delta ledger to
	// its pre-batch state after the sub-calls so the OUTER envelope
	// (the one full _meta this tool's contract promises) emits exactly
	// as if the sub-calls had never stamped. Worst case under a
	// concurrent race is a double-emit of the full slice — the same
	// safe direction the ledger already guarantees (never under-emits).
	// The defer covers early error returns; the success path restores
	// explicitly BEFORE jsonResultWithMeta builds the outer envelope
	// (defer would run after the outer stamp — too late).
	preBatchCapsFP, _ := s.lastEmittedCaps.Load().(string)
	defer s.lastEmittedCaps.Store(preBatchCapsFP)

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

		// Chain resolution (M13): splice the named selection from the
		// upstream result into this sub-query's args. If the upstream
		// errored, was skipped, or the selector found nothing, this
		// entry skips with upstream_empty — never a guessed call.
		if spec := chains[i]; spec != nil {
			var selected any
			if up := bodies[spec.upstream]; up != nil {
				ids, files := chainCandidates(up)
				switch spec.sel {
				case "top_id":
					if len(ids) > 0 {
						selected = ids[0]
					}
				case "ids":
					if len(ids) > chainIDsCap {
						chainTrims = append(chainTrims, map[string]any{
							"query_index": i, "select": spec.sel,
							"matched": len(ids), "kept": chainIDsCap,
						})
						ids = ids[:chainIDsCap]
					}
					if len(ids) > 0 {
						selected = ids
					}
				case "files":
					if len(files) > chainFilesCap {
						chainTrims = append(chainTrims, map[string]any{
							"query_index": i, "select": spec.sel,
							"matched": len(files), "kept": chainFilesCap,
						})
						files = files[:chainFilesCap]
					}
					if len(files) > 0 {
						selected = files
					}
				}
			}
			if selected == nil {
				results = append(results, map[string]any{
					"index":    i,
					"tool":     subTool,
					"skipped":  "upstream_empty",
					"upstream": spec.upstream,
				})
				continue
			}
			// A single id feeding `symbols`' array arg arrives as a
			// one-element list — the sub-tool's own schema, unchanged.
			if sv, isStr := selected.(string); isStr && subTool == "symbols" && spec.into == "ids" {
				subArgs[spec.into] = []any{sv}
			} else {
				subArgs[spec.into] = selected
			}
			// Provenance: a quiet upstream's body is omitted from the
			// response, so stamp what was passed on at its entry —
			// the chain stays auditable without echoing the body.
			if quietAt[spec.upstream] {
				upEntry := results[spec.upstream]
				if prev, has := upEntry["selected"]; has {
					if list, isList := prev.([]any); isList {
						upEntry["selected"] = append(list, selected)
					} else {
						upEntry["selected"] = []any{prev, selected}
					}
				} else {
					upEntry["selected"] = selected
				}
			}
		}

		// Default the outer project into each sub-query lacking one so
		// callers don't repeat it N times.
		if project != "" {
			if _, ok := subArgs["project"]; !ok {
				subArgs["project"] = project
			}
		}
		// Conclusion-density defaults (payload diet): inject the lean
		// per-row projection unless the caller named their own. See
		// batchDefaultFields for the contract; `fields:"*"` is the
		// explicit full-payload escape (dropped here so the sub-tool's
		// nil-set = all-fields path runs without an unknown-field
		// warning).
		if df, ok := batchDefaultFields[subTool]; ok {
			if raw, has := subArgs["fields"]; has {
				if fs, _ := raw.(string); strings.TrimSpace(fs) == "*" {
					delete(subArgs, "fields")
				}
			} else {
				_, sizedSnippets := subArgs["snippet_lines"]
				if !(subTool == "search" && sizedSnippets) {
					subArgs["fields"] = df
				}
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
		// v1.4.0 release-review hardening: a quiet sub-query's body
		// never reaches the caller's context window, so it must not
		// touch the #655 diff cache — neither recording "served" nor
		// short-circuiting to {unchanged:true} (which would make the
		// suppressed body silently unobtainable to the caller). The
		// budget injection above already keeps budgeted context
		// sub-calls out of the cache (full-fidelity gate); diff=false
		// makes the quiet case explicit and future-proof.
		if q, _ := qm["quiet"].(bool); q && subTool == "context" {
			subArgs["diff"] = false
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
			// Carry the caller's session identity through to sub-handlers:
			// a batch sub-result ships to the same context window as a
			// direct call, so connection-keyed state (the #655 diff cache)
			// must see the same identity, not an anonymous one.
			Session: req.Session,
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
		bodies[i] = body

		if q, _ := qm["quiet"].(bool); q {
			// quiet (M13): the body stays server-side for downstream
			// chains; only the slim provenance entry ships. The shared
			// budget is charged for what actually ships — keeping the
			// suppressed body off the budget is the whole point
			// (intermediates never cross the token envelope).
			quietAt[i] = true
			entry := map[string]any{
				"index": i,
				"tool":  subTool,
				"quiet": true,
			}
			results = append(results, entry)
			remarshaled, _ := json.Marshal(entry)
			remaining -= db.ApproxTokens(string(remarshaled))
			continue
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
	for _, tr := range chainTrims {
		attachWarningStructured(data, "chain_selector_trimmed", WarningSeverityWarning,
			fmt.Sprintf("queries[%v] from.select=%q matched %v values; only the first %v were spliced — narrow the upstream query (kind=, limit=) if the tail mattered", tr["query_index"], tr["select"], tr["matched"], tr["kept"]),
			tr)
	}
	if len(errIdx) > 0 {
		attachWarningStructured(data, "batch_sub_errors", WarningSeverityWarning,
			fmt.Sprintf("sub-quer%s at index %v returned errors — see the per-entry `error` field; the rest of the batch completed normally", pluralIES(len(errIdx)), errIdx),
			map[string]any{"error_indexes": errIdx})
	}

	// Restore the session-delta ledger before stamping the outer
	// envelope — see the pre-loop comment.
	s.lastEmittedCaps.Store(preBatchCapsFP)
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
