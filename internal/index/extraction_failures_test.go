// SPDX-License-Identifier: MIT

package index

import (
	"context"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// extraction_failures end-to-end tests (#42 part 1).
//
// The unit tests in internal/db/ verify the table + RecordExtractionFailure
// + ListExtractionFailures semantics. These tests exercise the full
// indexer → heuristic → DB pipeline. If a regression silently turns off
// the heuristics (e.g. someone removes the recordExtractionHeuristics call),
// these fail loud.

// TestExtractionFailures_NoFalsePositivesOnHealthyCode is the negative gate.
// Indexing a normal, well-formed Go file MUST produce ZERO failure rows.
// If this fails, our heuristics are over-eager and would clutter the
// failure log on every healthy project.
func TestExtractionFailures_NoFalsePositivesOnHealthyCode(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()

	writeFile(t, dir, "main.go", `package demo

// Greet returns a salutation.
func Greet(name string) string {
	return "hello, " + name
}

// Process is a worker function with a method.
type Worker struct {
	id int
}

func (w *Worker) Process() error {
	return nil
}
`)

	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("Index: %v", err)
	}

	pid := db.ProjectIDFromPath(dir)
	rows, err := store.ListExtractionFailures(pid, 0)
	if err != nil {
		t.Fatalf("ListExtractionFailures: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("healthy code produced %d failure rows; sanity heuristics over-eager:", len(rows))
		for _, r := range rows {
			t.Logf("  %s/%s: %s — %s", r.FilePath, r.Language, r.Reason, r.Details)
		}
	}
}

// TestExtractionFailures_ReindexDoesNotMultiply pins the UNIQUE-conflict
// upsert path at the indexer layer. Re-indexing a file that produces a
// failure (or that has no failures at all) MUST NOT grow the
// extraction_failures table on every Watch tick.
func TestExtractionFailures_ReindexDoesNotMultiply(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "package demo\nfunc Hello() {}\n")

	pid := db.ProjectIDFromPath(dir)

	// Index three times, force=true so every iteration re-extracts.
	for i := 0; i < 3; i++ {
		if _, err := idx.Index(context.Background(), dir, true); err != nil {
			t.Fatalf("Index iteration %d: %v", i, err)
		}
	}
	rows, err := store.ListExtractionFailures(pid, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("clean re-index produced %d failure rows after 3 iterations; want 0", len(rows))
	}
}
