// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #1044: handleDeadCode silently returned confidently-wrong FPs on
// ghost-extraction projects. With no inbound edges anywhere in the
// project (substantial symbol count, ZERO edges — the #815 resolver-
// phase failure shape), EVERY function looks unreferenced. Pre-fix
// the populated dead_symbols list was guidance toward deletion of
// what are categorically false positives. The empty-result path was
// also misleading on a ghost project — "lower min_confidence" can't
// help when the resolver, not the floor, produced zero edges. Same
// family as #1040 (architecture) / #1042 (schema) / #1043 (query) +
// the existing #1009 doctor advisory.

func TestHandleDeadCode_GhostExtractionProject_DiagnoseGhost(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "p-dead-ghost"
	store.UpsertProject(db.Project{
		ID: pid, Path: t.TempDir(), Name: pid, IndexedAt: time.Now(),
		FileCount: 10, SymCount: 150, EdgeCount: 0,
	})
	srv.sessionID = pid

	// Seed 150 callable Go symbols (above the 100 threshold), NO edges.
	// All look "dead" because nothing has an inbound edge.
	syms := []db.Symbol{}
	for i := 0; i < 150; i++ {
		syms = append(syms, db.Symbol{
			ID:                   pid + "::pkg.F" + string(rune('A'+i%26)) + string(rune('A'+i/26)) + "#Function",
			ProjectID:            pid,
			FilePath:             "a.go",
			Name:                 "F" + string(rune('A'+i%26)) + string(rune('A'+i/26)),
			QualifiedName:        "pkg.F" + string(rune('A'+i%26)) + string(rune('A'+i/26)),
			Kind:                 "Function",
			Language:             "Go",
			ExtractionConfidence: 1.0,
		})
	}
	mustUpsertSymbols(t, store, syms)

	result, err := srv.handleDeadCode(context.Background(), makeReq(map[string]any{
		"limit": float64(5),
	}))
	if err != nil {
		t.Fatalf("handleDeadCode: %v", err)
	}
	body := decode(t, result)

	meta, _ := body["_meta"].(map[string]any)
	if meta == nil {
		t.Fatal("_meta missing — expected ghost-extraction diagnosis")
	}
	diagnosis, _ := meta["diagnosis"].(string)
	if !strings.Contains(diagnosis, "ghost-extraction") {
		t.Errorf("expected ghost-extraction diagnosis; got %q", diagnosis)
	}
	// Next_steps should point at architecture + doctor, NOT the "verify
	// the dead symbol" path that the populated-result branch would build.
	steps, _ := meta["next_steps"].([]any)
	sawArch, sawDoctor := false, false
	for _, st := range steps {
		m, _ := st.(map[string]any)
		switch m["tool"] {
		case "architecture":
			sawArch = true
		case "doctor":
			sawDoctor = true
		}
	}
	if !sawArch || !sawDoctor {
		t.Errorf("ghost next_steps should include architecture + doctor; got %v", steps)
	}
}

// Control: healthy project (symbols + edges) must not get the ghost
// diagnosis — the existing next_steps about verifying the top dead
// symbol stay intact.
func TestHandleDeadCode_HealthyProject_NoGhostDiagnosis(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "p-dead-healthy"
	store.UpsertProject(db.Project{
		ID: pid, Path: t.TempDir(), Name: pid, IndexedAt: time.Now(),
	})
	srv.sessionID = pid

	mustUpsertSymbols(t, store, []db.Symbol{
		{ID: pid + "::pkg.Caller#Function", ProjectID: pid, FilePath: "a.go",
			Name: "Caller", QualifiedName: "pkg.Caller", Kind: "Function", Language: "Go",
			ExtractionConfidence: 1.0},
		{ID: pid + "::pkg.Dead#Function", ProjectID: pid, FilePath: "a.go",
			Name: "Dead", QualifiedName: "pkg.Dead", Kind: "Function", Language: "Go",
			ExtractionConfidence: 1.0},
		{ID: pid + "::pkg.Target#Function", ProjectID: pid, FilePath: "a.go",
			Name: "Target", QualifiedName: "pkg.Target", Kind: "Function", Language: "Go",
			ExtractionConfidence: 1.0},
	})
	// Caller -> Target. Dead has no inbound.
	if err := store.BulkUpsertEdges([]db.Edge{
		{ProjectID: pid, FromID: pid + "::pkg.Caller#Function", ToID: pid + "::pkg.Target#Function",
			Kind: "CALLS", Confidence: 1},
	}); err != nil {
		t.Fatal(err)
	}

	result, err := srv.handleDeadCode(context.Background(), makeReq(map[string]any{}))
	if err != nil {
		t.Fatalf("handleDeadCode: %v", err)
	}
	body := decode(t, result)
	meta, _ := body["_meta"].(map[string]any)
	if meta == nil {
		return
	}
	diagnosis, _ := meta["diagnosis"].(string)
	if strings.Contains(diagnosis, "ghost-extraction") {
		t.Errorf("healthy project must not get ghost diagnosis; got %q", diagnosis)
	}
}

// Control: small project (below the 100-symbol threshold) with 0 edges
// must not get the ghost diagnosis — a 5-symbol toy project legitimately
// might have no edges, and the false-positive cost of ghost-flagging
// every small project outweighs the signal.
func TestHandleDeadCode_SmallProjectZeroEdges_NoGhostDiagnosis(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "p-dead-small"
	store.UpsertProject(db.Project{
		ID: pid, Path: t.TempDir(), Name: pid, IndexedAt: time.Now(),
	})
	srv.sessionID = pid

	mustUpsertSymbols(t, store, []db.Symbol{
		{ID: pid + "::pkg.A#Function", ProjectID: pid, FilePath: "a.go",
			Name: "A", QualifiedName: "pkg.A", Kind: "Function", Language: "Go",
			ExtractionConfidence: 1.0},
	})

	result, err := srv.handleDeadCode(context.Background(), makeReq(map[string]any{}))
	if err != nil {
		t.Fatalf("handleDeadCode: %v", err)
	}
	body := decode(t, result)
	meta, _ := body["_meta"].(map[string]any)
	if meta == nil {
		return
	}
	diagnosis, _ := meta["diagnosis"].(string)
	if strings.Contains(diagnosis, "ghost-extraction") {
		t.Errorf("small project (1 symbol) must not get ghost diagnosis; got %q", diagnosis)
	}
}
