// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #1052: trace's id-mode does an unscoped GetSymbol — the seed can
// land in ANY indexed project that carries the id. But the BFS
// traversal scopes to `projectID` (the session by default). When
// those diverge, hops silently come back empty because edges are
// project-scoped to the seed's project, not the BFS's.

func TestHandleTrace_IDMode_CrossProjectSeed_Warns(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	sessionPID := "p-trace-session"
	mirrorPID := "p-trace-mirror"
	store.UpsertProject(db.Project{
		ID: sessionPID, Path: t.TempDir(), Name: sessionPID, IndexedAt: time.Now(),
	})
	store.UpsertProject(db.Project{
		ID: mirrorPID, Path: t.TempDir(), Name: mirrorPID, IndexedAt: time.Now(),
	})
	srv.sessionID = sessionPID

	// Seed lives only in the mirror project; session has nothing at this id.
	mustUpsertSymbols(t, store, []db.Symbol{
		{ID: "shared.go::pkg.Common#Function", ProjectID: mirrorPID, FilePath: "shared.go",
			Name: "Common", QualifiedName: "pkg.Common", Kind: "Function", Language: "Go",
			ExtractionConfidence: 1.0},
	})

	result, err := srv.handleTrace(context.Background(), makeReq(map[string]any{
		"id":    "shared.go::pkg.Common#Function",
		"depth": float64(1),
	}))
	if err != nil {
		t.Fatalf("handleTrace: %v", err)
	}
	body := decode(t, result)
	meta, _ := body["_meta"].(map[string]any)
	if meta == nil {
		t.Fatal("_meta missing — expected cross-project warning")
	}
	warnings, _ := meta["warnings"].([]any)
	saw := false
	for _, w := range warnings {
		s, _ := w.(string)
		if strings.Contains(s, "resolved from project") &&
			strings.Contains(s, mirrorPID) &&
			strings.Contains(s, sessionPID) &&
			strings.Contains(s, "hops will silently be 0") {
			saw = true
			break
		}
	}
	if !saw {
		t.Errorf("expected cross-project warning naming mirror+session+0-hops cause; got %v", warnings)
	}
}

// Control: id-mode with seed in the session project — no warning.
func TestHandleTrace_IDMode_SeedInSessionProject_NoWarning(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	sessionPID := "p-trace-ok"
	store.UpsertProject(db.Project{
		ID: sessionPID, Path: t.TempDir(), Name: sessionPID, IndexedAt: time.Now(),
	})
	srv.sessionID = sessionPID

	mustUpsertSymbols(t, store, []db.Symbol{
		{ID: "a.go::pkg.Foo#Function", ProjectID: sessionPID, FilePath: "a.go",
			Name: "Foo", QualifiedName: "pkg.Foo", Kind: "Function", Language: "Go",
			ExtractionConfidence: 1.0},
	})

	result, err := srv.handleTrace(context.Background(), makeReq(map[string]any{
		"id":    "a.go::pkg.Foo#Function",
		"depth": float64(1),
	}))
	if err != nil {
		t.Fatalf("handleTrace: %v", err)
	}
	body := decode(t, result)
	meta, _ := body["_meta"].(map[string]any)
	if meta == nil {
		return
	}
	warnings, _ := meta["warnings"].([]any)
	for _, w := range warnings {
		s, _ := w.(string)
		if strings.Contains(s, "hops will silently be 0") {
			t.Errorf("session-scoped seed must not warn cross-project; got %q", s)
		}
	}
}

// Control: explicit project arg means the caller already pinned scope.
func TestHandleTrace_IDMode_ExplicitProject_NoCrossProjectWarning(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	sessionPID := "p-trace-exp"
	otherPID := "p-trace-other"
	store.UpsertProject(db.Project{
		ID: sessionPID, Path: t.TempDir(), Name: sessionPID, IndexedAt: time.Now(),
	})
	store.UpsertProject(db.Project{
		ID: otherPID, Path: t.TempDir(), Name: otherPID, IndexedAt: time.Now(),
	})
	srv.sessionID = sessionPID

	mustUpsertSymbols(t, store, []db.Symbol{
		{ID: "b.go::pkg.Bar#Function", ProjectID: otherPID, FilePath: "b.go",
			Name: "Bar", QualifiedName: "pkg.Bar", Kind: "Function", Language: "Go",
			ExtractionConfidence: 1.0},
	})

	result, err := srv.handleTrace(context.Background(), makeReq(map[string]any{
		"id":      "b.go::pkg.Bar#Function",
		"project": otherPID,
		"depth":   float64(1),
	}))
	if err != nil {
		t.Fatalf("handleTrace: %v", err)
	}
	body := decode(t, result)
	meta, _ := body["_meta"].(map[string]any)
	if meta == nil {
		return
	}
	warnings, _ := meta["warnings"].([]any)
	for _, w := range warnings {
		s, _ := w.(string)
		if strings.Contains(s, "hops will silently be 0") {
			t.Errorf("explicit project arg must not trip cross-project warning; got %q", s)
		}
	}
}
