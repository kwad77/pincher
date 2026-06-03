// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #1045: handleDeadCode silently returned empty results with the
// misleading "lower min_confidence" hint when language= filtered to
// a language the project had zero symbols of. Same shape as the
// kinds validation (#851) and search's language diagnosis — the
// caller couldn't tell "Java has no dead code" from "no Java
// symbols exist in this project at all". Now warns with the
// project's actual language histogram.

func TestHandleDeadCode_UnknownLanguageInProject_Warns(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "p-deadlang"
	store.UpsertProject(db.Project{
		ID: pid, Path: t.TempDir(), Name: pid, IndexedAt: time.Now(),
	})
	srv.sessionID = pid

	// Seed Go symbols only; no Java symbols at all.
	mustUpsertSymbols(t, store, []db.Symbol{
		{ID: pid + "::pkg.Caller#Function", ProjectID: pid, FilePath: "a.go",
			Name: "Caller", QualifiedName: "pkg.Caller", Kind: "Function", Language: "Go",
			ExtractionConfidence: 1.0},
		{ID: pid + "::pkg.Dead#Function", ProjectID: pid, FilePath: "a.go",
			Name: "Dead", QualifiedName: "pkg.Dead", Kind: "Function", Language: "Go",
			ExtractionConfidence: 1.0},
	})

	result, err := srv.handleDeadCode(context.Background(), makeReq(map[string]any{
		"language": "Java",
		"limit":    float64(5),
	}))
	if err != nil {
		t.Fatalf("handleDeadCode: %v", err)
	}
	body := decode(t, result)
	meta, _ := body["_meta"].(map[string]any)
	if meta == nil {
		t.Fatal("_meta missing — expected language-mismatch warning")
	}
	warnings, _ := meta["warnings"].([]any)
	sawLangWarn := false
	for _, w := range warnings {
		if s, _ := w.(string); strings.Contains(s, `language="Java"`) &&
			strings.Contains(s, "0 symbols") &&
			strings.Contains(s, "Go") {
			sawLangWarn = true
			break
		}
	}
	if !sawLangWarn {
		t.Errorf("expected warning naming Java + 0 symbols + listing Go as available; got warnings=%v", warnings)
	}
}

// Control: language= matching the project's actual language must
// NOT trip the warning.
func TestHandleDeadCode_KnownLanguageInProject_NoWarning(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "p-deadlang-known"
	store.UpsertProject(db.Project{
		ID: pid, Path: t.TempDir(), Name: pid, IndexedAt: time.Now(),
	})
	srv.sessionID = pid

	mustUpsertSymbols(t, store, []db.Symbol{
		{ID: pid + "::pkg.Foo#Function", ProjectID: pid, FilePath: "a.go",
			Name: "Foo", QualifiedName: "pkg.Foo", Kind: "Function", Language: "Go",
			ExtractionConfidence: 1.0},
	})

	result, err := srv.handleDeadCode(context.Background(), makeReq(map[string]any{
		"language": "Go",
	}))
	if err != nil {
		t.Fatalf("handleDeadCode: %v", err)
	}
	body := decode(t, result)
	meta, _ := body["_meta"].(map[string]any)
	if meta == nil {
		return
	}
	warnings, _ := meta["warnings"].([]any)
	for _, w := range warnings {
		if s, _ := w.(string); strings.Contains(s, "0 symbols in this project") {
			t.Errorf("Go matched symbols — must not warn; got %q", s)
		}
	}
}

// Control: omitting language= (default: all languages) must not warn.
func TestHandleDeadCode_NoLanguageFilter_NoWarning(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "p-deadlang-none"
	store.UpsertProject(db.Project{
		ID: pid, Path: t.TempDir(), Name: pid, IndexedAt: time.Now(),
	})
	srv.sessionID = pid

	mustUpsertSymbols(t, store, []db.Symbol{
		{ID: pid + "::pkg.Foo#Function", ProjectID: pid, FilePath: "a.go",
			Name: "Foo", QualifiedName: "pkg.Foo", Kind: "Function", Language: "Go",
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
	warnings, _ := meta["warnings"].([]any)
	for _, w := range warnings {
		if s, _ := w.(string); strings.Contains(s, "0 symbols in this project") {
			t.Errorf("no language filter — must not warn; got %q", s)
		}
	}
}
