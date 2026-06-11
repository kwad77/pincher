// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
	"github.com/kwad77/pincher/internal/index"
)

// feat/dispatch-blind-verdicts: harden dead-code verdicts against
// dispatch blindness. Measured failure: audit_unused labeled all nine
// live tree-sitter `extract` methods "confidence: high" with evidence
// "deep_trace_confirms_unreachable" — they are reached via interface
// dispatch (the extractor registry), which produces no CALLS edges.
// The static graph cannot see Go interface dispatch; the verdict must
// say so instead of inviting an autonomous agent to delete live code.
//
// Coverage:
//   (a) Method whose case-folded name matches a project interface
//       method → capped at medium + interface_dispatch_possible +
//       warnings_v2 dispatch_blind.
//   (b) Control: plain uncalled Function in an AST-tier language keeps
//       confidence high with no dispatch_blind warning (no regression
//       of the useful high-confidence path).
//   (c) Regex-tier candidate (extraction_confidence < 0.95) → capped
//       at medium + warnings_v2 dispatch_blind.

// dispatchBlindGoSrc mirrors the measured tree-sitter shape: the
// registry interface declares `Extract` (exported), the impl-tier
// method is spelled `extract` (unexported) and has zero static
// callers — the live path goes through the interface value, which the
// call graph records no CALLS edge for. The exact-case #493 SQL
// suppression (interface_methods.method_name = s.name) misses the
// case variant; the folded heuristic must catch it.
const dispatchBlindGoSrc = `package reg

// Extractor is the registry interface.
type Extractor interface {
	Extract() error
}

// tsImpl is the concrete extractor.
type tsImpl struct{}

// extract has zero callers in the static graph — reached via dispatch.
func (x *tsImpl) extract() error {
	return nil
}
`

// dispatchBlindControlGoSrc: a plain unexported function with zero
// callers and no interface-name collision — the genuinely-safe case.
const dispatchBlindControlGoSrc = `package reg

// orphanHelper has no callers and no interface method shares its name.
func orphanHelper() error {
	return nil
}
`

func setupDispatchBlindServer(t *testing.T, rel, src string) (*Server, string) {
	t.Helper()
	srv, store, root := newTestServer(t)
	srv.sessionRoot = root
	writeGoFile(t, root, rel, src)
	idx := index.New(store)
	res, err := idx.Index(context.Background(), root, false)
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	srv.sessionID = res.ProjectID
	return srv, res.ProjectID
}

// hasWarningsV2Code reports whether _meta.warnings_v2 carries an entry
// with the given code.
func hasWarningsV2Code(body map[string]any, code string) bool {
	meta, _ := body["_meta"].(map[string]any)
	if meta == nil {
		return false
	}
	v2, _ := meta["warnings_v2"].([]any)
	for _, w := range v2 {
		m, _ := w.(map[string]any)
		if m != nil && m["code"] == code {
			return true
		}
	}
	return false
}

func candidateByName(t *testing.T, body map[string]any, name string) map[string]any {
	t.Helper()
	cands, _ := body["candidates"].([]any)
	for _, c := range cands {
		m, _ := c.(map[string]any)
		if m != nil && m["name"] == name {
			return m
		}
	}
	return nil
}

func evidenceContains(m map[string]any, want string) bool {
	ev, _ := m["evidence"].([]any)
	for _, e := range ev {
		if e == want {
			return true
		}
	}
	return false
}

