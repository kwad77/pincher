// SPDX-License-Identifier: MIT

package index

import (
	"context"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// #1313: pre-fix, files whose extraction yielded 0 symbols (empty
// Markdown with no headings, YAML/JSON with no extractable settings,
// `package X`-only Go fixtures, etc.) never got a `files` row written
// — SetFileHash was only invoked inside flushBuffers' symbol-iteration
// loop. The next watcher tick treated the file as unseen and re-walked
// + re-extracted it forever. User repro: 16 ghost files re-extracted
// every incremental pass on pincherMCP-on-Mac, billed to `reprocessed`
// instead of `skipped`.
//
// The fix stamps the file hash inline in the per-file goroutine:
//
//   • Unknown-language early-return path
//   • Zero-symbol early-return path
//   • Successful-extraction path (BEFORE symbols join the batched flush,
//     so batch-flush failure can't drop the hash row)
//
// This regression test pins all three paths.

func TestIndex_ZeroSymbolFileGetsHashRow_1313(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()

	// Control: a normal Go file that yields ≥1 symbol.
	writeFile(t, dir, "app/run.go", "package app\n\nfunc Run() {}\n")

	// Zero-symbol case 1: empty Markdown — no headings, no extractable
	// sections. Markdown's AST extractor returns 0 symbols.
	writeFile(t, dir, "docs/empty.md", "\n")

	// Zero-symbol case 2: Markdown with only a paragraph — also 0
	// Section symbols (Sections require headings).
	writeFile(t, dir, "docs/prose.md", "just a paragraph, no heading.\n")

	// First pass: every file walks the extractor for the first time;
	// nothing is skipped because no prior hash rows exist.
	res1, err := idx.Index(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("Index pass 1: %v", err)
	}
	if res1.Skipped != 0 {
		t.Errorf("pass 1: nothing was indexed yet, expected Skipped=0, got %d", res1.Skipped)
	}
	if res1.Files != 3 {
		t.Fatalf("pass 1: expected Files=3 (1 Go + 2 Markdown), got %d", res1.Files)
	}

	// Hash rows MUST exist for the zero-symbol files post-pass-1.
	// Pre-fix these returned "" because SetFileHash was only invoked
	// inside flushBuffers' symbol-iteration loop, which never iterated
	// for files that yielded 0 symbols.
	zeroSymbolFiles := []string{"docs/empty.md", "docs/prose.md"}
	for _, fp := range zeroSymbolFiles {
		if got := store.GetFileHash(res1.ProjectID, fp); got == "" {
			t.Errorf("pass 1: expected hash row for zero-symbol file %q, got empty", fp)
		}
	}

	// Second pass with no source changes: every file's content hash
	// matches, so every file should land in Skipped — including the
	// zero-symbol ones. Pre-fix, the zero-symbol files re-walked here.
	res2, err := idx.Index(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("Index pass 2: %v", err)
	}
	if res2.Skipped != 3 {
		t.Errorf("pass 2: expected Skipped=3 (all files hash-match), got %d", res2.Skipped)
	}

	// Control: the normal Go file's symbols are still present —
	// verifying we didn't regress the happy path by removing the
	// flushBuffers SetFileHash loop.
	runID := db.MakeSymbolID("app/run.go", "app.Run", "Function")
	if got, err := store.GetSymbol(runID); err != nil || got == nil {
		t.Errorf("control: expected app.Run to still be indexed; err=%v sym=%v", err, got)
	}
}

// Negative control: a file whose CONTENT changes between passes MUST
// still be re-extracted — proving the hash-stamp doesn't poison the
// staleness check.
func TestIndex_ZeroSymbolFileReExtractsOnContentChange_1313(t *testing.T) {
	idx, _ := newTestIndexer(t)
	dir := t.TempDir()

	writeFile(t, dir, "app/run.go", "package app\n\nfunc Run() {}\n")
	mdPath := writeFile(t, dir, "docs/prose.md", "just a paragraph.\n")

	res1, err := idx.Index(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("Index pass 1: %v", err)
	}

	// Mutate the zero-symbol file's content. Still zero symbols (no
	// heading) but a different hash. The next pass MUST re-walk.
	writeFile(t, dir, "docs/prose.md", "a different paragraph.\n")
	_ = mdPath

	res2, err := idx.Index(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("Index pass 2: %v", err)
	}

	// Only the changed prose.md should re-extract; the Go file should
	// skip. Total Skipped should equal Files - 1.
	if res2.Skipped != res1.Files-1 {
		t.Errorf("pass 2: expected Skipped=%d (all but the mutated file), got %d", res1.Files-1, res2.Skipped)
	}
}
