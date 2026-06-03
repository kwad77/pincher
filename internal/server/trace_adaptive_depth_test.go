// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #402: when many direct callers exist (>=5 at depth=1), trace
// auto-trims to depth=1; agent doesn't get 50+ depth-2/3 hops.
func TestHandleTrace_AutoTrim_ManyDirectCallers_StaysShallow(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p1"
	store.UpsertProject(db.Project{ID: "p1", Path: "/tmp/p1", Name: "p1", IndexedAt: time.Now()})

	// Target with 8 direct (depth=1) callers, plus 10 indirect
	// (depth=2) callers behind some of those direct ones.
	syms := []db.Symbol{
		{ID: "p1::pkg.Target#Function", ProjectID: "p1", FilePath: "svc/svc.go",
			Name: "Target", QualifiedName: "pkg.Target", Kind: "Function", Language: "Go",
			ExtractionConfidence: 1.0},
	}
	var edges []db.Edge
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("p1::pkg.direct%d#Function", i)
		syms = append(syms, db.Symbol{ID: id, ProjectID: "p1", FilePath: "svc/svc.go",
			Name: fmt.Sprintf("direct%d", i), QualifiedName: fmt.Sprintf("pkg.direct%d", i),
			Kind: "Function", Language: "Go", ExtractionConfidence: 1.0})
		edges = append(edges, db.Edge{ProjectID: "p1", FromID: id,
			ToID: "p1::pkg.Target#Function", Kind: "CALLS", Confidence: 1})
	}
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("p1::pkg.indirect%d#Function", i)
		syms = append(syms, db.Symbol{ID: id, ProjectID: "p1", FilePath: "svc/svc.go",
			Name: fmt.Sprintf("indirect%d", i), QualifiedName: fmt.Sprintf("pkg.indirect%d", i),
			Kind: "Function", Language: "Go", ExtractionConfidence: 1.0})
		// Each indirect calls direct0 → reaches Target at depth=2.
		edges = append(edges, db.Edge{ProjectID: "p1", FromID: id,
			ToID: "p1::pkg.direct0#Function", Kind: "CALLS", Confidence: 1})
	}
	mustUpsertSymbols(t, store, syms)
	if err := store.BulkUpsertEdges(edges); err != nil {
		t.Fatal(err)
	}

	// Default (no depth arg): auto-trim should kick in and stop at
	// depth=1 because we have 8 direct callers (>=5).
	result, err := srv.handleTrace(context.Background(), makeReq(map[string]any{
		"name":      "Target",
		"direction": "inbound",
	}))
	if err != nil {
		t.Fatal(err)
	}
	body := decode(t, result)
	meta, _ := body["_meta"].(map[string]any)
	if v, ok := meta["depth_used"].(float64); !ok || int(v) != 1 {
		t.Errorf("expected _meta.depth_used=1, got %v", meta["depth_used"])
	}
	// Total hops should be the 8 direct callers — no indirect ones.
	if total, ok := body["total"].(float64); !ok || int(total) != 8 {
		t.Errorf("expected 8 hops (depth=1 trim), got total=%v", body["total"])
	}
}

