// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #1431 (trace arm): scope-to-session-first when projectArg is empty.
// Mirrors the #1409 handleSymbol fix and the #1425 handleNeighborhood
// fix — trace was missed by both. Pre-fix repro on the dogfood DB:
// pincher-repo and sniffer (a fork) both have
// `internal/db/db.go::db.Open#Function`. `mcp__pincher__trace id=...`
// without a project arg looked up via unscoped GetSymbol, hit sniffer's
// row first, emitted a misleading "hops will silently be 0" warning,
// then ran the BFS in pincher-repo's graph and returned 16 actual
// hops. Mixed signal — the warning contradicted the data.

// Positive — seed ID in BOTH session AND mirror project. Without
// explicit project=, trace must prefer the session project's row and
// NOT emit the cross-project warning (because the seed IS in the
// session).
func TestHandleTrace_DuplicateIDInTwoProjects_PrefersSession_NoFalseWarning(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	sessionPID := "p-trace-session"
	mirrorPID := "p-trace-mirror"
	if err := store.UpsertProject(db.Project{
		ID: sessionPID, Path: t.TempDir(), Name: sessionPID, IndexedAt: time.Now(),
	}); err != nil {
		t.Fatalf("UpsertProject session: %v", err)
	}
	if err := store.UpsertProject(db.Project{
		ID: mirrorPID, Path: t.TempDir(), Name: mirrorPID, IndexedAt: time.Now(),
	}); err != nil {
		t.Fatalf("UpsertProject mirror: %v", err)
	}
	srv.sessionID = sessionPID

	// Same ID in BOTH projects (canonical fork shape).
	mustUpsertSymbols(t, store, []db.Symbol{
		{ID: "shared.go::pkg.Seed#Function", ProjectID: sessionPID, FilePath: "shared.go",
			Name: "Seed", QualifiedName: "pkg.Seed", Kind: "Function", Language: "Go",
			ExtractionConfidence: 1.0},
		{ID: "shared.go::pkg.Seed#Function", ProjectID: mirrorPID, FilePath: "shared.go",
			Name: "Seed", QualifiedName: "pkg.Seed", Kind: "Function", Language: "Go",
			ExtractionConfidence: 1.0},
	})

	result, err := srv.handleTrace(context.Background(), makeReq(map[string]any{
		"id":    "shared.go::pkg.Seed#Function",
		"depth": float64(1),
	}))
	if err != nil {
		t.Fatalf("handleTrace: %v", err)
	}
	if result.IsError {
		body := decode(t, result)
		t.Fatalf("must not error when seed is in session project; got: %v", body["error"])
	}
	body := decode(t, result)
	meta, _ := body["_meta"].(map[string]any)
	if meta == nil {
		return // OK, no _meta means no warning
	}
	warnings, _ := meta["warnings"].([]any)
	for _, w := range warnings {
		s, _ := w.(string)
		// The pre-fix bug surfaced "hops will silently be 0" naming
		// the mirror project as where the seed "lives". With the
		// scope-to-session fix, this string must not appear because
		// the seed IS in the session project.
		if strings.Contains(s, "silently be 0") || strings.Contains(s, mirrorPID) {
			t.Errorf("trace emitted misleading cross-project warning despite seed being in session project (#1431): %s", s)
		}
	}
}

// Cross-check — when seed ID is ONLY in the mirror project, the
// existing #1052 cross-project warning STILL fires (regression guard).
// The #1431 fix only changes behaviour for the duplicate-ID case; the
// genuine cross-project case keeps its warning.
func TestHandleTrace_OnlyInOtherProject_CrossProjectWarningStillFires(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	sessionPID := "p-trace-session"
	mirrorPID := "p-trace-mirror"
	if err := store.UpsertProject(db.Project{
		ID: sessionPID, Path: t.TempDir(), Name: sessionPID, IndexedAt: time.Now(),
	}); err != nil {
		t.Fatalf("UpsertProject session: %v", err)
	}
	if err := store.UpsertProject(db.Project{
		ID: mirrorPID, Path: t.TempDir(), Name: mirrorPID, IndexedAt: time.Now(),
	}); err != nil {
		t.Fatalf("UpsertProject mirror: %v", err)
	}
	srv.sessionID = sessionPID

	// Seed ONLY in the mirror project.
	mustUpsertSymbols(t, store, []db.Symbol{
		{ID: "shared.go::pkg.OnlyMirror#Function", ProjectID: mirrorPID, FilePath: "shared.go",
			Name: "OnlyMirror", QualifiedName: "pkg.OnlyMirror", Kind: "Function", Language: "Go",
			ExtractionConfidence: 1.0},
	})

	result, err := srv.handleTrace(context.Background(), makeReq(map[string]any{
		"id":    "shared.go::pkg.OnlyMirror#Function",
		"depth": float64(1),
	}))
	if err != nil {
		t.Fatalf("handleTrace: %v", err)
	}
	body := decode(t, result)
	meta, _ := body["_meta"].(map[string]any)
	warnings, _ := meta["warnings"].([]any)
	sawCrossProjectWarning := false
	for _, w := range warnings {
		s, _ := w.(string)
		if strings.Contains(s, mirrorPID) && strings.Contains(s, "silently be 0") {
			sawCrossProjectWarning = true
			break
		}
	}
	if !sawCrossProjectWarning {
		t.Errorf("genuine cross-project seed must STILL get the #1052 warning (regression guard); warnings=%v", warnings)
	}
}
