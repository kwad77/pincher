// SPDX-License-Identifier: MIT

package db

import "testing"

// #1950: RebuildFTSWithProgress reports one callback per completed
// stage — drops, schema recreate, three per-corpus backfills, row
// count — with a stable total, in ascending order, and still returns
// the same row count as the plain RebuildFTS path.
func TestRebuildFTSWithProgress_ReportsStages(t *testing.T) {
	s := newTestStore(t)
	if err := s.UpsertProject(testProject("ftsp")); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	if err := s.BulkUpsertSymbols([]Symbol{
		{ID: "p1", ProjectID: "ftsp", FilePath: "a.go", Name: "Apple",
			QualifiedName: "pkg.Apple", Kind: "Function", Language: "Go"},
		{ID: "p2", ProjectID: "ftsp", FilePath: "doc.md", Name: "Guide",
			QualifiedName: "doc.md#Guide", Kind: "Section", Language: "Markdown"},
	}); err != nil {
		t.Fatalf("BulkUpsertSymbols: %v", err)
	}

	var stages [][2]int64
	rows, err := s.RebuildFTSWithProgress(func(done, total int64) {
		stages = append(stages, [2]int64{done, total})
	})
	if err != nil {
		t.Fatalf("RebuildFTSWithProgress: %v", err)
	}
	if rows != 2 {
		t.Errorf("rows = %d, want 2", rows)
	}

	if len(stages) != RebuildFTSStages {
		t.Fatalf("got %d stage reports, want %d: %v", len(stages), RebuildFTSStages, stages)
	}
	for i, st := range stages {
		if st[0] != int64(i+1) {
			t.Errorf("stage %d reported done=%d, want %d", i, st[0], i+1)
		}
		if st[1] != RebuildFTSStages {
			t.Errorf("stage %d reported total=%d, want %d", i, st[1], RebuildFTSStages)
		}
	}

	// The rebuilt index must still serve searches across corpora.
	hits, err := s.SearchSymbols("ftsp", "Apple", "", "", 10)
	if err != nil {
		t.Fatalf("SearchSymbols post-rebuild: %v", err)
	}
	if len(hits) == 0 {
		t.Error("post-rebuild search found nothing — backfill missing?")
	}
}

// A nil callback must behave exactly like RebuildFTS (no panic, same
// result) — it is the wrapper's own code path.
func TestRebuildFTSWithProgress_NilCallback(t *testing.T) {
	s := newTestStore(t)
	rows, err := s.RebuildFTSWithProgress(nil)
	if err != nil {
		t.Fatalf("RebuildFTSWithProgress(nil): %v", err)
	}
	if rows != 0 {
		t.Errorf("rows = %d, want 0 on empty store", rows)
	}
}
