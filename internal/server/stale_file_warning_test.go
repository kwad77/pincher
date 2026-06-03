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

	"github.com/zeebo/xxh3"

	"github.com/kwad77/pincher/internal/db"
)

// #317: when a file is edited after indexing, the stored byte
// offsets point at content that no longer matches the symbol.
// handleSymbol must warn the agent so they don't act on wrong
// source.

func writeAndHash(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", xxh3.Hash([]byte(content)))
}

// Stale file → warning + re-index next_step. Verifies the whole
// retrieve path: seed symbol, modify file on disk after recording
// hash, call symbol, assert the warning surfaces.
func TestHandleSymbol_StaleFileEmitsWarning(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	root := t.TempDir()
	srv.sessionRoot = root
	srv.sessionID = "p1"
	store.UpsertProject(db.Project{
		ID: "p1", Path: root, Name: "p1", IndexedAt: time.Now(),
	})

	// Index-time content + hash; symbol points at bytes 0..50 of it.
	indexed := "func A() {\n\treturn 1\n}\nfunc B() { return 2 }\n"
	hash := writeAndHash(t, filepath.Join(root, "main.go"), indexed)

	store.BulkUpsertSymbols([]db.Symbol{{
		ID: "p1::main.A#Function", ProjectID: "p1", FilePath: "main.go",
		Name: "A", QualifiedName: "main.A", Kind: "Function", Language: "Go",
		StartByte: 0, EndByte: 22, StartLine: 1, EndLine: 3,
		FileHash: hash, ExtractionConfidence: 1.0,
	}})
	store.SetFileHash("p1", "main.go", hash)

	// Modify the file AFTER indexing — bytes 0..22 no longer hold A.
	_ = os.WriteFile(filepath.Join(root, "main.go"),
		[]byte("// header line added\nfunc A() {\n\treturn 1\n}\n"), 0o600)

	result, err := srv.handleSymbol(context.Background(), makeReq(map[string]any{
		"id":      "p1::main.A#Function",
		"project": "p1",
	}))
	if err != nil {
		t.Fatalf("handleSymbol: %v", err)
	}
	body := decode(t, result)
	meta, _ := body["_meta"].(map[string]any)
	warnings, _ := meta["warnings"].([]any)
	if len(warnings) == 0 {
		t.Fatalf("expected staleness warning; got meta: %v", meta)
	}
	if !strings.Contains(fmt.Sprint(warnings[0]), "modified since last index") {
		t.Errorf("warning %v should mention 'modified since last index'", warnings[0])
	}
	steps, _ := meta["next_steps"].([]any)
	hasReindex := false
	for _, s := range steps {
		if step, ok := s.(map[string]any); ok && step["tool"] == "index" {
			args, _ := step["args"].(string)
			if strings.Contains(args, `"force":true`) {
				hasReindex = true
			}
		}
	}
	if !hasReindex {
		t.Errorf("expected a force-reindex next_step; got: %v", steps)
	}
}

// Matching file → no warning. The happy path must not noise up
// every call with a false-positive warning.
func TestHandleSymbol_MatchingFileNoWarning(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	root := t.TempDir()
	srv.sessionRoot = root
	srv.sessionID = "p1"
	store.UpsertProject(db.Project{
		ID: "p1", Path: root, Name: "p1", IndexedAt: time.Now(),
	})

	content := "func A() { return 1 }\n"
	hash := writeAndHash(t, filepath.Join(root, "main.go"), content)

	store.BulkUpsertSymbols([]db.Symbol{{
		ID: "p1::main.A#Function", ProjectID: "p1", FilePath: "main.go",
		Name: "A", QualifiedName: "main.A", Kind: "Function", Language: "Go",
		StartByte: 0, EndByte: 22, StartLine: 1, EndLine: 1,
		FileHash: hash, ExtractionConfidence: 1.0,
	}})
	store.SetFileHash("p1", "main.go", hash)

	result, err := srv.handleSymbol(context.Background(), makeReq(map[string]any{
		"id": "p1::main.A#Function", "project": "p1",
	}))
	if err != nil {
		t.Fatalf("handleSymbol: %v", err)
	}
	body := decode(t, result)
	meta, _ := body["_meta"].(map[string]any)
	if warnings, ok := meta["warnings"]; ok {
		t.Errorf("matching file should not emit warnings; got %v", warnings)
	}
}

// fields=signature (no source) skips the staleness check. The check
// is only meaningful when the response includes byte-offset-driven
// content — projection that excludes source uses the in-DB metadata.
func TestHandleSymbol_FieldsExcludeSource_SkipsStalenessCheck(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	root := t.TempDir()
	srv.sessionRoot = root
	srv.sessionID = "p1"
	store.UpsertProject(db.Project{
		ID: "p1", Path: root, Name: "p1", IndexedAt: time.Now(),
	})

	// Stored hash deliberately mismatches reality (no file on disk).
	store.BulkUpsertSymbols([]db.Symbol{{
		ID: "p1::main.A#Function", ProjectID: "p1", FilePath: "main.go",
		Name: "A", QualifiedName: "main.A", Kind: "Function", Language: "Go",
		StartByte: 0, EndByte: 22, StartLine: 1, EndLine: 1,
		FileHash: "deadbeef", ExtractionConfidence: 1.0,
	}})
	store.SetFileHash("p1", "main.go", "deadbeef")

	result, err := srv.handleSymbol(context.Background(), makeReq(map[string]any{
		"id":      "p1::main.A#Function",
		"project": "p1",
		"fields":  "id,signature",
	}))
	if err != nil {
		t.Fatalf("handleSymbol: %v", err)
	}
	body := decode(t, result)
	meta, _ := body["_meta"].(map[string]any)
	if warnings, ok := meta["warnings"]; ok {
		t.Errorf("fields=id,signature shouldn't trigger staleness check; got %v", warnings)
	}
}

// fileHashOnDisk pure-helper coverage.
func TestFileHashOnDisk(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	wantHash := writeAndHash(t, p, "hello world")
	got, ok := fileHashOnDisk(p)
	if !ok {
		t.Fatal("expected ok=true for existing file")
	}
	if got != wantHash {
		t.Errorf("hash = %q, want %q", got, wantHash)
	}
	if _, ok := fileHashOnDisk(filepath.Join(dir, "missing")); ok {
		t.Error("expected ok=false for missing file")
	}
}
