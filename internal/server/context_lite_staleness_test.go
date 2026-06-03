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

// `context lite=true` is the minimum-envelope shape the PreToolUse
// hook uses to redirect a Read call. Pre-fix, the lite path
// short-circuited before attachStalenessWarning ran — an agent
// redirected from Read after a file edit got stale source bytes
// with no signal the content didn't match the current file. The
// non-lite path already warns; lite must too. Same silent-
// confidently-wrong family as #317/#960.

func TestHandleContext_LiteMode_StaleFileEmitsWarning(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	root := t.TempDir()
	srv.sessionRoot = root
	srv.sessionID = "pclite"
	store.UpsertProject(db.Project{
		ID: "pclite", Path: root, Name: "pclite", IndexedAt: time.Now(),
	})

	indexed := "func A() {\n\treturn 1\n}\nfunc B() { return 2 }\n"
	hash := writeAndHash(t, filepath.Join(root, "main.go"), indexed)

	store.BulkUpsertSymbols([]db.Symbol{{
		ID: "pclite::main.A#Function", ProjectID: "pclite", FilePath: "main.go",
		Name: "A", QualifiedName: "main.A", Kind: "Function", Language: "Go",
		StartByte: 0, EndByte: 22, StartLine: 1, EndLine: 3,
		FileHash: hash, ExtractionConfidence: 1.0,
	}})
	store.SetFileHash("pclite", "main.go", hash)

	// File edited after indexing — bytes 0..22 no longer hold A.
	_ = os.WriteFile(filepath.Join(root, "main.go"),
		[]byte("// header line added\nfunc A() {\n\treturn 1\n}\n"), 0o600)

	result, err := srv.handleContext(context.Background(), makeReq(map[string]any{
		"id":      "pclite::main.A#Function",
		"project": "pclite",
		"lite":    true,
	}))
	if err != nil {
		t.Fatalf("handleContext lite: %v", err)
	}
	body := decode(t, result)
	meta, _ := body["_meta"].(map[string]any)
	warnings, _ := meta["warnings"].([]any)
	if len(warnings) == 0 {
		t.Fatalf("lite mode should still emit staleness warning on dirty file; got meta=%v", meta)
	}
	foundStale := false
	for _, w := range warnings {
		if strings.Contains(fmt.Sprint(w), "modified since last index") {
			foundStale = true
			break
		}
	}
	if !foundStale {
		t.Errorf("warnings should mention 'modified since last index'; got %v", warnings)
	}
}

// Clean file → lite mode must not emit a false positive. Pin the
// non-regression so the warning doesn't start firing on every call.
func TestHandleContext_LiteMode_CleanFileNoWarning(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	root := t.TempDir()
	srv.sessionRoot = root
	srv.sessionID = "pcclean"
	store.UpsertProject(db.Project{
		ID: "pcclean", Path: root, Name: "pcclean", IndexedAt: time.Now(),
	})

	content := "func A() {\n\treturn 1\n}\n"
	hash := writeAndHash(t, filepath.Join(root, "main.go"), content)
	store.BulkUpsertSymbols([]db.Symbol{{
		ID: "pcclean::main.A#Function", ProjectID: "pcclean", FilePath: "main.go",
		Name: "A", QualifiedName: "main.A", Kind: "Function", Language: "Go",
		StartByte: 0, EndByte: 22, StartLine: 1, EndLine: 3,
		FileHash: hash, ExtractionConfidence: 1.0,
	}})
	store.SetFileHash("pcclean", "main.go", hash)

	result, err := srv.handleContext(context.Background(), makeReq(map[string]any{
		"id":      "pcclean::main.A#Function",
		"project": "pcclean",
		"lite":    true,
	}))
	if err != nil {
		t.Fatalf("handleContext lite: %v", err)
	}
	body := decode(t, result)
	meta, _ := body["_meta"].(map[string]any)
	warnings, _ := meta["warnings"].([]any)
	for _, w := range warnings {
		if strings.Contains(fmt.Sprint(w), "modified since last index") {
			t.Errorf("clean file should not emit staleness warning; got %v", w)
		}
	}
}
