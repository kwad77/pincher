// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// Conclusion-density primitives: trace/search count_only + assert_graph.
//
// The contract under test: compute below the envelope is token-free;
// only conclusions pay. count_only totals must equal the row-shaped
// call's totals on the same fixture (no separate COUNT path that can
// drift), the count envelopes must carry NOTHING row-shaped, and
// assert_graph must return two-token conclusions when passing /
// violations-only when failing.

// seedConclusionGraph builds a small call graph:
//
//	cmd/main.go::main.Run            ─CALLS→ internal/db/db.go::db.Open
//	internal/server/server.go::Serve ─CALLS→ internal/db/db.go::db.Open
//	internal/extract/extract.go::Pull ─CALLS→ internal/db/db.go::db.Open  (the layering stray)
//	cmd/main.go::main.Run            ─CALLS→ internal/server/server.go::Serve
func seedConclusionGraph(t *testing.T, store *db.Store, pid string) {
	t.Helper()
	mustUpsertProject(t, store, pid, "/tmp/"+pid, pid)
	syms := []db.Symbol{
		{ID: "internal/db/db.go::db.Open#Function", FilePath: "internal/db/db.go",
			Name: "Open", QualifiedName: "db.Open", Kind: "Function", Language: "Go",
			ExtractionConfidence: 1.0},
		{ID: "cmd/main.go::main.Run#Function", FilePath: "cmd/main.go",
			Name: "Run", QualifiedName: "main.Run", Kind: "Function", Language: "Go",
			ExtractionConfidence: 1.0},
		{ID: "internal/server/server.go::server.Serve#Function", FilePath: "internal/server/server.go",
			Name: "Serve", QualifiedName: "server.Serve", Kind: "Function", Language: "Go",
			ExtractionConfidence: 1.0},
		{ID: "internal/extract/extract.go::extract.Pull#Function", FilePath: "internal/extract/extract.go",
			Name: "Pull", QualifiedName: "extract.Pull", Kind: "Function", Language: "Go",
			ExtractionConfidence: 1.0},
	}
	for i := range syms {
		syms[i].ProjectID = pid
	}
	mustUpsertSymbols(t, store, syms)
	mustUpsertEdges(t, store, pid, []db.Edge{
		{FromID: "cmd/main.go::main.Run#Function", ToID: "internal/db/db.go::db.Open#Function", Kind: "CALLS", Confidence: 1},
		{FromID: "internal/server/server.go::server.Serve#Function", ToID: "internal/db/db.go::db.Open#Function", Kind: "CALLS", Confidence: 1},
		{FromID: "internal/extract/extract.go::extract.Pull#Function", ToID: "internal/db/db.go::db.Open#Function", Kind: "CALLS", Confidence: 1},
		{FromID: "cmd/main.go::main.Run#Function", ToID: "internal/server/server.go::server.Serve#Function", Kind: "CALLS", Confidence: 1},
	})
}

// ─── trace count_only ────────────────────────────────────────────────

