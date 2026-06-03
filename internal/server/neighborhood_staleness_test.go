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

// `neighborhood include_source=true` reads every sibling's source
// body via byte offsets. If the file was edited after indexing the
// offsets are wrong for every neighbor. Pre-fix the response shipped
// stale bytes with no signal — same silent-confidently-wrong family
// as `context lite=true` (#978), `symbol` (#317), `symbols` (#960).
// One warning per call (every neighbor shares the file) is the
// right shape, not N per-symbol warnings.

func TestHandleNeighborhood_IncludeSourceStale_EmitsWarning(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	root := t.TempDir()
	srv.sessionRoot = root
	srv.sessionID = "pnstale"
	store.UpsertProject(db.Project{
		ID: "pnstale", Path: root, Name: "pnstale", IndexedAt: time.Now(),
	})

	indexed := "func A() {\n\treturn 1\n}\nfunc B() {\n\treturn 2\n}\n"
	hash := writeAndHash(t, filepath.Join(root, "main.go"), indexed)

	store.BulkUpsertSymbols([]db.Symbol{
		{
			ID: "pnstale::main.A#Function", ProjectID: "pnstale", FilePath: "main.go",
			Name: "A", QualifiedName: "main.A", Kind: "Function", Language: "Go",
			StartByte: 0, EndByte: 22, StartLine: 1, EndLine: 3,
			FileHash: hash, ExtractionConfidence: 1.0,
		},
		{
			ID: "pnstale::main.B#Function", ProjectID: "pnstale", FilePath: "main.go",
			Name: "B", QualifiedName: "main.B", Kind: "Function", Language: "Go",
			StartByte: 23, EndByte: 45, StartLine: 4, EndLine: 6,
			FileHash: hash, ExtractionConfidence: 1.0,
		},
	})
	store.SetFileHash("pnstale", "main.go", hash)

	// File edited — offsets no longer match.
	_ = os.WriteFile(filepath.Join(root, "main.go"),
		[]byte("// header line added\nfunc A() {\n\treturn 1\n}\nfunc B() {\n\treturn 2\n}\n"), 0o600)

	result, err := srv.handleNeighborhood(context.Background(), makeReq(map[string]any{
		"id":             "pnstale::main.A#Function",
		"project":        "pnstale",
		"include_source": true,
	}))
	if err != nil {
		t.Fatalf("handleNeighborhood: %v", err)
	}
	body := decode(t, result)
	meta, _ := body["_meta"].(map[string]any)
	warnings, _ := meta["warnings"].([]any)
	foundStale := false
	for _, w := range warnings {
		if strings.Contains(fmt.Sprint(w), "modified since last index") {
			foundStale = true
			break
		}
	}
	if !foundStale {
		t.Errorf("include_source=true on dirty file should emit staleness warning; got warnings=%v", warnings)
	}

	// Also verify the re-index next_step is present so the failure
	// teaches the recovery path.
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

// include_source=false → no warning even if the file is stale. The
// staleness only bites the byte-offset read path; signature-only
// responses stay correct regardless.
func TestHandleNeighborhood_SignaturesOnly_NoStaleWarning(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	root := t.TempDir()
	srv.sessionRoot = root
	srv.sessionID = "pnsig"
	store.UpsertProject(db.Project{
		ID: "pnsig", Path: root, Name: "pnsig", IndexedAt: time.Now(),
	})

	indexed := "func A() {\n\treturn 1\n}\n"
	hash := writeAndHash(t, filepath.Join(root, "main.go"), indexed)
	store.BulkUpsertSymbols([]db.Symbol{{
		ID: "pnsig::main.A#Function", ProjectID: "pnsig", FilePath: "main.go",
		Name: "A", QualifiedName: "main.A", Kind: "Function", Language: "Go",
		StartByte: 0, EndByte: 22, StartLine: 1, EndLine: 3,
		FileHash: hash, ExtractionConfidence: 1.0,
	}})
	store.SetFileHash("pnsig", "main.go", hash)

	// File edited.
	_ = os.WriteFile(filepath.Join(root, "main.go"),
		[]byte("// header\nfunc A() {\n\treturn 1\n}\n"), 0o600)

	result, err := srv.handleNeighborhood(context.Background(), makeReq(map[string]any{
		"id":      "pnsig::main.A#Function",
		"project": "pnsig",
		// include_source omitted → default false
	}))
	if err != nil {
		t.Fatalf("handleNeighborhood: %v", err)
	}
	body := decode(t, result)
	meta, _ := body["_meta"].(map[string]any)
	warnings, _ := meta["warnings"].([]any)
	for _, w := range warnings {
		if strings.Contains(fmt.Sprint(w), "modified since last index") {
			t.Errorf("include_source=false must not emit stale-bytes warning (signatures stay correct); got %v", w)
		}
	}
}
