// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/kwad77/pincher/internal/db"
	"github.com/kwad77/pincher/internal/index"
)

// Chain-mode (loop-substrate M13) tests: server-side pipelining inside
// `batch` — a later sub-query splices a NAMED selection from an earlier
// result into its own args, so the intermediate never crosses the token
// envelope. Coverage: top_id chain correctness, quiet suppression with
// selected provenance, ids→symbols fan-in, upstream-empty skip,
// forward-ref / selector validation errors, selector caps + trim
// warning, independent-queries regression, budget enforcement across a
// chain, and the measured token win vs the two-call equivalent.

// setupChainPileServer indexes a file with 25 functions sharing the
// docstring token "chainmark" — enough hits to overflow chainIDsCap.
func setupChainPileServer(t *testing.T) (*Server, string) {
	t.Helper()
	srv, store, root := newTestServer(t)
	srv.sessionRoot = root
	var sb strings.Builder
	sb.WriteString("package chainpile\n\n")
	for i := 0; i < 25; i++ {
		fmt.Fprintf(&sb, "// chainmark probe %d\nfunc Probe%d() int { return %d }\n\n", i, i, i)
	}
	writeGoFile(t, root, "chainpile.go", sb.String())
	idx := index.New(store)
	res, err := idx.Index(context.Background(), root, false)
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	srv.sessionID = res.ProjectID
	return srv, res.ProjectID
}

// Positive: search → context via from:{query:0, select:"top_id"}. The
// dependent context call must return the TOP search hit's context —
// the id the agent would have copied by hand, spliced server-side.
func TestBatchChain_TopID_SearchToContext(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupComposeTestServer(t)

	res, err := srv.handleBatch(context.Background(), makeReq(map[string]any{
		"project": projectID,
		"queries": []any{
			map[string]any{"tool": "search", "args": map[string]any{"query": "Compute", "kind": "Function"}},
			map[string]any{"tool": "context", "from": map[string]any{"query": float64(0), "select": "top_id"}},
		},
	}))
	if err != nil {
		t.Fatalf("handleBatch: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success; got %s", textOf(t, res))
	}
	results := batchResults(t, decode(t, res))
	if len(results) != 2 {
		t.Fatalf("expected 2 entries; got %d", len(results))
	}
	ctxBody, ok := results[1]["result"].(map[string]any)
	if !ok {
		t.Fatalf("results[1] (chained context) has no result body: %#v", results[1])
	}
	sym, _ := ctxBody["symbol"].(map[string]any)
	if name, _ := sym["name"].(string); name != "Compute" {
		t.Errorf("chained context resolved symbol %q, want \"Compute\" (top search hit's id should have been spliced)", name)
	}
	if id, _ := sym["id"].(string); id != "compose.go::compose.Compute#Function" {
		t.Errorf("chained context id = %q, want the top search hit's stable id", id)
	}
}

// quiet: the upstream search body is OMITTED from the response, but the
// entry keeps {index, tool, quiet:true, selected} so the chain stays
// auditable — and the dependent context still resolves correctly.
func TestBatchChain_Quiet_SuppressesBodyCarriesSelected(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupComposeTestServer(t)

	res, err := srv.handleBatch(context.Background(), makeReq(map[string]any{
		"project": projectID,
		"queries": []any{
			map[string]any{"tool": "search", "args": map[string]any{"query": "Compute", "kind": "Function"}, "quiet": true},
			map[string]any{"tool": "context", "from": map[string]any{"query": float64(0), "select": "top_id"}},
		},
	}))
	if err != nil {
		t.Fatalf("handleBatch: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success; got %s", textOf(t, res))
	}
	results := batchResults(t, decode(t, res))

	// Entry 0: body suppressed, provenance kept.
	if _, has := results[0]["result"]; has {
		t.Errorf("results[0] is quiet but still carries a result body: %#v", results[0])
	}
	if _, has := results[0]["_meta"]; has {
		t.Errorf("results[0] is quiet but still carries per-entry _meta: %#v", results[0])
	}
	if q, _ := results[0]["quiet"].(bool); !q {
		t.Errorf("results[0].quiet = %v, want true", results[0]["quiet"])
	}
	sel, _ := results[0]["selected"].(string)
	if sel != "compose.go::compose.Compute#Function" {
		t.Errorf("results[0].selected = %q, want the spliced top id (auditable provenance)", sel)
	}

	// Entry 1: the context envelope is the one real payload.
	ctxBody, ok := results[1]["result"].(map[string]any)
	if !ok {
		t.Fatalf("results[1] (chained context) has no result body: %#v", results[1])
	}
	sym, _ := ctxBody["symbol"].(map[string]any)
	if name, _ := sym["name"].(string); name != "Compute" {
		t.Errorf("chained context resolved %q, want \"Compute\"", name)
	}
}