// Parity: count_only's total must equal the row-shaped call's total on
// the same args, and the by_depth/by_risk breakdowns must sum to it.
// Shape: nothing row-shaped (no hops, no risk_summary).
func TestHandleTrace_CountOnly_ParityAndShape(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "p-cd-trace"
	seedConclusionGraph(t, store, pid)
	srv.sessionID = pid

	traceArgs := map[string]any{
		"name":      "Open",
		"direction": "inbound",
		"depth":     3,
	}
	rowRes, err := srv.handleTrace(context.Background(), makeReq(traceArgs))
	if err != nil {
		t.Fatalf("row-shaped trace: %v", err)
	}
	rowBody := decode(t, rowRes)
	rowTotal, _ := rowBody["total"].(float64)
	if rowTotal == 0 {
		t.Fatalf("fixture should produce inbound hops; got %v", rowBody)
	}

	traceArgs["count_only"] = true
	countRes, err := srv.handleTrace(context.Background(), makeReq(traceArgs))
	if err != nil {
		t.Fatalf("count_only trace: %v", err)
	}
	body := decode(t, countRes)

	if got, _ := body["total"].(float64); got != rowTotal {
		t.Errorf("count parity broken: count_only total=%v, row-shaped total=%v", got, rowTotal)
	}
	for _, rowShaped := range []string{"hops", "risk_summary"} {
		if _, present := body[rowShaped]; present {
			t.Errorf("count_only response must not carry row-shaped key %q; got %v", rowShaped, body[rowShaped])
		}
	}
	if got := str(body, "root"); got != "Open" {
		t.Errorf("root = %q, want Open", got)
	}
	if got := str(body, "direction"); got != "inbound" {
		t.Errorf("direction = %q, want inbound", got)
	}
	byDepth, ok := body["by_depth"].(map[string]any)
	if !ok {
		t.Fatalf("by_depth missing or wrong type; got %v", body["by_depth"])
	}
	depthSum := 0.0
	for _, v := range byDepth {
		depthSum += v.(float64)
	}
	if depthSum != rowTotal {
		t.Errorf("by_depth sums to %v, want %v", depthSum, rowTotal)
	}
	byRisk, ok := body["by_risk"].(map[string]any)
	if !ok {
		t.Fatalf("by_risk missing (risk defaults true); got %v", body["by_risk"])
	}
	riskSum := 0.0
	for _, v := range byRisk {
		riskSum += v.(float64)
	}
	if riskSum != rowTotal {
		t.Errorf("by_risk sums to %v, want %v", riskSum, rowTotal)
	}
	// Direct callers of Open are depth 1 = CRITICAL; the fixture has 3.
	if got, _ := byRisk["CRITICAL"].(float64); got != 3 {
		t.Errorf("by_risk[CRITICAL] = %v, want 3 (db.Open's direct callers)", got)
	}
}

// count_only composes with filters: direction=outbound on a leaf
// returns total=0 with empty maps, never a row key.
func TestHandleTrace_CountOnly_ComposesWithDirection(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "p-cd-trace-dir"
	seedConclusionGraph(t, store, pid)
	srv.sessionID = pid

	res, err := srv.handleTrace(context.Background(), makeReq(map[string]any{
		"name":       "Open",
		"direction":  "outbound", // db.Open calls nothing in the fixture
		"depth":      3,
		"count_only": true,
	}))
	if err != nil {
		t.Fatalf("handleTrace: %v", err)
	}
	body := decode(t, res)
	if got, _ := body["total"].(float64); got != 0 {
		t.Errorf("outbound total = %v, want 0 (Open is a callee leaf)", got)
	}
	if _, present := body["hops"]; present {
		t.Error("count_only must not carry hops even when empty")
	}
}

// ─── search count_only ───────────────────────────────────────────────

