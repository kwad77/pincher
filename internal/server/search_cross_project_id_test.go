// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #1047: cross-project search (project="*") returned a flat result
// list with no project_id field, so an agent had no way to tell
// which project each result came from short of pattern-matching
// file_path. On monorepo mounts with mirrored source trees (e.g.
// pincher-repo + sniffer mirrors that both contain
// `internal/server/server.go`) file_path is itself ambiguous.
// project_id is now emitted on cross-project results; single-
// project queries omit it to avoid bloating every row.

func TestHandleSearch_CrossProjectIncludesProjectID(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pidA := "p-search-a"
	pidB := "p-search-b"
	store.UpsertProject(db.Project{
		ID: pidA, Path: t.TempDir(), Name: pidA, IndexedAt: time.Now(),
	})
	store.UpsertProject(db.Project{
		ID: pidB, Path: t.TempDir(), Name: pidB, IndexedAt: time.Now(),
	})

	mustUpsertSymbols(t, store, []db.Symbol{
		{ID: pidA + "::pkg.Foo#Function", ProjectID: pidA, FilePath: "a.go",
			Name: "Foo", QualifiedName: "pkg.Foo", Kind: "Function", Language: "Go",
			ExtractionConfidence: 1.0},
		{ID: pidB + "::pkg.Foo#Function", ProjectID: pidB, FilePath: "a.go",
			Name: "Foo", QualifiedName: "pkg.Foo", Kind: "Function", Language: "Go",
			ExtractionConfidence: 1.0},
	})

	result, err := srv.handleSearch(context.Background(), makeReq(map[string]any{
		"query":   "Foo",
		"project": "*",
	}))
	if err != nil {
		t.Fatalf("handleSearch: %v", err)
	}
	body := decode(t, result)
	results, _ := body["results"].([]any)
	if len(results) < 2 {
		t.Fatalf("expected at least 2 cross-project results; got %d", len(results))
	}
	sawA, sawB := false, false
	for _, r := range results {
		m, _ := r.(map[string]any)
		pid, _ := m["project_id"].(string)
		switch pid {
		case pidA:
			sawA = true
		case pidB:
			sawB = true
		case "":
			t.Errorf("cross-project result missing project_id: %v", m)
		}
	}
	if !sawA || !sawB {
		t.Errorf("expected results from both pidA and pidB; sawA=%v sawB=%v", sawA, sawB)
	}
}

// Control: single-project search (no project arg or explicit project)
// must NOT include project_id in each row — that info is already
// implicit in the scope and would bloat every response.
func TestHandleSearch_SingleProjectOmitsProjectID(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "p-search-single"
	store.UpsertProject(db.Project{
		ID: pid, Path: t.TempDir(), Name: pid, IndexedAt: time.Now(),
	})
	srv.sessionID = pid

	mustUpsertSymbols(t, store, []db.Symbol{
		{ID: pid + "::pkg.Bar#Function", ProjectID: pid, FilePath: "a.go",
			Name: "Bar", QualifiedName: "pkg.Bar", Kind: "Function", Language: "Go",
			ExtractionConfidence: 1.0},
	})

	result, err := srv.handleSearch(context.Background(), makeReq(map[string]any{
		"query": "Bar",
	}))
	if err != nil {
		t.Fatalf("handleSearch: %v", err)
	}
	body := decode(t, result)
	results, _ := body["results"].([]any)
	if len(results) == 0 {
		t.Fatal("expected at least 1 result")
	}
	for _, r := range results {
		m, _ := r.(map[string]any)
		if _, ok := m["project_id"]; ok {
			t.Errorf("single-project search row must omit project_id; got %v", m)
		}
	}
}

// Control: fields=project_id on a single-project search must NOT
// trip the unknown-keys warning (project_id is a valid projectable
// key, just unpopulated in single-project mode).
func TestHandleSearch_FieldsProjectIDDoesNotWarn(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "p-search-fields"
	store.UpsertProject(db.Project{
		ID: pid, Path: t.TempDir(), Name: pid, IndexedAt: time.Now(),
	})
	srv.sessionID = pid

	mustUpsertSymbols(t, store, []db.Symbol{
		{ID: pid + "::pkg.Baz#Function", ProjectID: pid, FilePath: "a.go",
			Name: "Baz", QualifiedName: "pkg.Baz", Kind: "Function", Language: "Go",
			ExtractionConfidence: 1.0},
	})

	result, err := srv.handleSearch(context.Background(), makeReq(map[string]any{
		"query":  "Baz",
		"fields": "id,project_id",
	}))
	if err != nil {
		t.Fatalf("handleSearch: %v", err)
	}
	body := decode(t, result)
	meta, _ := body["_meta"].(map[string]any)
	if meta == nil {
		return
	}
	warnings, _ := meta["warnings"].([]any)
	for _, w := range warnings {
		if s, _ := w.(string); strings.Contains(s, "project_id") &&
			strings.Contains(s, "matched no keys") {
			t.Errorf("project_id must be a valid field; got warning %q", s)
		}
	}
}