// TestAuditUnused_InterfaceDispatchCandidateCappedAtMedium — (a). An
// uncalled unexported Method whose folded name matches an interface
// method must not be reported as a high-confidence deletion target.
func TestAuditUnused_InterfaceDispatchCandidateCappedAtMedium(t *testing.T) {
	t.Parallel()
	srv, projectID := setupDispatchBlindServer(t, "reg.go", dispatchBlindGoSrc)

	res, err := srv.handleAuditUnused(context.Background(), makeReq(map[string]any{
		"project": projectID,
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	body := decode(t, res)

	cand := candidateByName(t, body, "extract")
	if cand == nil {
		t.Fatalf("expected `extract` among candidates; got %v", body["candidates"])
	}
	if cand["confidence"] != "medium" {
		t.Errorf("extract confidence = %v; want medium (capped — graph is blind to interface dispatch)", cand["confidence"])
	}
	if !evidenceContains(cand, "interface_dispatch_possible") {
		t.Errorf("extract missing interface_dispatch_possible evidence; got %v", cand["evidence"])
	}
	// The trace fact stays on record — zero CALLS edges is true, just
	// not conclusive.
	if !evidenceContains(cand, "deep_trace_confirms_unreachable") {
		t.Errorf("extract should keep deep_trace_confirms_unreachable evidence; got %v", cand["evidence"])
	}
	if !hasWarningsV2Code(body, "dispatch_blind") {
		t.Errorf("expected _meta.warnings_v2 code=dispatch_blind; got %v", body["_meta"])
	}
}

// TestAuditUnused_PlainFunctionKeepsHighConfidence — (b) control. The
// useful high-confidence path must not regress: an AST-tier Function
// with zero callers and no dispatch ambiguity stays high, with no
// dispatch_blind warning on the response.
func TestAuditUnused_PlainFunctionKeepsHighConfidence(t *testing.T) {
	t.Parallel()
	srv, projectID := setupDispatchBlindServer(t, "reg.go", dispatchBlindControlGoSrc)

	res, err := srv.handleAuditUnused(context.Background(), makeReq(map[string]any{
		"project": projectID,
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	body := decode(t, res)

	cand := candidateByName(t, body, "orphanHelper")
	if cand == nil {
		t.Fatalf("expected `orphanHelper` among candidates; got %v", body["candidates"])
	}
	if cand["confidence"] != "high" {
		t.Errorf("orphanHelper confidence = %v; want high (control — no cap applies)", cand["confidence"])
	}
	if evidenceContains(cand, "interface_dispatch_possible") {
		t.Errorf("orphanHelper wrongly flagged interface_dispatch_possible; got %v", cand["evidence"])
	}
	if hasWarningsV2Code(body, "dispatch_blind") {
		t.Errorf("control response should carry no dispatch_blind warning; got %v", body["_meta"])
	}
}

// TestAuditUnused_RegexTierCandidateCapped — (c). A candidate whose
// extraction_confidence sits below the AST tier (regex-extracted
// language) is capped at medium: regex tiers under-resolve cross-file
// calls, so zero inbound edges is expected even for live code.
func TestAuditUnused_RegexTierCandidateCapped(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "p-dispatch-blind-regex"
	if err := store.UpsertProject(db.Project{
		ID: pid, Path: t.TempDir(), Name: pid, IndexedAt: time.Now(),
		FileCount: 1, SymCount: 1, EdgeCount: 0,
	}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	srv.sessionID = pid

	mustUpsertSymbols(t, store, []db.Symbol{{
		ID:                   "src/util.ts::util.lonelyHelper#Function",
		ProjectID:            pid,
		FilePath:             "src/util.ts",
		Name:                 "lonelyHelper",
		QualifiedName:        "util.lonelyHelper",
		Kind:                 "Function",
		Language:             "TypeScript",
		StartLine:            10,
		ExtractionConfidence: 0.85,
	}})

	res, err := srv.handleAuditUnused(context.Background(), makeReq(map[string]any{
		"project":        pid,
		"min_confidence": 0.85,
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	body := decode(t, res)

	cand := candidateByName(t, body, "lonelyHelper")
	if cand == nil {
		t.Fatalf("expected `lonelyHelper` among candidates; got %v", body["candidates"])
	}
	if cand["confidence"] != "medium" {
		t.Errorf("lonelyHelper confidence = %v; want medium (regex-tier cap)", cand["confidence"])
	}
	if !evidenceContains(cand, "extraction_confidence_below_ast_tier_calls_under_resolved") {
		t.Errorf("lonelyHelper missing regex-tier evidence; got %v", cand["evidence"])
	}
	if !hasWarningsV2Code(body, "dispatch_blind") {
		t.Errorf("expected _meta.warnings_v2 code=dispatch_blind; got %v", body["_meta"])
	}
}

// TestDeadCode_InterfaceDispatchRowCarriesEvidence — the atomic tool
// gets the same flags: per-row evidence + the response-level
// dispatch_blind warning (dead_code has no per-row confidence field
// to cap, so evidence + warning are the contract there).
func TestDeadCode_InterfaceDispatchRowCarriesEvidence(t *testing.T) {
	t.Parallel()
	srv, projectID := setupDispatchBlindServer(t, "reg.go", dispatchBlindGoSrc)

	res, err := srv.handleDeadCode(context.Background(), makeReq(map[string]any{
		"project": projectID,
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	body := decode(t, res)

	rows, _ := body["dead_symbols"].([]any)
	var extractRow map[string]any
	for _, r := range rows {
		m, _ := r.(map[string]any)
		if m != nil && m["name"] == "extract" {
			extractRow = m
		}
	}
	if extractRow == nil {
		t.Fatalf("expected `extract` among dead_symbols; got %v", rows)
	}
	if !evidenceContains(extractRow, "interface_dispatch_possible") {
		t.Errorf("extract row missing interface_dispatch_possible evidence; got %v", extractRow["evidence"])
	}
	if !hasWarningsV2Code(body, "dispatch_blind") {
		t.Errorf("expected _meta.warnings_v2 code=dispatch_blind; got %v", body["_meta"])
	}
}
