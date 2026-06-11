// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// Tests for verify_change (loop-substrate PR-10): the post-edit gate.
// Git seeding mirrors changes_tests_to_run_test.go — a temp repo with
// one committed-then-modified file so `git diff` yields real hunks.

// setupVerifyServer wires the standard verify_change fixture: a test
// server pointed at a temp git repo (main.go modified on lines 2-3)
// registered as the session project.
func setupVerifyServer(t *testing.T, name string) (*Server, *db.Store, string) {
	t.Helper()
	srv, store, _ := newTestServer(t)
	repoDir := setupChangesGitRepo(t)
	store.UpsertProject(db.Project{ID: repoDir, Path: repoDir, Name: name, IndexedAt: time.Now()})
	srv.sessionID = repoDir
	srv.sessionRoot = repoDir
	return srv, store, repoDir
}

// seedFooWithCaller seeds the canonical plan→verify scenario: Foo
// (changed, line 2 of main.go) with one direct caller in callers.go.
func seedFooWithCaller(t *testing.T, store *db.Store, repoDir string) {
	t.Helper()
	mustUpsertSymbols(t, store, []db.Symbol{
		{ID: "p::main.Foo#Function", ProjectID: repoDir, FilePath: "main.go", Name: "Foo",
			QualifiedName: "main.Foo", Kind: "Function", Language: "Go",
			StartByte: 13, EndByte: 30, StartLine: 2, EndLine: 2, ExtractionConfidence: 1.0},
		{ID: "p::main.CallsFoo#Function", ProjectID: repoDir, FilePath: "callers.go", Name: "CallsFoo",
			QualifiedName: "main.CallsFoo", Kind: "Function", Language: "Go",
			StartByte: 0, EndByte: 50, StartLine: 1, EndLine: 3, ExtractionConfidence: 1.0},
	})
	mustUpsertEdges(t, store, repoDir, []db.Edge{
		{FromID: "p::main.CallsFoo#Function", ToID: "p::main.Foo#Function", Kind: "CALLS"},
	})
}

