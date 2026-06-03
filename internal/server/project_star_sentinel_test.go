// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #1048: symbol / context / neighborhood / symbols (batch) treated
// project="*" as an unknown project name and emitted a misleading
// "did not resolve — falling back" warning even though the unscoped
// lookup returned the right answer. search and query already accept
// "*" as the cross-project sentinel; the consistency gap meant an
// agent could write a single workflow (search project=* → context
// project=*) and get useful results from search but a misleading
// warning from context. Now all four tools accept "*" silently as
// the documented "no scoping" sentinel.

func setupStarSentinelProject(t *testing.T) (*Server, *db.Store, string) {
	t.Helper()
	srv, store, _ := newTestServer(t)
	pid := "p-star"
	store.UpsertProject(db.Project{
		ID: pid, Path: t.TempDir(), Name: pid, IndexedAt: time.Now(),
	})
	mustUpsertSymbols(t, store, []db.Symbol{
		{ID: "a.go::pkg.Foo#Function", ProjectID: pid, FilePath: "a.go",
			Name: "Foo", QualifiedName: "pkg.Foo", Kind: "Function", Language: "Go",
			ExtractionConfidence: 1.0},
	})
	return srv, store, pid
}

func TestHandleSymbol_StarSentinel_NoWarning(t *testing.T) {
	t.Parallel()
	srv, _, _ := setupStarSentinelProject(t)

	result, err := srv.handleSymbol(context.Background(), makeReq(map[string]any{
		"id":      "a.go::pkg.Foo#Function",
		"project": "*",
	}))
	if err != nil {
		t.Fatalf("handleSymbol: %v", err)
	}
	body := decode(t, result)
	assertNoDidNotResolveWarning(t, body, "*")
}

func TestHandleContext_StarSentinel_NoWarning(t *testing.T) {
	t.Parallel()
	srv, _, _ := setupStarSentinelProject(t)

	result, err := srv.handleContext(context.Background(), makeReq(map[string]any{
		"id":      "a.go::pkg.Foo#Function",
		"project": "*",
		"lite":    true,
	}))
	if err != nil {
		t.Fatalf("handleContext: %v", err)
	}
	body := decode(t, result)
	assertNoDidNotResolveWarning(t, body, "*")
}

func TestHandleNeighborhood_StarSentinel_NoWarning(t *testing.T) {
	t.Parallel()
	srv, _, _ := setupStarSentinelProject(t)

	result, err := srv.handleNeighborhood(context.Background(), makeReq(map[string]any{
		"id":      "a.go::pkg.Foo#Function",
		"project": "*",
	}))
	if err != nil {
		t.Fatalf("handleNeighborhood: %v", err)
	}
	body := decode(t, result)
	assertNoDidNotResolveWarning(t, body, "*")
}

func TestHandleSymbols_StarSentinel_NoWarning(t *testing.T) {
	t.Parallel()
	srv, _, _ := setupStarSentinelProject(t)

	result, err := srv.handleSymbols(context.Background(), makeReq(map[string]any{
		"ids":     []any{"a.go::pkg.Foo#Function"},
		"project": "*",
	}))
	if err != nil {
		t.Fatalf("handleSymbols: %v", err)
	}
	body := decode(t, result)
	assertNoDidNotResolveWarning(t, body, "*")
}

// Control: a genuinely unknown project arg (not "*") still warns.
func TestHandleSymbol_UnknownProject_StillWarns(t *testing.T) {
	t.Parallel()
	srv, _, _ := setupStarSentinelProject(t)

	result, err := srv.handleSymbol(context.Background(), makeReq(map[string]any{
		"id":      "a.go::pkg.Foo#Function",
		"project": "totally-bogus-project",
	}))
	if err != nil {
		t.Fatalf("handleSymbol: %v", err)
	}
	body := decode(t, result)
	meta, _ := body["_meta"].(map[string]any)
	if meta == nil {
		t.Fatal("_meta missing — expected project-resolve warning on unknown project")
	}
	warnings, _ := meta["warnings"].([]any)
	saw := false
	for _, w := range warnings {
		if s, _ := w.(string); strings.Contains(s, "totally-bogus-project") &&
			strings.Contains(s, "did not resolve") {
			saw = true
			break
		}
	}
	if !saw {
		t.Errorf("expected did-not-resolve warning naming totally-bogus-project; got %v", warnings)
	}
}

func assertNoDidNotResolveWarning(t *testing.T, body map[string]any, projectArg string) {
	t.Helper()
	meta, _ := body["_meta"].(map[string]any)
	if meta == nil {
		return
	}
	warnings, _ := meta["warnings"].([]any)
	for _, w := range warnings {
		s, _ := w.(string)
		if strings.Contains(s, "did not resolve") && strings.Contains(s, projectArg) {
			t.Errorf("project=%q is a sentinel; must not warn 'did not resolve'; got %q",
				projectArg, s)
		}
	}
}