// Parity: count_only total == the row-shaped call's total, by_kind sums
// to total, and nothing row-shaped (results/count/snippets) appears.
func TestHandleSearch_CountOnly_ParityAndShape(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "p-cd-search"
	mustUpsertProject(t, store, pid, "/tmp/"+pid, pid)
	syms := []db.Symbol{
		{ID: "a.go::pkg.LoopAlpha#Function", FilePath: "a.go", Name: "LoopAlpha",
			QualifiedName: "pkg.LoopAlpha", Kind: "Function", Language: "Go", ExtractionConfidence: 1.0},
		{ID: "b.go::pkg.LoopBeta#Function", FilePath: "b.go", Name: "LoopBeta",
			QualifiedName: "pkg.LoopBeta", Kind: "Function", Language: "Go", ExtractionConfidence: 1.0},
		{ID: "c.go::pkg.LoopWidget#Method", FilePath: "c.go", Name: "LoopWidget",
			QualifiedName: "pkg.T.LoopWidget", Kind: "Method", Language: "Go", ExtractionConfidence: 1.0},
	}
	for i := range syms {
		syms[i].ProjectID = pid
	}
	mustUpsertSymbols(t, store, syms)
	srv.sessionID = pid

	rowRes, err := srv.handleSearch(context.Background(), makeReq(map[string]any{
		"query": "Loop*",
	}))
	if err != nil {
		t.Fatalf("row-shaped search: %v", err)
	}
	rowBody := decode(t, rowRes)
	rowTotal, _ := rowBody["total"].(float64)
	rowRows, _ := rowBody["results"].([]any)
	if rowTotal == 0 || float64(len(rowRows)) != rowTotal {
		t.Fatalf("fixture sanity: total=%v len(results)=%d", rowTotal, len(rowRows))
	}

	countRes, err := srv.handleSearch(context.Background(), makeReq(map[string]any{
		"query":      "Loop*",
		"count_only": true,
	}))
	if err != nil {
		t.Fatalf("count_only search: %v", err)
	}
	body := decode(t, countRes)
	if got, _ := body["total"].(float64); got != rowTotal {
		t.Errorf("count parity broken: count_only total=%v, row-shaped total=%v (== len(results))", got, rowTotal)
	}
	for _, rowShaped := range []string{"results", "count", "has_more", "offset", "limit"} {
		if _, present := body[rowShaped]; present {
			t.Errorf("count_only response must not carry row-shaped key %q; got %v", rowShaped, body[rowShaped])
		}
	}
	byKind, ok := body["by_kind"].(map[string]any)
	if !ok {
		t.Fatalf("by_kind missing or wrong type; got %v", body["by_kind"])
	}
	if got, _ := byKind["Function"].(float64); got != 2 {
		t.Errorf("by_kind[Function] = %v, want 2", got)
	}
	if got, _ := byKind["Method"].(float64); got != 1 {
		t.Errorf("by_kind[Method] = %v, want 1", got)
	}
}

// count_only composes with the kind filter (and any other store-side
// filter): kind=Method narrows total and by_kind in lockstep.
func TestHandleSearch_CountOnly_ComposesWithKindFilter(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "p-cd-search-kind"
	mustUpsertProject(t, store, pid, "/tmp/"+pid, pid)
	syms := []db.Symbol{
		{ID: "a.go::pkg.GadgetOne#Function", FilePath: "a.go", Name: "GadgetOne",
			QualifiedName: "pkg.GadgetOne", Kind: "Function", Language: "Go", ExtractionConfidence: 1.0},
		{ID: "b.go::pkg.GadgetTwo#Method", FilePath: "b.go", Name: "GadgetTwo",
			QualifiedName: "pkg.T.GadgetTwo", Kind: "Method", Language: "Go", ExtractionConfidence: 1.0},
	}
	for i := range syms {
		syms[i].ProjectID = pid
	}
	mustUpsertSymbols(t, store, syms)
	srv.sessionID = pid

	res, err := srv.handleSearch(context.Background(), makeReq(map[string]any{
		"query":      "Gadget*",
		"kind":       "Method",
		"count_only": true,
	}))
	if err != nil {
		t.Fatalf("handleSearch: %v", err)
	}
	body := decode(t, res)
	if got, _ := body["total"].(float64); got != 1 {
		t.Errorf("kind=Method total = %v, want 1", got)
	}
	byKind, _ := body["by_kind"].(map[string]any)
	if _, present := byKind["Function"]; present {
		t.Errorf("kind=Method by_kind must not contain Function; got %v", byKind)
	}
}

// ─── assert_graph ────────────────────────────────────────────────────

// Passing assertion: {pass:true, checked:N}, no violations key.
func TestHandleAssertGraph_NoCallersOutside_Pass(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "p-cd-assert-pass"
	seedConclusionGraph(t, store, pid)
	srv.sessionID = pid

	res, err := srv.handleAssertGraph(context.Background(), makeReq(map[string]any{
		"kind":   "no_callers_outside",
		"target": "db.Open",
		"scope":  "cmd/,internal/server/,internal/extract/",
	}))
	if err != nil {
		t.Fatalf("handleAssertGraph: %v", err)
	}
	body := decode(t, res)
	if pass, _ := body["pass"].(bool); !pass {
		t.Errorf("expected pass=true; got %v", body)
	}
	if got, _ := body["checked"].(float64); got != 3 {
		t.Errorf("checked = %v, want 3 direct callers", got)
	}
	if _, present := body["violations"]; present {
		t.Errorf("passing assertion must not carry violations; got %v", body["violations"])
	}
}