func verifyWarningsV2(body map[string]any) []map[string]any {
	meta, _ := body["_meta"].(map[string]any)
	raw, _ := meta["warnings_v2"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, w := range raw {
		if m, ok := w.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func warningWithCode(ws []map[string]any, code string) (map[string]any, bool) {
	for _, w := range ws {
		if w["code"] == code {
			return w, true
		}
	}
	return nil, false
}

// Happy path: plan_change stashes the depth-1 prediction; the edit
// touches exactly what was planned; verify_change reports a fresh
// comparison with an empty unpredicted_impact and NO warning.
func TestVerifyChange_PlanMatchesActual_NoWarning(t *testing.T) {
	t.Parallel()
	srv, store, repoDir := setupVerifyServer(t, "vc-happy")
	seedFooWithCaller(t, store, repoDir)

	// Plan BEFORE the edit (the edit itself is the repo mutation
	// setupChangesGitRepo already made — order doesn't matter to the
	// cache, only the index generation does).
	if _, err := srv.handlePlanChange(context.Background(), makeReq(map[string]any{
		"target": "p::main.Foo#Function",
	})); err != nil {
		t.Fatalf("plan_change: %v", err)
	}

	result, err := srv.handleVerifyChange(context.Background(), makeReq(map[string]any{
		"scope": "all", "target": "p::main.Foo#Function",
	}))
	if err != nil {
		t.Fatalf("verify_change: %v", err)
	}
	body := decode(t, result)

	pc, ok := body["plan_comparison"].(map[string]any)
	if !ok {
		t.Fatalf("plan_comparison missing after a cached plan:\n%v", body)
	}
	if stale, _ := pc["stale"].(bool); stale {
		t.Errorf("plan should be fresh (same index generation); got stale=true")
	}
	predicted, _ := pc["predicted_callers"].([]any)
	if len(predicted) != 1 || predicted[0] != "p::main.CallsFoo#Function" {
		t.Errorf("predicted_callers = %v, want [p::main.CallsFoo#Function]", predicted)
	}
	actual, _ := pc["actual_impacted"].([]any)
	if len(actual) != 1 || actual[0] != "p::main.CallsFoo#Function" {
		t.Errorf("actual_impacted = %v, want [p::main.CallsFoo#Function]", actual)
	}
	if unpredicted, _ := pc["unpredicted_impact"].([]any); len(unpredicted) != 0 {
		t.Errorf("unpredicted_impact should be empty when prediction matched; got %v", unpredicted)
	}
	ws := verifyWarningsV2(body)
	if _, found := warningWithCode(ws, "unpredicted_impact"); found {
		t.Errorf("no unpredicted_impact warning expected on a matching plan; got %v", ws)
	}
	if _, found := warningWithCode(ws, "plan_stale"); found {
		t.Errorf("no plan_stale warning expected at the same generation; got %v", ws)
	}
}

// A caller the plan never predicted appears after the edit:
// unpredicted_impact lists it and warnings_v2 names it.
func TestVerifyChange_UnpredictedCaller_Warns(t *testing.T) {
	t.Parallel()
	srv, store, repoDir := setupVerifyServer(t, "vc-unpredicted")
	seedFooWithCaller(t, store, repoDir)

	if _, err := srv.handlePlanChange(context.Background(), makeReq(map[string]any{
		"target": "p::main.Foo#Function",
	})); err != nil {
		t.Fatalf("plan_change: %v", err)
	}

	// The "edit" wires in a brand-new caller the plan never saw.
	mustUpsertSymbols(t, store, []db.Symbol{
		{ID: "p::main.Surprise#Function", ProjectID: repoDir, FilePath: "surprise.go", Name: "Surprise",
			QualifiedName: "main.Surprise", Kind: "Function", Language: "Go",
			StartByte: 0, EndByte: 50, StartLine: 1, EndLine: 3, ExtractionConfidence: 1.0},
	})
	mustUpsertEdges(t, store, repoDir, []db.Edge{
		{FromID: "p::main.Surprise#Function", ToID: "p::main.Foo#Function", Kind: "CALLS"},
	})

	result, err := srv.handleVerifyChange(context.Background(), makeReq(map[string]any{
		"scope": "all", "target": "p::main.Foo#Function",
	}))
	if err != nil {
		t.Fatalf("verify_change: %v", err)
	}
	body := decode(t, result)

	pc, _ := body["plan_comparison"].(map[string]any)
	if pc == nil {
		t.Fatalf("plan_comparison missing:\n%v", body)
	}
	unpredicted, _ := pc["unpredicted_impact"].([]any)
	if len(unpredicted) != 1 || unpredicted[0] != "p::main.Surprise#Function" {
		t.Fatalf("unpredicted_impact = %v, want [p::main.Surprise#Function]", unpredicted)
	}
	w, found := warningWithCode(verifyWarningsV2(body), "unpredicted_impact")
	if !found {
		t.Fatalf("expected warnings_v2 code=unpredicted_impact; got %v", verifyWarningsV2(body))
	}
	if msg, _ := w["message"].(string); !strings.Contains(msg, "p::main.Surprise#Function") {
		t.Errorf("unpredicted_impact warning should list the surprise caller; got %q", msg)
	}
}

// The index generation moved between plan and verify: the comparison
// reports staleness instead of a bogus diff.
func TestVerifyChange_StalePlan_WarnsInsteadOfDiff(t *testing.T) {
	t.Parallel()
	srv, store, repoDir := setupVerifyServer(t, "vc-stale")
	seedFooWithCaller(t, store, repoDir)

	if _, err := srv.handlePlanChange(context.Background(), makeReq(map[string]any{
		"target": "p::main.Foo#Function",
	})); err != nil {
		t.Fatalf("plan_change: %v", err)
	}
	// Simulate an index pass completing between plan and verify by
	// rewinding the stashed generation. (The real bump happens inside
	// Indexer.Index; mutating the cache entry exercises the exact same
	// comparison without indexing real files into the fixture store.)
	srv.planMu.Lock()
	if len(srv.planCache) == 0 {
		srv.planMu.Unlock()
		t.Fatal("plan_change did not stash a plan")
	}
	srv.planCache[len(srv.planCache)-1].generation = 42
	srv.planMu.Unlock()

	result, err := srv.handleVerifyChange(context.Background(), makeReq(map[string]any{
		"scope": "all", "target": "p::main.Foo#Function",
	}))
	if err != nil {
		t.Fatalf("verify_change: %v", err)
	}
	body := decode(t, result)

	if _, found := warningWithCode(verifyWarningsV2(body), "plan_stale"); !found {
		t.Fatalf("expected warnings_v2 code=plan_stale; got %v", verifyWarningsV2(body))
	}
	pc, _ := body["plan_comparison"].(map[string]any)
	if pc == nil {
		t.Fatalf("plan_comparison should still report the stale plan's prediction:\n%v", body)
	}
	if stale, _ := pc["stale"].(bool); !stale {
		t.Errorf("plan_comparison.stale = %v, want true", pc["stale"])
	}
	if _, hasActual := pc["actual_impacted"]; hasActual {
		t.Errorf("stale plan must NOT carry an actual-vs-predicted diff; got %v", pc)
	}
}

// No plan cached for the target: plan_comparison is absent and an
// info-severity note explains the plan→edit→verify contract.
func TestVerifyChange_NoPlan_ComparisonAbsent(t *testing.T) {
	t.Parallel()
	srv, store, repoDir := setupVerifyServer(t, "vc-noplan")
	seedFooWithCaller(t, store, repoDir)

	result, err := srv.handleVerifyChange(context.Background(), makeReq(map[string]any{
		"scope": "all", "target": "p::main.Foo#Function",
	}))
	if err != nil {
		t.Fatalf("verify_change: %v", err)
	}
	body := decode(t, result)

	if _, present := body["plan_comparison"]; present {
		t.Errorf("plan_comparison must be absent when no plan was cached; got %v", body["plan_comparison"])
	}
	w, found := warningWithCode(verifyWarningsV2(body), "no_plan_cached")
	if !found {
		t.Fatalf("expected warnings_v2 code=no_plan_cached note; got %v", verifyWarningsV2(body))
	}
	if sev, _ := w["severity"].(string); sev != "info" {
		t.Errorf("no_plan_cached severity = %q, want info (advisory, not a fault)", sev)
	}
}

// Orphan check: a Function in the changed file whose only caller was
// deleted by the edit (zero inbound edges now) is reported as
// possibly_orphaned_by_change. Methods are excluded — the static call
// graph is dispatch-blind for interface calls.
func TestVerifyChange_OrphanedFunctionReported_MethodExcluded(t *testing.T) {
	t.Parallel()
	srv, store, repoDir := setupVerifyServer(t, "vc-orphan")

	mustUpsertSymbols(t, store, []db.Symbol{
		// Changed + still called: NOT orphaned.
		{ID: "p::main.Foo#Function", ProjectID: repoDir, FilePath: "main.go", Name: "Foo",
			QualifiedName: "main.Foo", Kind: "Function", Language: "Go",
			StartByte: 13, EndByte: 30, StartLine: 2, EndLine: 2, ExtractionConfidence: 1.0},
		// The edit deleted helper's only caller — zero inbound edges now.
		{ID: "p::main.helper#Function", ProjectID: repoDir, FilePath: "main.go", Name: "helper",
			QualifiedName: "main.helper", Kind: "Function", Language: "Go",
			StartByte: 31, EndByte: 48, StartLine: 3, EndLine: 3, ExtractionConfidence: 1.0},
		// A Method with zero inbound edges — could be live via interface
		// dispatch, so the orphan check must skip it.
		{ID: "p::main.T.orphMethod#Method", ProjectID: repoDir, FilePath: "main.go", Name: "orphMethod",
			QualifiedName: "main.T.orphMethod", Kind: "Method", Language: "Go",
			StartByte: 49, EndByte: 60, StartLine: 3, EndLine: 3, ExtractionConfidence: 1.0},
		{ID: "p::main.TestFoo#Function", ProjectID: repoDir, FilePath: "main_test.go", Name: "TestFoo",
			QualifiedName: "main.TestFoo", Kind: "Function", Language: "Go",
			StartByte: 0, EndByte: 100, StartLine: 1, EndLine: 5, IsTest: true, ExtractionConfidence: 1.0},
	})
	mustUpsertEdges(t, store, repoDir, []db.Edge{
		{FromID: "p::main.TestFoo#Function", ToID: "p::main.Foo#Function", Kind: "CALLS"},
	})

	result, err := srv.handleVerifyChange(context.Background(), makeReq(map[string]any{"scope": "all"}))
	if err != nil {
		t.Fatalf("verify_change: %v", err)
	}
	body := decode(t, result)

	orphans, _ := body["possibly_orphaned"].([]any)
	if len(orphans) != 1 {
		t.Fatalf("possibly_orphaned = %v, want exactly [helper] (Foo has a caller; orphMethod is a Method)", orphans)
	}
	row, _ := orphans[0].(map[string]any)
	if row["id"] != "p::main.helper#Function" {
		t.Errorf("possibly_orphaned[0].id = %v, want p::main.helper#Function", row["id"])
	}
	if row["label"] != "possibly_orphaned_by_change" {
		t.Errorf("orphan label = %v, want possibly_orphaned_by_change (advisory)", row["label"])
	}
	summary, _ := body["summary"].(map[string]any)
	if n, _ := summary["possibly_orphaned"].(float64); n != 1 {
		t.Errorf("summary.possibly_orphaned = %v, want 1", n)
	}
}

// Budget: max_tokens trims bulk lists deterministically and surfaces
// warnings_v2 code=budget_truncated; summary keeps the true counts.
func TestVerifyChange_BudgetRespected(t *testing.T) {
	t.Parallel()
	srv, store, repoDir := setupVerifyServer(t, "vc-budget")

	// An untracked file with many symbols — the cheapest way to make
	// the envelope big (same trick as the #740 changed_symbols test).
	untracked := filepath.Join(repoDir, "untracked.go")
	os.WriteFile(untracked, []byte("package main\n"), 0o644)
	nSyms := 40
	syms := make([]db.Symbol, 0, nSyms)
	for i := 0; i < nSyms; i++ {
		syms = append(syms, db.Symbol{
			ID: "p::main.U" + itoaPad(i) + "#Function", ProjectID: repoDir,
			FilePath: "untracked.go", Name: "U" + itoaPad(i),
			QualifiedName: "main.U" + itoaPad(i), Kind: "Function", Language: "Go",
			ExtractionConfidence: 1.0,
		})
	}
	mustUpsertSymbols(t, store, syms)

	result, err := srv.handleVerifyChange(context.Background(), makeReq(map[string]any{
		"scope": "all", "max_tokens": 150,
	}))
	if err != nil {
		t.Fatalf("verify_change: %v", err)
	}
	body := decode(t, result)

	changed, _ := body["changed_symbols"].([]any)
	if len(changed) >= nSyms {
		t.Errorf("changed_symbols not trimmed under max_tokens=150: %d rows", len(changed))
	}
	if _, found := warningWithCode(verifyWarningsV2(body), "budget_truncated"); !found {
		t.Fatalf("expected warnings_v2 code=budget_truncated; got %v", verifyWarningsV2(body))
	}
	// Summary keeps the TRUE counts even when the lists were cut.
	summary, _ := body["summary"].(map[string]any)
	if n, _ := summary["changed_symbols"].(float64); int(n) != nSyms {
		t.Errorf("summary.changed_symbols = %v, want %d (true count survives the trim)", n, nSyms)
	}
}

// Lookup matches the plan by resolved file path too, not just the raw
// target string — the agent often plans by symbol id and verifies by
// file (or vice versa).
func TestVerifyChange_PlanLookupByFile(t *testing.T) {
	t.Parallel()
	srv, store, repoDir := setupVerifyServer(t, "vc-byfile")
	seedFooWithCaller(t, store, repoDir)

	if _, err := srv.handlePlanChange(context.Background(), makeReq(map[string]any{
		"target": "p::main.Foo#Function",
	})); err != nil {
		t.Fatalf("plan_change: %v", err)
	}

	result, err := srv.handleVerifyChange(context.Background(), makeReq(map[string]any{
		"scope": "all", "target": "main.go",
	}))
	if err != nil {
		t.Fatalf("verify_change: %v", err)
	}
	body := decode(t, result)
	if _, ok := body["plan_comparison"].(map[string]any); !ok {
		t.Fatalf("plan_comparison missing — file-path lookup against the plan's resolved file should match:\n%v", body)
	}
}