// Fan-in: trace(inbound Compute) yields the callers' ids; select:"ids"
// splices them into symbols' ids arg (the one multi-value pairing v1
// implements). Both depth-1 callers must come back.
func TestBatchChain_IDsToSymbols_FanIn(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupComposeTestServer(t)

	res, err := srv.handleBatch(context.Background(), makeReq(map[string]any{
		"project": projectID,
		"queries": []any{
			map[string]any{"tool": "trace", "args": map[string]any{"name": "Compute", "direction": "inbound"}},
			map[string]any{"tool": "symbols", "from": map[string]any{"query": float64(0), "select": "ids"}},
		},
	}))
	if err != nil {
		t.Fatalf("handleBatch: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success; got %s", textOf(t, res))
	}
	results := batchResults(t, decode(t, res))
	symBody, ok := results[1]["result"].(map[string]any)
	if !ok {
		t.Fatalf("results[1] (chained symbols) has no result body: %#v", results[1])
	}
	rows, _ := symBody["symbols"].([]any)
	if len(rows) < 2 {
		t.Fatalf("expected >=2 symbols fanned in from the trace (Caller, Render); got %d: %#v", len(rows), symBody)
	}
	names := map[string]bool{}
	for _, r := range rows {
		m, _ := r.(map[string]any)
		if n, _ := m["name"].(string); n != "" {
			names[n] = true
		}
	}
	for _, want := range []string{"Caller", "Render"} {
		if !names[want] {
			t.Errorf("fan-in missing caller %q; got names %v", want, names)
		}
	}
}

// Upstream-empty: a selector that finds nothing (empty search) and an
// errored upstream (bad symbol id) both skip the dependent entry with
// skipped:"upstream_empty" + the upstream index — never a guessed call.
func TestBatchChain_UpstreamEmpty_Skips(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupComposeTestServer(t)

	res, err := srv.handleBatch(context.Background(), makeReq(map[string]any{
		"project": projectID,
		"queries": []any{
			map[string]any{"tool": "search", "args": map[string]any{"query": "zzzNoSuchSymbolAnywhere"}},
			map[string]any{"tool": "context", "from": map[string]any{"query": float64(0), "select": "top_id"}},
			map[string]any{"tool": "symbol", "args": map[string]any{"id": "nope.go::nope.Missing#Function"}},
			map[string]any{"tool": "context", "from": map[string]any{"query": float64(2), "select": "top_id"}},
		},
	}))
	if err != nil {
		t.Fatalf("handleBatch: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success envelope; got %s", textOf(t, res))
	}
	results := batchResults(t, decode(t, res))

	for _, tc := range []struct{ entry, upstream int }{{1, 0}, {3, 2}} {
		e := results[tc.entry]
		if sk, _ := e["skipped"].(string); sk != "upstream_empty" {
			t.Errorf("results[%d].skipped = %v, want \"upstream_empty\"", tc.entry, e["skipped"])
		}
		if up, _ := e["upstream"].(float64); int(up) != tc.upstream {
			t.Errorf("results[%d].upstream = %v, want %d", tc.entry, e["upstream"], tc.upstream)
		}
		if _, has := e["result"]; has {
			t.Errorf("results[%d] skipped on empty upstream but carries a result: %#v", tc.entry, e)
		}
	}
}

