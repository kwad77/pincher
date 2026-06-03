// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #941: search project="*" returned every result with an empty snippet.
// Pre-fix the snippet path resolved the project root ONCE from the
// session/explicit project — but project="*" leaves projectID="", so
// root="" and the disk read short-circuited for every result. Cross-
// project agents lost the BM25-snippet discriminator and had to round-
// trip a `symbol` call per result. Fix resolves root per-symbol via
// r.Symbol.ProjectID with a small cache.

func TestHandleSearch_CrossProject_PopulatesSnippet(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)

	// Two separate projects, each with a file on disk whose byte range
	// the seeded symbol points at. The snippet path reads via
	// index.ReadSymbolSourceCapped, so the bytes must exist on disk
	// relative to the project's Path.
	pid1 := t.TempDir()
	pid2 := t.TempDir()

	body1 := "func handleX_v1() {\n\treturn 1\n}\n"
	body2 := "func handleX_v2() {\n\treturn 2\n}\n"
	if err := os.WriteFile(filepath.Join(pid1, "a.go"), []byte(body1), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pid2, "b.go"), []byte(body2), 0o644); err != nil {
		t.Fatal(err)
	}

	store.UpsertProject(db.Project{ID: pid1, Path: pid1, Name: "p1", IndexedAt: time.Now()})
	store.UpsertProject(db.Project{ID: pid2, Path: pid2, Name: "p2", IndexedAt: time.Now()})
	store.BulkUpsertSymbols([]db.Symbol{
		{ID: "p1::handleX", ProjectID: pid1, FilePath: "a.go", Name: "handleX_v1",
			QualifiedName: "pkg.handleX_v1", Kind: "Function", Language: "Go",
			StartByte: 0, EndByte: len(body1), StartLine: 1, EndLine: 3,
			ExtractionConfidence: 1.0},
		{ID: "p2::handleX", ProjectID: pid2, FilePath: "b.go", Name: "handleX_v2",
			QualifiedName: "pkg.handleX_v2", Kind: "Function", Language: "Go",
			StartByte: 0, EndByte: len(body2), StartLine: 1, EndLine: 3,
			ExtractionConfidence: 1.0},
	})

	result, err := srv.handleSearch(context.Background(), makeReq(map[string]any{
		"query":   "handleX*",
		"project": "*",
	}))
	if err != nil {
		t.Fatalf("handleSearch: %v", err)
	}
	body := decode(t, result)
	results, _ := body["results"].([]any)
	if len(results) < 2 {
		t.Fatalf("expected >= 2 cross-project results; got %d (body=%v)", len(results), body)
	}
	for _, raw := range results {
		row, _ := raw.(map[string]any)
		snippet, _ := row["snippet"].(string)
		if snippet == "" {
			t.Errorf("cross-project result %v has empty snippet (pre-fix shape)", row["id"])
		}
	}
}
