// SPDX-License-Identifier: MIT

package db

import (
	"testing"
	"time"
)

// TestPruneStaleExtractionFailures_DeletesPreIndexedAtRows pins the
// canonical case: a project's failure recorded BEFORE the project's
// indexed_at is the awaiting-re-index subset. PruneStaleExtractionFailures
// (#1386) deletes it; rows recorded AFTER indexed_at survive (current).
func TestPruneStaleExtractionFailures_DeletesPreIndexedAtRows(t *testing.T) {
	s := newTestStore(t)

	now := time.Now().Unix()
	past := time.Now().Add(-2 * time.Hour).Unix()

	if err := s.UpsertProject(Project{ID: "p1", Path: "/p1", Name: "p1", IndexedAt: time.Now()}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Stale row — last_seen_at past project's indexed_at.
	if err := s.RecordExtractionFailure("p1", "stale.go", "Go", "parse_error", "old"); err != nil {
		t.Fatalf("seed stale: %v", err)
	}
	if _, err := s.DB().Exec(
		`UPDATE extraction_failures SET last_seen_at = ? WHERE file_path = 'stale.go'`, past); err != nil {
		t.Fatalf("back-date: %v", err)
	}

	// Current row — last_seen_at after project's indexed_at.
	if err := s.RecordExtractionFailure("p1", "fresh.go", "Go", "parse_error", "new"); err != nil {
		t.Fatalf("seed fresh: %v", err)
	}
	if _, err := s.DB().Exec(
		`UPDATE extraction_failures SET last_seen_at = ? WHERE file_path = 'fresh.go'`, now+1); err != nil {
		t.Fatalf("forward-date: %v", err)
	}

	n, err := s.PruneStaleExtractionFailures()
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if n != 1 {
		t.Errorf("Prune returned %d, want 1 (only stale.go should be deleted)", n)
	}

	remaining, err := s.ListExtractionFailures("p1", 100)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(remaining) != 1 {
		t.Fatalf("remaining = %d, want 1", len(remaining))
	}
	if remaining[0].FilePath != "fresh.go" {
		t.Errorf("survived file = %q, want fresh.go", remaining[0].FilePath)
	}
}

// TestPruneStaleExtractionFailures_EmptyTableNoop — empty DB returns
// (0, nil), no error.
func TestPruneStaleExtractionFailures_EmptyTableNoop(t *testing.T) {
	s := newTestStore(t)
	n, err := s.PruneStaleExtractionFailures()
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if n != 0 {
		t.Errorf("Prune on empty table returned %d, want 0", n)
	}
}

// TestPruneStaleExtractionFailures_CrossProjectScoping — a stale row
// in project A and a current row in project B coexist; the prune only
// affects A's stale row, not B's current one.
func TestPruneStaleExtractionFailures_CrossProjectScoping(t *testing.T) {
	s := newTestStore(t)

	// Project A indexed recently.
	if err := s.UpsertProject(Project{ID: "pa", Path: "/pa", Name: "pa", IndexedAt: time.Now()}); err != nil {
		t.Fatalf("upsert A: %v", err)
	}
	// Project B indexed in the past — its failure recorded later is
	// CURRENT relative to its own indexed_at.
	if err := s.UpsertProject(Project{ID: "pb", Path: "/pb", Name: "pb", IndexedAt: time.Now().Add(-3 * time.Hour)}); err != nil {
		t.Fatalf("upsert B: %v", err)
	}

	// A: stale row (last_seen_at 2h ago vs indexed_at now).
	if err := s.RecordExtractionFailure("pa", "a.go", "Go", "parse_error", "a"); err != nil {
		t.Fatalf("seed A: %v", err)
	}
	if _, err := s.DB().Exec(
		`UPDATE extraction_failures SET last_seen_at = ? WHERE project_id='pa' AND file_path='a.go'`,
		time.Now().Add(-2*time.Hour).Unix()); err != nil {
		t.Fatalf("back-date A: %v", err)
	}

	// B: current row (last_seen_at now vs indexed_at 3h ago).
	if err := s.RecordExtractionFailure("pb", "b.go", "Go", "parse_error", "b"); err != nil {
		t.Fatalf("seed B: %v", err)
	}

	n, err := s.PruneStaleExtractionFailures()
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if n != 1 {
		t.Errorf("Prune returned %d, want 1 (A's stale row only)", n)
	}

	// Verify B's row survived.
	bRows, _ := s.ListExtractionFailures("pb", 100)
	if len(bRows) != 1 {
		t.Errorf("project B remaining = %d, want 1 (its current row should survive A's prune)", len(bRows))
	}
}