// Validation: forward/self references, unknown selectors, multi-value
// selects into single-value keys, and missing-into for plan-shaped
// tools all rich-error at validation time — before ANY sub-query runs.
func TestBatchChain_ValidationErrors(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupComposeTestServer(t)

	cases := []struct {
		name     string
		queries  []any
		wantText string
	}{
		{
			name: "forward_ref",
			queries: []any{
				map[string]any{"tool": "context", "from": map[string]any{"query": float64(1), "select": "top_id"}},
				map[string]any{"tool": "search", "args": map[string]any{"query": "Compute"}},
			},
			wantText: "LOWER index",
		},
		{
			name: "self_ref",
			queries: []any{
				map[string]any{"tool": "search", "args": map[string]any{"query": "Compute"}},
				map[string]any{"tool": "context", "from": map[string]any{"query": float64(1), "select": "top_id"}},
			},
			wantText: "LOWER index",
		},
		{
			name: "unknown_selector",
			queries: []any{
				map[string]any{"tool": "search", "args": map[string]any{"query": "Compute"}},
				map[string]any{"tool": "context", "from": map[string]any{"query": float64(0), "select": "results[0].id"}},
			},
			wantText: "named selector",
		},
		{
			name: "multi_into_single",
			queries: []any{
				map[string]any{"tool": "search", "args": map[string]any{"query": "Compute"}},
				map[string]any{"tool": "context", "from": map[string]any{"query": float64(0), "select": "ids"}},
			},
			wantText: "symbols",
		},
		{
			name: "missing_into_for_plan_shaped_tool",
			queries: []any{
				map[string]any{"tool": "search", "args": map[string]any{"query": "Compute"}},
				map[string]any{"tool": "search", "from": map[string]any{"query": float64(0), "select": "top_id"}},
			},
			wantText: "into",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := srv.handleBatch(context.Background(), makeReq(map[string]any{
				"project": projectID,
				"queries": tc.queries,
			}))
			if err != nil {
				t.Fatalf("handleBatch: %v", err)
			}
			if !res.IsError {
				t.Fatalf("expected validation IsError before any execution; got %s", textOf(t, res))
			}
			if msg := textOf(t, res); !strings.Contains(msg, tc.wantText) {
				t.Errorf("validation error should mention %q; got %s", tc.wantText, msg)
			}
		})
	}
}

// Selector cap: 25 search hits feeding select:"ids" are trimmed to
// chainIDsCap (20) with a chain_selector_trimmed warning on the outer
// envelope, and the dependent symbols call receives exactly the cap.
func TestBatchChain_SelectorCap_TrimWarning(t *testing.T) {
	t.Parallel()
	srv, projectID := setupChainPileServer(t)

	res, err := srv.handleBatch(context.Background(), makeReq(map[string]any{
		"project":    projectID,
		"max_tokens": 50000, // roomy: the cap, not the budget, is under test
		"queries": []any{
			map[string]any{"tool": "search", "args": map[string]any{"query": "chainmark", "limit": 30, "compact": true}, "quiet": true},
			map[string]any{"tool": "symbols", "from": map[string]any{"query": float64(0), "select": "ids"}},
		},
	}))
	if err != nil {
		t.Fatalf("handleBatch: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success; got %s", textOf(t, res))
	}
	body := decode(t, res)
	results := batchResults(t, body)

	symBody, ok := results[1]["result"].(map[string]any)
	if !ok {
		t.Fatalf("results[1] (chained symbols) has no result body: %#v", results[1])
	}
	rows, _ := symBody["symbols"].([]any)
	if len(rows) != chainIDsCap {
		t.Errorf("chained symbols received %d ids, want the cap %d", len(rows), chainIDsCap)
	}

	// The quiet upstream's provenance is the capped list.
	selList, _ := results[0]["selected"].([]any)
	if len(selList) != chainIDsCap {
		t.Errorf("results[0].selected carries %d ids, want %d (the values actually passed on)", len(selList), chainIDsCap)
	}

	found := false
	for _, c := range warningsV2Codes(body) {
		if c == "chain_selector_trimmed" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected warnings_v2 code=chain_selector_trimmed; got %v", warningsV2Codes(body))
	}
}

// Regression: queries without `from`/`quiet` keep today's exact entry
// shape — no chain keys leak into the independent path.
func TestBatchChain_IndependentQueries_NoChainKeys(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupComposeTestServer(t)

	res, err := srv.handleBatch(context.Background(), makeReq(map[string]any{
		"project": projectID,
		"queries": []any{
			map[string]any{"tool": "search", "args": map[string]any{"query": "Compute"}},
			map[string]any{"tool": "trace", "args": map[string]any{"name": "Compute"}},
		},
	}))
	if err != nil {
		t.Fatalf("handleBatch: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success; got %s", textOf(t, res))
	}
	results := batchResults(t, decode(t, res))
	for i, e := range results {
		for _, k := range []string{"quiet", "selected", "skipped", "upstream"} {
			if _, has := e[k]; has {
				t.Errorf("results[%d] carries chain key %q on an independent query — additive contract broken: %#v", i, k, e)
			}
		}
		if _, has := e["result"]; !has {
			t.Errorf("results[%d] has no result body: %#v", i, e)
		}
	}
}

