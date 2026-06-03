// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #980: context's imports and callees source bytes are read via the
// same byte-offset path as the seed. Pre-fix, only the seed's file
// was hash-checked — editing only a callee file (no edit to the seed
// file) returned stale callee source silently. Now every unique
// dependency path is hash-checked and emits a _meta.warnings entry
// per stale file.

func TestHandleContext_StaleCalleeFile_EmitsWarning(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	root := t.TempDir()
	srv.sessionRoot = root
	srv.sessionID = "pcdep"
	store.UpsertProject(db.Project{
		ID: "pcdep", Path: root, Name: "pcdep", IndexedAt: time.Now(),
	})

	// Seed in main.go; callee in helper.go. Same project, different files.
	seedSrc := "func Foo() {\n\tBar()\n}\n"
	seedHash := writeAndHash(t, filepath.Join(root, "main.go"), seedSrc)
	helperSrc := "func Bar() {\n\treturn\n}\n"
	helperHash := writeAndHash(t, filepath.Join(root, "helper.go"), helperSrc)

	store.BulkUpsertSymbols([]db.Symbol{
		{
			ID: "pcdep::main.Foo#Function", ProjectID: "pcdep", FilePath: "main.go",
			Name: "Foo", QualifiedName: "main.Foo", Kind: "Function", Language: "Go",
			StartByte: 0, EndByte: 22, StartLine: 1, EndLine: 3,
			FileHash: seedHash, ExtractionConfidence: 1.0, IsExported: true,
		},
		{
			ID: "pcdep::main.Bar#Function", ProjectID: "pcdep", FilePath: "helper.go",
			Name: "Bar", QualifiedName: "main.Bar", Kind: "Function", Language: "Go",
			StartByte: 0, EndByte: 22, StartLine: 1, EndLine: 3,
			FileHash: helperHash, ExtractionConfidence: 1.0, IsExported: true,
		},
	})
	store.SetFileHash("pcdep", "main.go", seedHash)
	store.SetFileHash("pcdep", "helper.go", helperHash)
	// Foo CALLS Bar — wire the edge so context follows it.
	store.BulkUpsertEdges([]db.Edge{{
		FromID: "pcdep::main.Foo#Function", ToID: "pcdep::main.Bar#Function",
		Kind: "CALLS", ProjectID: "pcdep",
	}})

	// Edit ONLY the callee file (helper.go). The seed (main.go) stays
	// at its indexed bytes — pre-fix this case shipped stale callee
	// source with no warning.
	_ = os.WriteFile(filepath.Join(root, "helper.go"),
		[]byte("// header line added\nfunc Bar() {\n\treturn\n}\n"), 0o600)

	result, err := srv.handleContext(context.Background(), makeReq(map[string]any{
		"id":      "pcdep::main.Foo#Function",
		"project": "pcdep",
	}))
	if err != nil {
		t.Fatalf("handleContext: %v", err)
	}
	body := decode(t, result)
	meta, _ := body["_meta"].(map[string]any)
	warnings, _ := meta["warnings"].([]any)
	foundDep := false
	for _, w := range warnings {
		s := fmt.Sprint(w)
		if strings.Contains(s, "helper.go") && strings.Contains(s, "modified since last index") {
			foundDep = true
			break
		}
	}
	if !foundDep {
		t.Errorf("expected dependency-staleness warning naming helper.go; got warnings=%v", warnings)
	}
	// And a force=true re-index next_step must be present.
	steps, _ := meta["next_steps"].([]any)
	hasReindex := false
	for _, s := range steps {
		if step, ok := s.(map[string]any); ok && step["tool"] == "index" {
			if args, _ := step["args"].(string); strings.Contains(args, `"force":true`) {
				hasReindex = true
				break
			}
		}
	}
	if !hasReindex {
		t.Errorf("expected force=true re-index next_step; got %v", steps)
	}
}

// Clean baseline — same shape, no edits: no warnings.
func TestHandleContext_CleanDeps_NoStaleWarning(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	root := t.TempDir()
	srv.sessionRoot = root
	srv.sessionID = "pcdepc"
	store.UpsertProject(db.Project{
		ID: "pcdepc", Path: root, Name: "pcdepc", IndexedAt: time.Now(),
	})

	seedSrc := "func Foo() {\n\tBar()\n}\n"
	seedHash := writeAndHash(t, filepath.Join(root, "main.go"), seedSrc)
	helperSrc := "func Bar() {\n\treturn\n}\n"
	helperHash := writeAndHash(t, filepath.Join(root, "helper.go"), helperSrc)
	store.BulkUpsertSymbols([]db.Symbol{
		{
			ID: "pcdepc::main.Foo#Function", ProjectID: "pcdepc", FilePath: "main.go",
			Name: "Foo", QualifiedName: "main.Foo", Kind: "Function", Language: "Go",
			StartByte: 0, EndByte: 22, StartLine: 1, EndLine: 3,
			FileHash: seedHash, ExtractionConfidence: 1.0, IsExported: true,
		},
		{
			ID: "pcdepc::main.Bar#Function", ProjectID: "pcdepc", FilePath: "helper.go",
			Name: "Bar", QualifiedName: "main.Bar", Kind: "Function", Language: "Go",
			StartByte: 0, EndByte: 22, StartLine: 1, EndLine: 3,
			FileHash: helperHash, ExtractionConfidence: 1.0, IsExported: true,
		},
	})
	store.SetFileHash("pcdepc", "main.go", seedHash)
	store.SetFileHash("pcdepc", "helper.go", helperHash)
	store.BulkUpsertEdges([]db.Edge{{
		FromID: "pcdepc::main.Foo#Function", ToID: "pcdepc::main.Bar#Function",
		Kind: "CALLS", ProjectID: "pcdepc",
	}})

	result, err := srv.handleContext(context.Background(), makeReq(map[string]any{
		"id":      "pcdepc::main.Foo#Function",
		"project": "pcdepc",
	}))
	if err != nil {
		t.Fatalf("handleContext: %v", err)
	}
	body := decode(t, result)
	meta, _ := body["_meta"].(map[string]any)
	warnings, _ := meta["warnings"].([]any)
	for _, w := range warnings {
		s := fmt.Sprint(w)
		if strings.Contains(s, "modified since last index") {
			t.Errorf("clean tree must not emit staleness warnings; got %v", s)
		}
	}
}
