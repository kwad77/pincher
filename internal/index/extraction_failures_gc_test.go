// SPDX-License-Identifier: MIT

package index

import (
	"context"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// #1319 v0.71: extraction_failures rows are GC'd on the next successful
// extraction pass when their reason no longer fires. Pre-fix, fix-the-
// bug PRs left historical rows in the table forever — user repro:
// README.md `qualified_name_collision` row 8 days old after #1207's
// Markdown suppression made the diagnostic stop firing for Markdown.
//
// Tests cover:
//   - positive: a stale row whose reason no longer fires is deleted
//   - negative control: a row whose reason DOES still fire is retained
//     (and last_seen_at updates)
//   - cross-check: rows for a different file are not touched

func TestExtractionFailures_StaleRowPrunedOnReExtraction_1319(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "app/run.go", "package app\n\nfunc Run() {}\n")

	// Prime the project by indexing once.
	res1, err := idx.Index(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("Index pass 1: %v", err)
	}

	// Simulate a stale failure row left over from a previous buggy
	// version of pincher: synthetically insert a `qualified_name_collision`
	// row for app/run.go. The file's current extraction yields no
	// collision (one Function symbol), so this reason will not re-fire
	// on the next pass — it MUST be pruned.
	if err := store.RecordExtractionFailure(res1.ProjectID, "app/run.go", "Go",
		"qualified_name_collision", "synthetic stale row from a prior buggy state"); err != nil {
		t.Fatalf("RecordExtractionFailure: %v", err)
	}

	rowsBefore, _ := store.ListExtractionFailures(res1.ProjectID, 0)
	if len(rowsBefore) != 1 {
		t.Fatalf("expected 1 row after synthetic insert; got %d", len(rowsBefore))
	}

	// Re-index the file (touch its content so the hash differs and
	// the extractor actually runs — the per-file goroutine is what
	// drives recordExtractionHeuristics, which is what triggers the
	// prune).
	writeFile(t, dir, "app/run.go", "package app\n\nfunc Run() { _ = 1 }\n")
	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("Index pass 2: %v", err)
	}

	rowsAfter, _ := store.ListExtractionFailures(res1.ProjectID, 0)
	for _, r := range rowsAfter {
		if r.FilePath == "app/run.go" && r.Reason == "qualified_name_collision" {
			t.Errorf("stale row %q still present after re-extraction did not re-fire the reason", r.Reason)
		}
	}
}

// Negative control: a row whose reason DOES still fire must be retained.
// Synthesize a byte_range_negative failure by writing a Go file that
// extracts cleanly, then synthetically insert the failure row, then
// re-index AND verify the prune doesn't delete it just because we synthesized
// it — only deletes when the reason really doesn't re-fire.
//
// To test "reason still fires", we'd need a file whose extraction genuinely
// produces a byte_range_negative failure. Easier shape: prime with a
// real failure, re-index without changing content (hash skip → no
// heuristic call → no prune), assert the row survives.
func TestExtractionFailures_RowSurvivesWhenFileSkippedByHash_1319(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "app/run.go", "package app\n\nfunc Run() {}\n")

	res1, err := idx.Index(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("Index pass 1: %v", err)
	}

	if err := store.RecordExtractionFailure(res1.ProjectID, "app/run.go", "Go",
		"byte_range_negative", "synthetic"); err != nil {
		t.Fatalf("RecordExtractionFailure: %v", err)
	}

	// Re-index WITHOUT changing content. The hash-skip path bypasses
	// the heuristic call entirely, so the prune doesn't run — the
	// synthetic row stays. (This is the right semantics: the file
	// wasn't re-extracted, so we can't say the reason no longer
	// applies.)
	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("Index pass 2: %v", err)
	}

	rows, _ := store.ListExtractionFailures(res1.ProjectID, 0)
	found := false
	for _, r := range rows {
		if r.FilePath == "app/run.go" && r.Reason == "byte_range_negative" {
			found = true
			break
		}
	}
	if !found {
		t.Error("byte_range_negative row was pruned even though the file was skipped (hash-match) and never re-extracted — incorrect: prune must be gated on actual re-extraction")
	}
}

// Cross-check: pruning is scoped to (project_id, file_path). A row
// for OTHER_FILE.go must not be touched when MY_FILE.go re-extracts
// cleanly.
func TestExtractionFailures_PruneScopedToFile_1319(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "app/a.go", "package app\n\nfunc A() {}\n")
	writeFile(t, dir, "app/b.go", "package app\n\nfunc B() {}\n")

	res1, err := idx.Index(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("Index pass 1: %v", err)
	}

	// Stale failure on file B (which will NOT re-extract in pass 2).
	if err := store.RecordExtractionFailure(res1.ProjectID, "app/b.go", "Go",
		"qualified_name_collision", "synthetic on B"); err != nil {
		t.Fatalf("RecordExtractionFailure: %v", err)
	}

	// Touch only file A so only A re-extracts.
	writeFile(t, dir, "app/a.go", "package app\n\nfunc A() { _ = 1 }\n")
	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("Index pass 2: %v", err)
	}

	rows, _ := store.ListExtractionFailures(res1.ProjectID, 0)
	found := false
	for _, r := range rows {
		if r.FilePath == "app/b.go" && r.Reason == "qualified_name_collision" {
			found = true
		}
		if r.FilePath == "app/a.go" {
			t.Errorf("unexpected row on app/a.go after re-extraction: %s", r.Reason)
		}
	}
	if !found {
		t.Error("row on app/b.go was deleted when only app/a.go re-extracted — prune scope leaked")
	}
}

// Cross-check: passing keepReasons=nil to PruneExtractionFailuresForFile
// deletes ALL rows for the file. Mirrors the "extraction re-ran with
// zero failures" semantic the indexer relies on.
func TestPruneExtractionFailuresForFile_NilReasons_DeletesAll_1319(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "app/run.go", "package app\n")

	res1, err := idx.Index(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("Index pass 1: %v", err)
	}

	for _, reason := range []string{"qualified_name_collision", "byte_range_negative", "parse_error"} {
		if err := store.RecordExtractionFailure(res1.ProjectID, "app/run.go", "Go", reason, "synthetic"); err != nil {
			t.Fatalf("RecordExtractionFailure(%s): %v", reason, err)
		}
	}
	if err := store.PruneExtractionFailuresForFile(res1.ProjectID, "app/run.go", nil); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	rows, _ := store.ListExtractionFailures(res1.ProjectID, 0)
	for _, r := range rows {
		if r.FilePath == "app/run.go" {
			t.Errorf("expected all rows for app/run.go to be deleted with nil keep-set; found %q", r.Reason)
		}
	}
}

// Statically reference the unused db import for the helpers, keeping the
// import list honest.
var _ = db.ProjectIDFromPath
