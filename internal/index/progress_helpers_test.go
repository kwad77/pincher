// SPDX-License-Identifier: MIT

package index

import (
	"testing"
)

// #1164 §1 coverage push: MarkActiveForTest / UnmarkActiveForTest /
// GetProgressDetail are test-only helpers that have real consumers
// in internal/server but were never exercised from inside
// internal/index — so coverage reported 0% on all three despite
// the production-side server tests using them. Per-package coverage
// only counts code touched by tests in the same package, so these
// tests close the bookkeeping gap and pin the helpers' contracts.

// TestMarkActiveForTest_TogglesProgress pins the round-trip:
// Mark sets active=true and a populated progress entry; GetProgress
// reads them back; UnmarkActiveForTest reverts both. Without this
// the helpers can quietly drift (e.g., progress entry not deleted
// on Unmark would leak across server-side tests).
func TestMarkActiveForTest_TogglesProgress(t *testing.T) {
	idx, _ := newTestIndexer(t)
	const pid = "test-project-active-toggle"

	// Baseline: nothing active, no progress entry.
	if done, total, active := idx.GetProgress(pid); done != 0 || total != 0 || active {
		t.Errorf("baseline GetProgress = (%d, %d, %v), want (0, 0, false)", done, total, active)
	}

	idx.MarkActiveForTest(pid, 42, 100)
	done, total, active := idx.GetProgress(pid)
	if !active {
		t.Errorf("after Mark, active=false; want true")
	}
	if done != 42 || total != 100 {
		t.Errorf("after Mark, GetProgress = (%d, %d), want (42, 100)", done, total)
	}

	idx.UnmarkActiveForTest(pid)
	done, total, active = idx.GetProgress(pid)
	if active {
		t.Errorf("after Unmark, active=true; want false")
	}
	if done != 0 || total != 0 {
		t.Errorf("after Unmark, GetProgress = (%d, %d), want (0, 0) — progress entry leaked", done, total)
	}
}

// TestGetProgressDetail_PopulatedAndEmpty pins both branches of
// GetProgressDetail: the active-with-progress path returns the
// StartedAtUnix stamp, the no-progress path returns zeros. Used
// by HTTP /v1/index-progress; a regression that always returns 0
// would silently strip ETA from the dashboard (#535).
func TestGetProgressDetail_PopulatedAndEmpty(t *testing.T) {
	idx, _ := newTestIndexer(t)
	const pid = "test-project-progress-detail"

	// Empty branch.
	done, total, started, active := idx.GetProgressDetail(pid)
	if done != 0 || total != 0 || started != 0 || active {
		t.Errorf("empty GetProgressDetail = (%d, %d, %d, %v), want (0,0,0,false)", done, total, started, active)
	}

	// Populated branch. MarkActiveForTest doesn't stamp StartedAtUnix
	// itself (only the live Index() pass does), so we set it directly
	// on the progress entry to exercise the non-zero return path.
	idx.MarkActiveForTest(pid, 7, 14)
	if v, ok := idx.progress.Load(pid); ok {
		p := v.(*IndexProgress)
		p.StartedAtUnix.Store(1700000000)
	} else {
		t.Fatal("progress entry missing after MarkActiveForTest")
	}

	done, total, started, active = idx.GetProgressDetail(pid)
	if !active || done != 7 || total != 14 || started != 1700000000 {
		t.Errorf("populated GetProgressDetail = (%d, %d, %d, %v), want (7, 14, 1700000000, true)", done, total, started, active)
	}
	idx.UnmarkActiveForTest(pid)
}