// Failing assertion: pass=false + the strays, each {id, file_path}.
func TestHandleAssertGraph_NoCallersOutside_FailListsStrays(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "p-cd-assert-fail"
	seedConclusionGraph(t, store, pid)
	srv.sessionID = pid

	res, err := srv.handleAssertGraph(context.Background(), makeReq(map[string]any{
		"kind":   "no_callers_outside",
		"target": "db.Open",
		"scope":  "cmd/,internal/server/",
	}))
	if err != nil {
		t.Fatalf("handleAssertGraph: %v", err)
	}
	body := decode(t, res)
	if pass, _ := body["pass"].(bool); pass {
		t.Fatalf("expected pass=false (extract.Pull is the stray); got %v", body)
	}
	violations, _ := body["violations"].([]any)
	if len(violations) != 1 {
		t.Fatalf("expected exactly 1 violation; got %v", violations)
	}
	v, _ := violations[0].(map[string]any)
	if str(v, "id") != "internal/extract/extract.go::extract.Pull#Function" {
		t.Errorf("violation id = %q, want extract.Pull", str(v, "id"))
	}
	if str(v, "file_path") != "internal/extract/extract.go" {
		t.Errorf("violation file_path = %q, want internal/extract/extract.go", str(v, "file_path"))
	}
	if got, _ := body["violations_total"].(float64); got != 1 {
		t.Errorf("violations_total = %v, want 1", got)
	}
}

// no_calls_to is the inverse scope test: the layering rule fails when
// the forbidden region calls the target.
func TestHandleAssertGraph_NoCallsTo(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "p-cd-assert-layer"
	seedConclusionGraph(t, store, pid)
	srv.sessionID = pid

	// internal/extract/ DOES call db.Open → fail.
	res, err := srv.handleAssertGraph(context.Background(), makeReq(map[string]any{
		"kind":   "no_calls_to",
		"target": "db.Open",
		"scope":  "internal/extract/",
	}))
	if err != nil {
		t.Fatalf("handleAssertGraph: %v", err)
	}
	body := decode(t, res)
	if pass, _ := body["pass"].(bool); pass {
		t.Errorf("internal/extract/ calls db.Open — expected pass=false; got %v", body)
	}

	// docs/ does not → pass.
	res, err = srv.handleAssertGraph(context.Background(), makeReq(map[string]any{
		"kind":   "no_calls_to",
		"target": "db.Open",
		"scope":  "docs/",
	}))
	if err != nil {
		t.Fatalf("handleAssertGraph: %v", err)
	}
	body = decode(t, res)
	if pass, _ := body["pass"].(bool); !pass {
		t.Errorf("docs/ never calls db.Open — expected pass=true; got %v", body)
	}
}

// max_callers: inclusive limit; over-limit fails with the callers as
// violations.
func TestHandleAssertGraph_MaxCallers(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "p-cd-assert-max"
	seedConclusionGraph(t, store, pid)
	srv.sessionID = pid

	res, err := srv.handleAssertGraph(context.Background(), makeReq(map[string]any{
		"kind": "max_callers", "target": "db.Open", "limit": 3,
	}))
	if err != nil {
		t.Fatalf("handleAssertGraph: %v", err)
	}
	if body := decode(t, res); !body["pass"].(bool) {
		t.Errorf("3 callers <= limit 3 must pass; got %v", body)
	}

	res, err = srv.handleAssertGraph(context.Background(), makeReq(map[string]any{
		"kind": "max_callers", "target": "db.Open", "limit": 2,
	}))
	if err != nil {
		t.Fatalf("handleAssertGraph: %v", err)
	}
	body := decode(t, res)
	if body["pass"].(bool) {
		t.Fatalf("3 callers > limit 2 must fail; got %v", body)
	}
	if violations, _ := body["violations"].([]any); len(violations) != 3 {
		t.Errorf("over-limit failure lists the callers; got %v", body["violations"])
	}
}

