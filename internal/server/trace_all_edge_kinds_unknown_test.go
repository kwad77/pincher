// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #1096: companion to #1094 (dead_code all-unknown kinds). Pre-fix,
// trace with kinds="BogusKind" dropped the unknown value from
// edgeKinds, leaving the list empty — SQL ran with no edge-kind
// filter and returned ALL edge kinds. Same silent-confidently-wrong:
// the warning named the bad value but the result contradicted it.
// Now: an all-unknown edge-kinds filter rejects with a rich envelope.

func TestHandleTrace_AllEdgeKindsUnknown_RichEnvelope(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "p-trace-kinds"
	store.UpsertProject(db.Project{
		ID: pid, Path: t.TempDir(), Name: pid, IndexedAt: time.Now(),
		FileCount: 1, SymCount: 2, EdgeCount: 1,
	})
	srv.sessionID = pid
	syms := []db.Symbol{
		{
			ID: pid + "::pkg.A#Function", ProjectID: pid, FilePath: "a.go",
			Name: "A", QualifiedName: "pkg.A", Kind: "Function",
			Language: "Go", ExtractionConfidence: 1.0,
		},
		{
			ID: pid + "::pkg.B#Function", ProjectID: pid, FilePath: "a.go",
			Name: "B", QualifiedName: "pkg.B", Kind: "Function",
			Language: "Go", ExtractionConfidence: 1.0,
		},
	}
	mustUpsertSymbols(t, store, syms)
	if err := store.BulkUpsertEdges([]db.Edge{
		{ProjectID: pid, FromID: syms[0].ID, ToID: syms[1].ID, Kind: "CALLS", Confidence: 1.0},
	}); err != nil {
		t.Fatalf("BulkUpsertEdges: %v", err)
	}

	res, err := srv.handleTrace(context.Background(), makeReq(map[string]any{
		"name":  "A",
		"kinds": "BogusKind",
	}))
	if err != nil {
		t.Fatalf("handleTrace: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError on all-unknown kinds; got %s", textOf(t, res))
	}
	body := decode(t, res)
	errStr, _ := body["error"].(string)
	if !strings.Contains(errStr, "BogusKind") {
		t.Errorf("error must name the bad kind value; got %q", errStr)
	}
	if !strings.Contains(errStr, "CALLS") {
		t.Errorf("error must list valid edge kinds; got %q", errStr)
	}
	meta, _ := body["_meta"].(map[string]any)
	if meta == nil {
		t.Fatalf("expected _meta envelope; got bare error %q", errStr)
	}
	stepsRaw, _ := meta["next_steps"].([]any)
	if len(stepsRaw) < 3 {
		t.Fatalf("expected ≥3 next_steps; got %d", len(stepsRaw))
	}
	wantTools := map[string]int{"trace": 0, "schema": 0}
	for _, s := range stepsRaw {
		step, _ := s.(map[string]any)
		if tool, _ := step["tool"].(string); tool != "" {
			wantTools[tool]++
		}
	}
	if wantTools["trace"] < 2 {
		t.Errorf("expected ≥2 trace next_steps (drop-filter + CALLS-specific); got %d", wantTools["trace"])
	}
	if wantTools["schema"] != 1 {
		t.Errorf("expected 1 schema next_step; got %d", wantTools["schema"])
	}
}

// Control: a MIXED edge-kinds list (one valid, one bogus) keeps the
// existing drop-bad-keep-good behavior — CALLS survives, BogusKind is
// dropped with a warning. The hard-reject branch must NOT fire.
func TestHandleTrace_MixedEdgeKinds_DropsBadKeepsGood(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "p-trace-mixed"
	store.UpsertProject(db.Project{
		ID: pid, Path: t.TempDir(), Name: pid, IndexedAt: time.Now(),
		FileCount: 1, SymCount: 2, EdgeCount: 1,
	})
	srv.sessionID = pid
	syms := []db.Symbol{
		{
			ID: pid + "::pkg.A#Function", ProjectID: pid, FilePath: "a.go",
			Name: "A", QualifiedName: "pkg.A", Kind: "Function",
			Language: "Go", ExtractionConfidence: 1.0,
		},
		{
			ID: pid + "::pkg.B#Function", ProjectID: pid, FilePath: "a.go",
			Name: "B", QualifiedName: "pkg.B", Kind: "Function",
			Language: "Go", ExtractionConfidence: 1.0,
		},
	}
	mustUpsertSymbols(t, store, syms)
	if err := store.BulkUpsertEdges([]db.Edge{
		{ProjectID: pid, FromID: syms[0].ID, ToID: syms[1].ID, Kind: "CALLS", Confidence: 1.0},
	}); err != nil {
		t.Fatalf("BulkUpsertEdges: %v", err)
	}

	res, err := srv.handleTrace(context.Background(), makeReq(map[string]any{
		"name":  "A",
		"kinds": "CALLS,BogusKind",
	}))
	if err != nil {
		t.Fatalf("handleTrace: %v", err)
	}
	if res.IsError {
		t.Fatalf("mixed kinds (one valid) must not error; got %s", textOf(t, res))
	}
}
