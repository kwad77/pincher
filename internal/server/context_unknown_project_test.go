// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #1039: context's schema declared a `project` arg but the handler
// ignored it entirely. A typo'd project name silently fell through
// to the unscoped lookup. Same contract-drift family as #1024 (stats)
// / #1028 (guide). Now: warn on success, stack into not-found error.

func TestHandleContext_UnknownProject_WarnsAndFallsBack(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "p-ctx-project"
	store.UpsertProject(db.Project{
		ID: pid, Path: t.TempDir(), Name: pid, IndexedAt: time.Now(),
	})
	srv.sessionID = pid

	mustUpsertSymbols(t, store, []db.Symbol{
		{ID: pid + "::pkg.CtxProbe#Function", ProjectID: pid, FilePath: "f.go",
			Name: "CtxProbe", QualifiedName: "pkg.CtxProbe", Kind: "Function", Language: "Go",
			Signature: "func CtxProbe()", ExtractionConfidence: 1.0},
	})

	result, err := srv.handleContext(context.Background(), makeReq(map[string]any{
		"id":      pid + "::pkg.CtxProbe#Function",
		"project": "totally-bogus-project",
	}))
	if err != nil {
		t.Fatalf("handleContext: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success (fallback); got error: %s", textOf(t, result))
	}

	body := decode(t, result)
	meta, _ := body["_meta"].(map[string]any)
	warnings, _ := meta["warnings"].([]any)
	foundWarn := false
	for _, w := range warnings {
		if s, _ := w.(string); strings.Contains(s, "totally-bogus-project") && strings.Contains(s, "did not resolve") {
			foundWarn = true
			break
		}
	}
	if !foundWarn {
		t.Errorf("expected project-resolution warning; got warnings=%v", warnings)
	}
}

// Not-found + bogus project should stack both failures (mirrors #1037 / #1038).
func TestHandleContext_BogusProjectAndBogusID_BothFailuresSurfaced(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "p-ctx-both"
	store.UpsertProject(db.Project{
		ID: pid, Path: t.TempDir(), Name: pid, IndexedAt: time.Now(),
	})
	srv.sessionID = pid

	result, err := srv.handleContext(context.Background(), makeReq(map[string]any{
		"id":      "does/not/exist.go::pkg.NoSuchThing#Function",
		"project": "totally-bogus-project",
	}))
	if err != nil {
		t.Fatalf("handleContext: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected error result; got success")
	}
	body := decode(t, result)
	errMsg, _ := body["error"].(string)
	if !strings.Contains(errMsg, "totally-bogus-project") {
		t.Errorf("error must surface project-resolve failure; got %q", errMsg)
	}
	if !strings.Contains(errMsg, "not found") {
		t.Errorf("error must still report the symbol-not-found failure; got %q", errMsg)
	}
}