// Violations are capped at 10 with a warning carrying the full count.
func TestHandleAssertGraph_ViolationsCappedAtTen(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "p-cd-assert-cap"
	mustUpsertProject(t, store, pid, "/tmp/"+pid, pid)
	syms := []db.Symbol{{ID: "internal/db/db.go::db.Open#Function", FilePath: "internal/db/db.go",
		Name: "Open", QualifiedName: "db.Open", Kind: "Function", Language: "Go", ExtractionConfidence: 1.0}}
	var edges []db.Edge
	for i := 0; i < 14; i++ {
		id := fmt.Sprintf("pkg/f%02d.go::pkg.Caller%02d#Function", i, i)
		syms = append(syms, db.Symbol{ID: id, FilePath: fmt.Sprintf("pkg/f%02d.go", i),
			Name: fmt.Sprintf("Caller%02d", i), QualifiedName: fmt.Sprintf("pkg.Caller%02d", i),
			Kind: "Function", Language: "Go", ExtractionConfidence: 1.0})
		edges = append(edges, db.Edge{FromID: id, ToID: "internal/db/db.go::db.Open#Function", Kind: "CALLS", Confidence: 1})
	}
	for i := range syms {
		syms[i].ProjectID = pid
	}
	mustUpsertSymbols(t, store, syms)
	mustUpsertEdges(t, store, pid, edges)
	srv.sessionID = pid

	res, err := srv.handleAssertGraph(context.Background(), makeReq(map[string]any{
		"kind":   "no_callers_outside",
		"target": "db.Open",
		"scope":  "cmd/", // nothing lives under cmd/ → all 14 are strays
	}))
	if err != nil {
		t.Fatalf("handleAssertGraph: %v", err)
	}
	body := decode(t, res)
	if body["pass"].(bool) {
		t.Fatalf("14 strays must fail; got %v", body)
	}
	violations, _ := body["violations"].([]any)
	if len(violations) != assertGraphViolationsCap {
		t.Errorf("violations len = %d, want cap %d", len(violations), assertGraphViolationsCap)
	}
	if got, _ := body["violations_total"].(float64); got != 14 {
		t.Errorf("violations_total = %v, want 14", got)
	}
	if got, _ := body["checked"].(float64); got != 14 {
		t.Errorf("checked = %v, want 14", got)
	}
	meta, _ := body["_meta"].(map[string]any)
	warnings, _ := meta["warnings"].([]any)
	found := false
	for _, w := range warnings {
		if s, _ := w.(string); strings.Contains(s, "trimmed to 10 of 14") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a trim warning naming 10 of 14; got %v", meta["warnings"])
	}
}

// exists: pass on a real symbol, fail (pass=false, checked=0) on a
// missing one — never an IsError, absence is a valid conclusion.
func TestHandleAssertGraph_Exists(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "p-cd-assert-exists"
	seedConclusionGraph(t, store, pid)
	srv.sessionID = pid

	res, err := srv.handleAssertGraph(context.Background(), makeReq(map[string]any{
		"kind": "exists", "target": "Serve",
	}))
	if err != nil {
		t.Fatalf("handleAssertGraph: %v", err)
	}
	body := decode(t, res)
	if !body["pass"].(bool) {
		t.Errorf("Serve is indexed — expected pass=true; got %v", body)
	}
	if got, _ := body["checked"].(float64); got < 1 {
		t.Errorf("checked = %v, want >= 1", got)
	}

	res, err = srv.handleAssertGraph(context.Background(), makeReq(map[string]any{
		"kind": "exists", "target": "NoSuchSymbolAnywhere",
	}))
	if err != nil {
		t.Fatalf("handleAssertGraph: %v", err)
	}
	if res.IsError {
		t.Fatalf("exists on a missing symbol is a pass=false conclusion, not an error; got %s", textOf(t, res))
	}
	body = decode(t, res)
	if body["pass"].(bool) {
		t.Errorf("expected pass=false for a missing symbol; got %v", body)
	}
	if got, _ := body["checked"].(float64); got != 0 {
		t.Errorf("checked = %v, want 0", got)
	}
}

