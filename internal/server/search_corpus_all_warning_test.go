// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #935: corpus="all" used to be soft-redirected to "code" with only
// a slog.Warn line — invisible to the agent calling the tool. The
// agent thought it was searching every corpus and got code-only
// results with no signal. Now the redirect surfaces in
// _meta.warnings alongside the result so the deprecation is
// observable.

func TestHandleSearch_CorpusAll_SurfacesWarning(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p935"
	store.UpsertProject(db.Project{ID: "p935", Path: "/tmp/p935", Name: "p935", IndexedAt: time.Now()})
	store.BulkUpsertSymbols([]db.Symbol{
		{ID: "s1", ProjectID: "p935", FilePath: "a.go", Name: "ProcessOrder",
			QualifiedName: "pkg.ProcessOrder", Kind: "Function", Language: "Go",
			StartByte: 0, EndByte: 100, StartLine: 1, EndLine: 5,
			ExtractionConfidence: 1.0},
	})

	result, err := srv.handleSearch(context.Background(), makeReq(map[string]any{
		"query":  "ProcessOrder",
		"corpus": "all",
	}))
	if err != nil {
		t.Fatalf("handleSearch: %v", err)
	}
	body := decode(t, result)
	meta, _ := body["_meta"].(map[string]any)
	if meta == nil {
		t.Fatal("_meta missing")
	}
	warnings, _ := meta["warnings"].([]any)
	if len(warnings) == 0 {
		t.Fatal("expected _meta.warnings to surface corpus=all deprecation; got none")
	}
	found := false
	for _, w := range warnings {
		s, _ := w.(string)
		if strings.Contains(s, "corpus=\"all\"") && strings.Contains(s, "removed in v0.5") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("warning should name corpus=\"all\" and the v0.5 removal; got %v", warnings)
	}
}

// Bare default (corpus omitted) should NOT trigger the warning.
func TestHandleSearch_DefaultCorpus_NoDeprecationWarning(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p935b"
	store.UpsertProject(db.Project{ID: "p935b", Path: "/tmp/p935b", Name: "p935b", IndexedAt: time.Now()})

	result, err := srv.handleSearch(context.Background(), makeReq(map[string]any{
		"query": "foo",
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
		s, _ := w.(string)
		if strings.Contains(s, "corpus=\"all\"") {
			t.Errorf("default corpus must not emit deprecation warning; got %q", s)
		}
	}
}
