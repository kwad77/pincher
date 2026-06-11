// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"testing"
)

// PR-6 (loop-substrate): seed-quality gate tests.
//
// The composeGoSrc fixture (context_for_task_test.go) has Compute /
// helperA / helperB / Caller / Widget / Render, with Compute's
// docstring reading "Compute is the seed function for composite
// tests." — which lets a prose task BM25-match symbols whose NAMES
// share nothing with the task.

func seedQualityWarningPresent(t *testing.T, body map[string]any) bool {
	t.Helper()
	meta, _ := body["_meta"].(map[string]any)
	if meta == nil {
		return false
	}
	raw, _ := meta["warnings_v2"].([]any)
	for _, w := range raw {
		if wm, ok := w.(map[string]any); ok {
			if c, _ := wm["code"].(string); c == "seed_quality_low" {
				return true
			}
		}
	}
	return false
}

// Positive: a prose task that matches only via docstring text degrades
// to suggestions_only — seeds present, expansion suppressed, structured
// warning attached.
func TestContextForTask_ProseDocstringMatch_SuggestionsOnly(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupComposeTestServer(t)

	res, err := srv.handleContextForTask(context.Background(), makeReq(map[string]any{
		"task":    "seed anchoring during composite investigations",
		"project": projectID,
	}))
	if err != nil {
		t.Fatalf("handleContextForTask: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success envelope, got error: %s", textOf(t, res))
	}
	body := decode(t, res)
	if mode, _ := body["mode"].(string); mode != "suggestions_only" {
		t.Fatalf("expected mode=suggestions_only for prose-docstring task, got %q (body keys: %v)", mode, mapKeysContextForTask(body))
	}
	if seeds, _ := body["seeds"].([]any); len(seeds) == 0 {
		t.Error("suggestions_only must still carry the seed suggestions")
	}
	if callers, _ := body["callers"].([]any); len(callers) != 0 {
		t.Error("suggestions_only must not expand callers")
	}
	if !seedQualityWarningPresent(t, body) {
		t.Error("expected warnings_v2 code=seed_quality_low")
	}
	meta, _ := body["_meta"].(map[string]any)
	sq, _ := meta["seed_quality"].(map[string]any)
	if sq == nil {
		t.Fatal("expected _meta.seed_quality stamp")
	}
	if lvl, _ := sq["level"].(string); lvl != "low" {
		t.Errorf("expected seed_quality.level=low, got %q", lvl)
	}
}

// Control: an exact-name task expands fully and stamps level=high.
func TestContextForTask_ExactName_FullEnvelopeWithQualityStamp(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupComposeTestServer(t)

	res, err := srv.handleContextForTask(context.Background(), makeReq(map[string]any{
		"task":    "Compute",
		"project": projectID,
	}))
	if err != nil {
		t.Fatalf("handleContextForTask: %v", err)
	}
	body := decode(t, res)
	if mode, _ := body["mode"].(string); mode == "suggestions_only" {
		t.Fatal("exact-name task must not degrade to suggestions_only")
	}
	meta, _ := body["_meta"].(map[string]any)
	sq, _ := meta["seed_quality"].(map[string]any)
	if sq == nil {
		t.Fatal("expected _meta.seed_quality on the full envelope")
	}
	if lvl, _ := sq["level"].(string); lvl != "high" {
		t.Errorf("expected seed_quality.level=high for exact-name task, got %q", lvl)
	}
	if sq["exact_name_match"] != true {
		t.Error("expected exact_name_match=true")
	}
}

// Override: expand=true forces the full envelope despite low overlap.
func TestContextForTask_ExpandTrue_OverridesGate(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupComposeTestServer(t)

	res, err := srv.handleContextForTask(context.Background(), makeReq(map[string]any{
		"task":    "seed anchoring during composite investigations",
		"project": projectID,
		"expand":  true,
	}))
	if err != nil {
		t.Fatalf("handleContextForTask: %v", err)
	}
	body := decode(t, res)
	if mode, _ := body["mode"].(string); mode == "suggestions_only" {
		t.Fatal("expand=true must override the seed-quality gate")
	}
	meta, _ := body["_meta"].(map[string]any)
	if sq, _ := meta["seed_quality"].(map[string]any); sq == nil {
		t.Error("expanded low-quality envelope should still stamp seed_quality")
	}
}

// seed_id mode is exempt from the gate — the caller named their anchor.
func TestContextForTask_SeedIDMode_GateExempt(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupComposeTestServer(t)

	// Resolve Compute's real ID via search-equivalent store call by
	// using the task path first (exact name → full envelope).
	res, err := srv.handleContextForTask(context.Background(), makeReq(map[string]any{
		"task":    "Compute",
		"project": projectID,
	}))
	if err != nil {
		t.Fatalf("seed lookup: %v", err)
	}
	body := decode(t, res)
	seeds, _ := body["seeds"].([]any)
	if len(seeds) == 0 {
		t.Fatal("fixture should seed on Compute")
	}
	first, _ := seeds[0].(map[string]any)
	id, _ := first["id"].(string)

	res2, err := srv.handleContextForTask(context.Background(), makeReq(map[string]any{
		"seed_id": id,
		"project": projectID,
	}))
	if err != nil {
		t.Fatalf("seed_id mode: %v", err)
	}
	body2 := decode(t, res2)
	if mode, _ := body2["mode"].(string); mode == "suggestions_only" {
		t.Error("seed_id mode must never degrade to suggestions_only")
	}
}

// Unit: identifier tokenization shapes.
func TestIdentifierTokens_Shapes(t *testing.T) {
	t.Parallel()
	strong, weak := identifierTokens("fix the jsonResultWithMeta bug in db.Open under load_factor")
	wantStrong := map[string]bool{"jsonresultwithmeta": true, "db.open": true, "load_factor": true}
	if len(strong) != 3 {
		t.Fatalf("expected 3 strong tokens, got %v", strong)
	}
	for _, s := range strong {
		if !wantStrong[s] {
			t.Errorf("unexpected strong token %q", s)
		}
	}
	for _, w := range weak {
		if w == "the" || w == "fix" || w == "bug" || w == "in" {
			t.Errorf("stopword %q leaked into weak tokens", w)
		}
	}
	// ALLCAPS prose must not count as identifier-shaped.
	s2, _ := identifierTokens("TODO HTTP")
	if len(s2) != 0 {
		t.Errorf("ALLCAPS words must not be strong identifiers, got %v", s2)
	}
}