// Unknown kind rich-errors with the full four-kind catalog in
// next_steps (failure-as-pedagogy).
func TestHandleAssertGraph_UnknownKindPedagogy(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "p-cd-assert-kind"
	seedConclusionGraph(t, store, pid)
	srv.sessionID = pid

	res, err := srv.handleAssertGraph(context.Background(), makeReq(map[string]any{
		"kind": "has_cycles", "target": "db.Open",
	}))
	if err != nil {
		t.Fatalf("handleAssertGraph: %v", err)
	}
	if !res.IsError {
		t.Fatalf("unknown kind must be an error; got %s", textOf(t, res))
	}
	raw := textOf(t, res)
	if !strings.Contains(raw, "has_cycles") {
		t.Errorf("error must name the unknown kind; got %s", raw)
	}
	for _, k := range []string{"no_callers_outside", "max_callers", "no_calls_to", "exists"} {
		if !strings.Contains(raw, k) {
			t.Errorf("error must teach the full catalog — missing %q in %s", k, raw)
		}
	}
}

// Missing per-kind args rich-error before any graph work: scope for
// the scoped kinds, limit for max_callers.
func TestHandleAssertGraph_MissingArgsPedagogy(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "p-cd-assert-args"
	seedConclusionGraph(t, store, pid)
	srv.sessionID = pid

	for _, tc := range []struct {
		name string
		args map[string]any
		want string
	}{
		{"no scope on no_callers_outside",
			map[string]any{"kind": "no_callers_outside", "target": "db.Open"}, "requires `scope`"},
		{"no scope on no_calls_to",
			map[string]any{"kind": "no_calls_to", "target": "db.Open"}, "requires `scope`"},
		{"no limit on max_callers",
			map[string]any{"kind": "max_callers", "target": "db.Open"}, "requires `limit`"},
		{"no target",
			map[string]any{"kind": "exists"}, "requires `target`"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := srv.handleAssertGraph(context.Background(), makeReq(tc.args))
			if err != nil {
				t.Fatalf("handleAssertGraph: %v", err)
			}
			if !res.IsError {
				t.Fatalf("expected IsError; got %s", textOf(t, res))
			}
			if raw := textOf(t, res); !strings.Contains(raw, tc.want) {
				t.Errorf("error should contain %q; got %s", tc.want, raw)
			}
		})
	}
}

// Resolving the target by exact symbol id works and an unresolvable
// target is a rich-error pointing at search.
func TestHandleAssertGraph_TargetResolution(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "p-cd-assert-resolve"
	seedConclusionGraph(t, store, pid)
	srv.sessionID = pid

	res, err := srv.handleAssertGraph(context.Background(), makeReq(map[string]any{
		"kind":   "max_callers",
		"target": "internal/db/db.go::db.Open#Function",
		"limit":  10,
	}))
	if err != nil {
		t.Fatalf("handleAssertGraph: %v", err)
	}
	body := decode(t, res)
	if got, _ := body["checked"].(float64); got != 3 {
		t.Errorf("id-resolved target checked = %v, want 3", got)
	}
	if str(body, "target") != "internal/db/db.go::db.Open#Function" {
		t.Errorf("target echo = %q, want the resolved id", str(body, "target"))
	}

	res, err = srv.handleAssertGraph(context.Background(), makeReq(map[string]any{
		"kind": "max_callers", "target": "NoSuchFunc", "limit": 1,
	}))
	if err != nil {
		t.Fatalf("handleAssertGraph: %v", err)
	}
	if !res.IsError {
		t.Fatalf("unresolvable caller-shaped target must rich-error; got %s", textOf(t, res))
	}
	if raw := textOf(t, res); !strings.Contains(raw, "search") {
		t.Errorf("not-found error should point at search; got %s", raw)
	}
}
