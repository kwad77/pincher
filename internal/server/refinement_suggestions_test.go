// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #1634 v0.85: refinement-suggestion contract tests.

func TestSuggestReverseTraceDirection_Inbound_SuggestsOutbound_1634(t *testing.T) {
	t.Parallel()
	step, ok := suggestReverseTraceDirection("p::pkg.Target#Function", "inbound")
	if !ok {
		t.Fatal("inbound direction should produce a reverse-direction suggestion")
	}
	if step["tool"] != "trace" {
		t.Errorf("tool=%q, want trace", step["tool"])
	}
	if !strings.Contains(step["args"], `"direction":"outbound"`) {
		t.Errorf("args=%q, want direction=outbound", step["args"])
	}
	if !strings.Contains(step["args"], `"id":"p::pkg.Target#Function"`) {
		t.Errorf("args=%q, want seed id preserved", step["args"])
	}
	if !strings.Contains(step["why"], "inbound") || !strings.Contains(step["why"], "outbound") {
		t.Errorf("why=%q, want both directions named in the explanation", step["why"])
	}
}

func TestSuggestReverseTraceDirection_Outbound_SuggestsInbound_1634(t *testing.T) {
	t.Parallel()
	step, ok := suggestReverseTraceDirection("p::pkg.Hub#Function", "outbound")
	if !ok {
		t.Fatal("outbound direction should produce a reverse-direction suggestion")
	}
	if !strings.Contains(step["args"], `"direction":"inbound"`) {
		t.Errorf("args=%q, want direction=inbound", step["args"])
	}
}

func TestSuggestReverseTraceDirection_Both_ReturnsFalse_1634(t *testing.T) {
	t.Parallel()
	_, ok := suggestReverseTraceDirection("p::pkg.X#Function", "both")
	if ok {
		t.Error("direction=both should not produce a reverse suggestion — both sides already searched")
	}
}

func TestSuggestReverseTraceDirection_Unknown_ReturnsFalse_1634(t *testing.T) {
	t.Parallel()
	_, ok := suggestReverseTraceDirection("p::pkg.X#Function", "sideways")
	if ok {
		t.Error("unknown direction should not produce a reverse suggestion")
	}
}

// Integration: handleTrace's empty-leaf branch prepends the reverse-
// direction next_step when direction was one-sided. Mirrors the shape
// of TestHandleTrace_NextStepsAfterTrace but seeds the symbol with NO
// edges so the empty path fires.
func TestHandleTrace_EmptyInbound_NextStepsPrependsReverseDirection_1634(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "tr-empty-1634"
	srv.sessionID = pid
	srv.sessionRoot = "/tmp/" + pid
	store.UpsertProject(db.Project{ID: pid, Path: "/tmp/" + pid, Name: pid, IndexedAt: time.Now()})
	store.BulkUpsertSymbols([]db.Symbol{
		{ID: pid + "/a.go::pkg.Leaf#Function", ProjectID: pid,
			FilePath: "a.go", Name: "Leaf", QualifiedName: "pkg.Leaf",
			Kind: "Function", Language: "Go"},
	})
	// No edges seeded — empty-trace branch fires.

	result, err := srv.handleTrace(context.Background(), makeReq(map[string]any{
		"name":      "Leaf",
		"project":   pid,
		"direction": "inbound",
	}))
	if err != nil || result.IsError {
		t.Fatalf("handleTrace: err=%v isErr=%v body=%v", err, result.IsError, decode(t, result))
	}
	m := decode(t, result)
	meta, _ := m["_meta"].(map[string]any)
	if meta == nil {
		t.Fatal("_meta missing")
	}
	stepsRaw, _ := meta["next_steps"].([]any)
	if len(stepsRaw) < 2 {
		t.Fatalf("expected at least 2 next_steps (reverse-direction + context), got %d: %v", len(stepsRaw), stepsRaw)
	}
	// First step should be the reverse-direction trace retry.
	first, _ := stepsRaw[0].(map[string]any)
	if first == nil {
		t.Fatalf("first next_step is not a map: %T %v", stepsRaw[0], stepsRaw[0])
	}
	if first["tool"] != "trace" {
		t.Errorf("first next_step tool=%v, want trace (the reverse-direction retry)", first["tool"])
	}
	argsStr, _ := first["args"].(string)
	if !strings.Contains(argsStr, `"direction":"outbound"`) {
		t.Errorf("first next_step args=%q, want direction=outbound", argsStr)
	}
}

