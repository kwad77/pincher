// SPDX-License-Identifier: MIT

package index

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
	"github.com/zeebo/xxh3"
)

// #1573: a killed/OOMed index run can leave file hashes stamped even though
// the symbols for that file were deleted or never flushed. The persisted
// project-level running marker makes the next incremental Index() force a
// re-extract instead of trusting those hashes and preserving a half-written DB.
func TestIndex_IncompletePriorRun_ForcesReextractDespiteMatchingHash(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.go")
	initial := []byte("package main\nfunc Old() {}\n")
	if err := os.WriteFile(srcPath, initial, 0o600); err != nil {
		t.Fatal(err)
	}

	idx := New(store)
	idx.SetBinaryVersion("0.96.0-test")
	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("initial Index: %v", err)
	}
	projectID := db.ProjectIDFromPath(dir)

	updated := []byte("package main\nfunc New() {}\n")
	if err := os.WriteFile(srcPath, updated, 0o600); err != nil {
		t.Fatal(err)
	}
	relPath := "main.go"
	updatedHash := fmt.Sprintf("%x", xxh3.Hash(updated))

	// Simulate the crash window: stale symbols were deleted and the new file
	// hash was committed before the pass died. Without the incomplete-run
	// marker, the next incremental pass would hash-skip this file and leave it
	// symbol-empty.
	if err := store.DeleteSymbolsForFile(projectID, relPath); err != nil {
		t.Fatalf("DeleteSymbolsForFile: %v", err)
	}
	if err := store.SetFileHash(projectID, relPath, updatedHash); err != nil {
		t.Fatalf("SetFileHash: %v", err)
	}
	if err := store.MarkProjectIndexStarted(projectID, time.Unix(1234, 0)); err != nil {
		t.Fatalf("MarkProjectIndexStarted: %v", err)
	}

	result, err := idx.Index(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("recovery Index: %v", err)
	}
	if result.Files == 0 {
		t.Fatal("recovery Index hash-skipped the file; want forced re-extract")
	}

	syms, err := store.GetSymbolsForFile(projectID, relPath)
	if err != nil {
		t.Fatalf("GetSymbolsForFile: %v", err)
	}
	foundNew := false
	for _, sym := range syms {
		if sym.Name == "New" {
			foundNew = true
		}
		if sym.Name == "Old" {
			t.Fatalf("old symbol survived recovery reindex: %+v", sym)
		}
	}
	if !foundNew {
		t.Fatalf("New symbol missing after recovery reindex; symbols=%+v", syms)
	}

	incomplete, _, err := store.ProjectIndexIncomplete(projectID)
	if err != nil {
		t.Fatalf("ProjectIndexIncomplete: %v", err)
	}
	if incomplete {
		t.Fatal("successful recovery Index left project marked incomplete")
	}
}
