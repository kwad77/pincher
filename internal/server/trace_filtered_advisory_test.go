// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #898: when every inbound/outbound hop is filtered by the default
// test/fixture filter, trace returned an empty result with advice
// to "read the symbol's own source" — confidently wrong for a
// heavily-tested utility (the tests ARE the writers/callers). Now
// the advisory surfaces the include_tests=true escape hatch with the
// dropped count.

func seedTraceWithOnlyTestCallers(t *testing.T, projectID string) (srv *Server) {
	srv, store, _ := newTestServer(t)
	srv.sessionID = projectID
	store.UpsertProject(db.Project{ID: projectID, Path: "/tmp/" + projectID, Name: projectID, IndexedAt: time.Now()})

	mustUpsertSymbols(t, store, []db.Symbol{
		{ID: projectID + "::pkg.OnlyTestedTarget#Function", ProjectID: projectID,
			FilePath: "internal/util.go", Name: "OnlyTestedTarget",
			QualifiedName: "pkg.OnlyTestedTarget", Kind: "Function", Language: "Go",
			ExtractionConfidence: 1.0},
		{ID: projectID + "::pkg.TestA#Function", ProjectID: projectID,
			FilePath: "internal/util_test.go", Name: "TestA",
			QualifiedName: "pkg.TestA", Kind: "Function", Language: "Go",
			IsTest: true, ExtractionConfidence: 1.0},
		{ID: projectID + "::pkg.TestB#Function", ProjectID: projectID,
			FilePath: "internal/util_test.go", Name: "TestB",
			QualifiedName: "pkg.TestB", Kind: "Function", Language: "Go",
			IsTest: true, ExtractionConfidence: 1.0},
	})
	if err := store.BulkUpsertEdges([]db.Edge{
		{ProjectID: projectID, FromID: projectID + "::pkg.TestA#Function",
			ToID: projectID + "::pkg.OnlyTestedTarget#Function", Kind: "CALLS", Confidence: 1},
		{ProjectID: projectID, FromID: projectID + "::pkg.TestB#Function",
			ToID: projectID + "::pkg.OnlyTestedTarget#Function", Kind: "CALLS", Confidence: 1},
	}); err != nil {
		t.Fatal(err)
	}
	return srv
}

func TestHandleTrace_AllHopsFilteredAsTests_SurfacesIncludeTestsHint(t *testing.T) {
	t.Parallel()
	srv := seedTraceWithOnlyTestCallers(t, "p898a")

	result, err := srv.handleTrace(context.Background(), makeReq(map[string]any{
		"name":      "OnlyTestedTarget",
		"direction": "inbound",
	}))
	if err != nil {
		t.Fatal(err)
	}
	body := decode(t, result)
	if total, _ := body["total"].(float64); total != 0 {
		t.Errorf("default trace must return 0 hops when all are tests; got %v", total)
	}
	meta, _ := body["_meta"].(map[string]any)
	if meta == nil {
		t.Fatal("_meta missing")
	}
	diag, _ := meta["diagnosis"].(string)
	if !strings.Contains(diag, "test/fixture") {
		t.Errorf("diagnosis should name the test/fixture filter; got %q", diag)
	}
	if !strings.Contains(diag, "include_tests=true") {
		t.Errorf("diagnosis should mention include_tests=true; got %q", diag)
	}
	steps, _ := meta["next_steps"].([]any)
	if len(steps) == 0 {
		t.Fatal("next_steps must be non-empty when test hops were filtered")
	}
	first, _ := steps[0].(map[string]any)
	if first["tool"] != "trace" {
		t.Errorf("first next_step should re-invoke trace; got %v", first)
	}
	if argsStr, _ := first["args"].(string); !strings.Contains(argsStr, `"include_tests":true`) {
		t.Errorf("next_step args should pass include_tests=true; got %q", argsStr)
	}
}

// Control: a genuine leaf (no inbound edges at all) keeps the legacy
// "read source" advice. The test-filter hint must not fire.
func TestHandleTrace_GenuineLeaf_StillSuggestsContext(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p898b"
	store.UpsertProject(db.Project{ID: "p898b", Path: "/tmp/p898b", Name: "p898b", IndexedAt: time.Now()})

	mustUpsertSymbols(t, store, []db.Symbol{
		{ID: "p898b::pkg.Orphan#Function", ProjectID: "p898b",
			FilePath: "internal/util.go", Name: "Orphan",
			QualifiedName: "pkg.Orphan", Kind: "Function", Language: "Go",
			ExtractionConfidence: 1.0},
	})

	result, err := srv.handleTrace(context.Background(), makeReq(map[string]any{
		"name":      "Orphan",
		"direction": "inbound",
	}))
	if err != nil {
		t.Fatal(err)
	}
	body := decode(t, result)
	meta, _ := body["_meta"].(map[string]any)
	steps, _ := meta["next_steps"].([]any)
	if len(steps) == 0 {
		t.Fatal("genuine-leaf trace must still suggest a next step")
	}
	// #1634 v0.85: the empty-leaf branch now prepends a reverse-
	// direction trace retry before the context-on-self step when the
	// caller used a one-sided direction. The original guarantee still
	// holds — a `context` next_step IS present — but it's no longer
	// at index 0. Scan the slice instead of pinning a position.
	foundContext := false
	for _, raw := range steps {
		s, _ := raw.(map[string]any)
		if s["tool"] == "context" {
			foundContext = true
			break
		}
	}
	if !foundContext {
		t.Errorf("genuine-leaf next_steps should include a 'context' entry; got %v", steps)
	}
	// The include_tests hint must NOT fire.
	if diag, _ := meta["diagnosis"].(string); strings.Contains(diag, "include_tests=true") {
		t.Errorf("genuine-leaf trace must not suggest include_tests=true (nothing was filtered); got %q", diag)
	}
}

// Carry through kinds / direction when surfacing the retry hint so the
// re-invocation reproduces the same query shape (just with the filter
// disabled).
func TestHandleTrace_FilteredHint_PreservesKindsAndDirection(t *testing.T) {
	t.Parallel()
	srv := seedTraceWithOnlyTestCallers(t, "p898c")

	result, err := srv.handleTrace(context.Background(), makeReq(map[string]any{
		"name":      "OnlyTestedTarget",
		"direction": "inbound",
		"kinds":     "CALLS",
	}))
	if err != nil {
		t.Fatal(err)
	}
	body := decode(t, result)
	meta, _ := body["_meta"].(map[string]any)
	steps, _ := meta["next_steps"].([]any)
	if len(steps) == 0 {
		t.Fatal("expected next_steps")
	}
	argsStr, _ := steps[0].(map[string]any)["args"].(string)
	for _, want := range []string{`"include_tests":true`, `"direction":"inbound"`, `"kinds":"CALLS"`} {
		if !strings.Contains(argsStr, want) {
			t.Errorf("retry args should contain %s; got %q", want, argsStr)
		}
	}
}