func TestHandleTrace_EmptyOutbound_NextStepsPrependsReverseDirection_1634(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "tr-empty-out-1634"
	srv.sessionID = pid
	srv.sessionRoot = "/tmp/" + pid
	store.UpsertProject(db.Project{ID: pid, Path: "/tmp/" + pid, Name: pid, IndexedAt: time.Now()})
	store.BulkUpsertSymbols([]db.Symbol{
		{ID: pid + "/a.go::pkg.Hub#Function", ProjectID: pid,
			FilePath: "a.go", Name: "Hub", QualifiedName: "pkg.Hub",
			Kind: "Function", Language: "Go"},
	})

	result, err := srv.handleTrace(context.Background(), makeReq(map[string]any{
		"name":      "Hub",
		"project":   pid,
		"direction": "outbound",
	}))
	if err != nil || result.IsError {
		t.Fatalf("handleTrace: err=%v isErr=%v body=%v", err, result.IsError, decode(t, result))
	}
	m := decode(t, result)
	meta, _ := m["_meta"].(map[string]any)
	stepsRaw, _ := meta["next_steps"].([]any)
	if len(stepsRaw) < 2 {
		t.Fatalf("expected at least 2 next_steps, got %d", len(stepsRaw))
	}
	first, _ := stepsRaw[0].(map[string]any)
	argsStr, _ := first["args"].(string)
	if !strings.Contains(argsStr, `"direction":"inbound"`) {
		t.Errorf("first next_step args=%q, want direction=inbound", argsStr)
	}
}

// Control: direction=both already searches both edge sets, so no
// reverse-direction suggestion should appear. The existing leaf-source
// suggestion remains the only next_step.
func TestHandleTrace_EmptyBoth_NoReverseDirectionSuggestion_1634(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "tr-empty-both-1634"
	srv.sessionID = pid
	srv.sessionRoot = "/tmp/" + pid
	store.UpsertProject(db.Project{ID: pid, Path: "/tmp/" + pid, Name: pid, IndexedAt: time.Now()})
	store.BulkUpsertSymbols([]db.Symbol{
		{ID: pid + "/a.go::pkg.Solo#Function", ProjectID: pid,
			FilePath: "a.go", Name: "Solo", QualifiedName: "pkg.Solo",
			Kind: "Function", Language: "Go"},
	})

	result, err := srv.handleTrace(context.Background(), makeReq(map[string]any{
		"name":      "Solo",
		"project":   pid,
		"direction": "both",
	}))
	if err != nil || result.IsError {
		t.Fatalf("handleTrace: err=%v isErr=%v body=%v", err, result.IsError, decode(t, result))
	}
	m := decode(t, result)
	meta, _ := m["_meta"].(map[string]any)
	stepsRaw, _ := meta["next_steps"].([]any)
	for _, raw := range stepsRaw {
		step, _ := raw.(map[string]any)
		if step["tool"] == "trace" {
			argsStr, _ := step["args"].(string)
			if strings.Contains(argsStr, `"direction":`) {
				t.Errorf("direction=both should not produce a direction-flip suggestion, got step args=%q", argsStr)
			}
		}
	}
}

// Search's existing infrastructure (suggestEmptySearchNextSteps +
// verifyEmptySearchCause) already covers #1634 acceptance A1 for the
// search tool. This test pins that contract so a future refactor can't
// silently break the refinement-suggestion shape.
func TestHandleSearch_UnexpectedZero_RefinementsAreDifferentFromOriginal_1634(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "search-empty-1634"
	srv.sessionID = pid
	srv.sessionRoot = "/tmp/" + pid
	store.UpsertProject(db.Project{ID: pid, Path: "/tmp/" + pid, Name: pid, IndexedAt: time.Now()})
	// No symbols — search will hit the empty path and stamp
	// EmptyReasonNoResultsInCorpus + emit recovery next_steps.

	result, err := srv.handleSearch(context.Background(), makeReq(map[string]any{
		"query":          "NonexistentFunction",
		"project":        pid,
		"kind":           "Function",
		"min_confidence": 0.9,
	}))
	if err != nil || result.IsError {
		t.Fatalf("handleSearch: err=%v isErr=%v body=%v", err, result.IsError, decode(t, result))
	}
	m := decode(t, result)
	meta, _ := m["_meta"].(map[string]any)
	if meta == nil {
		t.Fatal("_meta missing on empty search")
	}
	stepsRaw, _ := meta["next_steps"].([]any)
	if len(stepsRaw) == 0 {
		t.Fatalf("expected next_steps[] on unexpected-zero search, got none. meta=%v", meta)
	}
	// At least one step must have args differing from the original
	// call — the #1634 acceptance criterion verbatim.
	originalArgsJSON, _ := json.Marshal(map[string]any{
		"query": "NonexistentFunction", "kind": "Function", "min_confidence": 0.9,
	})
	foundDifferent := false
	for _, raw := range stepsRaw {
		step, _ := raw.(map[string]any)
		argsStr, _ := step["args"].(string)
		if argsStr != "" && argsStr != string(originalArgsJSON) {
			foundDifferent = true
			break
		}
	}
	if !foundDifferent {
		t.Errorf("no next_step had args differing from the original call — #1634 acceptance failed. steps=%v", stepsRaw)
	}
}