// Budget: the shared max_tokens is enforced across a chain exactly as
// for independent queries — once the upstream spends it, the dependent
// entry skips with budget_exhausted (budget precedes chain resolution).
func TestBatchChain_BudgetEnforcedAcrossChain(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupComposeTestServer(t)

	res, err := srv.handleBatch(context.Background(), makeReq(map[string]any{
		"project":    projectID,
		"max_tokens": 1,
		"queries": []any{
			map[string]any{"tool": "search", "args": map[string]any{"query": "Compute"}},
			map[string]any{"tool": "context", "from": map[string]any{"query": float64(0), "select": "top_id"}},
		},
	}))
	if err != nil {
		t.Fatalf("handleBatch: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success envelope; got %s", textOf(t, res))
	}
	body := decode(t, res)
	results := batchResults(t, body)
	if sk, _ := results[1]["skipped"].(string); sk != "budget_exhausted" {
		t.Errorf("results[1].skipped = %v, want \"budget_exhausted\" — chains must respect the shared budget", results[1]["skipped"])
	}
	found := false
	for _, c := range warningsV2Codes(body) {
		if c == "budget_truncated" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected warnings_v2 code=budget_truncated; got %v", warningsV2Codes(body))
	}
}

// MEASURE: the locality claim, on the fixture. A chained quiet
// search→context must ship at least 25% fewer approximate tokens than
// the two-call equivalent (full search envelope + full context
// envelope) an agent pays when it ferries the id itself.
func TestBatchChain_MeasuredTokenWin_VsTwoCalls(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupComposeTestServer(t)

	// Chained: ONE envelope — quiet search + context, id spliced server-side.
	chainRes, err := srv.handleBatch(context.Background(), makeReq(map[string]any{
		"project": projectID,
		"queries": []any{
			map[string]any{"tool": "search", "args": map[string]any{"query": "Compute", "kind": "Function"}, "quiet": true},
			map[string]any{"tool": "context", "from": map[string]any{"query": float64(0), "select": "top_id"}},
		},
	}))
	if err != nil {
		t.Fatalf("handleBatch (chain): %v", err)
	}
	if chainRes.IsError {
		t.Fatalf("chain failed: %s", textOf(t, chainRes))
	}
	// Sanity: the chain answered the actual question.
	results := batchResults(t, decode(t, chainRes))
	ctxBody, _ := results[1]["result"].(map[string]any)
	sym, _ := ctxBody["symbol"].(map[string]any)
	if name, _ := sym["name"].(string); name != "Compute" {
		t.Fatalf("chain answered the wrong symbol: %q", name)
	}
	chainTokens := db.ApproxTokens(textOf(t, chainRes))

	// Two-call equivalent: full search envelope, then full context
	// envelope with the id the agent read out of the first response.
	searchRes, err := srv.handleSearch(context.Background(), makeReq(map[string]any{
		"project": projectID, "query": "Compute", "kind": "Function",
	}))
	if err != nil {
		t.Fatalf("handleSearch: %v", err)
	}
	contextRes, err := srv.handleContext(context.Background(), makeReq(map[string]any{
		"project": projectID, "id": "compose.go::compose.Compute#Function",
	}))
	if err != nil {
		t.Fatalf("handleContext: %v", err)
	}
	twoCallTokens := db.ApproxTokens(textOf(t, searchRes)) + db.ApproxTokens(textOf(t, contextRes))

	t.Logf("chain=%d tokens, two-call=%d tokens, ratio=%.2f", chainTokens, twoCallTokens, float64(chainTokens)/float64(twoCallTokens))
	// At least 25% smaller: chain <= 0.75 * twoCall.
	if 4*chainTokens > 3*twoCallTokens {
		t.Errorf("chained quiet search→context = %d approx tokens vs two-call %d — want at least a 25%% reduction", chainTokens, twoCallTokens)
	}
}