// Few direct callers (<5) → auto-deepen continues until threshold
// or max depth reached.
func TestHandleTrace_AutoTrim_FewDirectCallers_Deepens(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p2"
	store.UpsertProject(db.Project{ID: "p2", Path: "/tmp/p2", Name: "p2", IndexedAt: time.Now()})

	// Target with 1 direct caller, 4 indirect via that caller.
	// Threshold=5, so depth=1 has 1 hop (insufficient), depth=2 has
	// 5 hops (sufficient) → depth_used=2.
	syms := []db.Symbol{
		{ID: "p2::pkg.Target#Function", ProjectID: "p2", FilePath: "svc/svc.go",
			Name: "Target", QualifiedName: "pkg.Target", Kind: "Function", Language: "Go",
			ExtractionConfidence: 1.0},
		{ID: "p2::pkg.direct0#Function", ProjectID: "p2", FilePath: "svc/svc.go",
			Name: "direct0", QualifiedName: "pkg.direct0", Kind: "Function", Language: "Go",
			ExtractionConfidence: 1.0},
	}
	edges := []db.Edge{
		{ProjectID: "p2", FromID: "p2::pkg.direct0#Function",
			ToID: "p2::pkg.Target#Function", Kind: "CALLS", Confidence: 1},
	}
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("p2::pkg.indirect%d#Function", i)
		syms = append(syms, db.Symbol{ID: id, ProjectID: "p2", FilePath: "svc/svc.go",
			Name: fmt.Sprintf("indirect%d", i), QualifiedName: fmt.Sprintf("pkg.indirect%d", i),
			Kind: "Function", Language: "Go", ExtractionConfidence: 1.0})
		edges = append(edges, db.Edge{ProjectID: "p2", FromID: id,
			ToID: "p2::pkg.direct0#Function", Kind: "CALLS", Confidence: 1})
	}
	mustUpsertSymbols(t, store, syms)
	store.BulkUpsertEdges(edges)

	result, err := srv.handleTrace(context.Background(), makeReq(map[string]any{
		"name":      "Target",
		"direction": "inbound",
	}))
	if err != nil {
		t.Fatal(err)
	}
	body := decode(t, result)
	meta, _ := body["_meta"].(map[string]any)
	if v, ok := meta["depth_used"].(float64); !ok || int(v) != 2 {
		t.Errorf("expected _meta.depth_used=2 (auto-deepened past depth=1), got %v", meta["depth_used"])
	}
}

// Explicit `depth=N` from the caller skips auto-trim entirely. The
// _meta.depth_used field should NOT appear.
func TestHandleTrace_ExplicitDepth_SkipsAutoTrim(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p3"
	store.UpsertProject(db.Project{ID: "p3", Path: "/tmp/p3", Name: "p3", IndexedAt: time.Now()})

	// Same setup as ManyDirectCallers — 8 direct callers + 10 indirect.
	syms := []db.Symbol{
		{ID: "p3::pkg.Target#Function", ProjectID: "p3", FilePath: "svc/svc.go",
			Name: "Target", QualifiedName: "pkg.Target", Kind: "Function", Language: "Go",
			ExtractionConfidence: 1.0},
	}
	var edges []db.Edge
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("p3::pkg.direct%d#Function", i)
		syms = append(syms, db.Symbol{ID: id, ProjectID: "p3", FilePath: "svc/svc.go",
			Name: fmt.Sprintf("direct%d", i), QualifiedName: fmt.Sprintf("pkg.direct%d", i),
			Kind: "Function", Language: "Go", ExtractionConfidence: 1.0})
		edges = append(edges, db.Edge{ProjectID: "p3", FromID: id,
			ToID: "p3::pkg.Target#Function", Kind: "CALLS", Confidence: 1})
	}
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("p3::pkg.indirect%d#Function", i)
		syms = append(syms, db.Symbol{ID: id, ProjectID: "p3", FilePath: "svc/svc.go",
			Name: fmt.Sprintf("indirect%d", i), QualifiedName: fmt.Sprintf("pkg.indirect%d", i),
			Kind: "Function", Language: "Go", ExtractionConfidence: 1.0})
		edges = append(edges, db.Edge{ProjectID: "p3", FromID: id,
			ToID: "p3::pkg.direct0#Function", Kind: "CALLS", Confidence: 1})
	}
	mustUpsertSymbols(t, store, syms)
	store.BulkUpsertEdges(edges)

	// Explicit depth=3 — should NOT auto-trim.
	result, err := srv.handleTrace(context.Background(), makeReq(map[string]any{
		"name":      "Target",
		"direction": "inbound",
		"depth":     3,
	}))
	if err != nil {
		t.Fatal(err)
	}
	body := decode(t, result)
	meta, _ := body["_meta"].(map[string]any)
	if _, hasDepthUsed := meta["depth_used"]; hasDepthUsed {
		t.Errorf("explicit depth=3 should NOT emit _meta.depth_used; got %v", meta)
	}
	// Total hops include depth=1 directs + depth=2 indirects → ≥18.
	if total, ok := body["total"].(float64); !ok || int(total) < 18 {
		t.Errorf("explicit depth=3 should return all hops; got total=%v", body["total"])
	}
}
