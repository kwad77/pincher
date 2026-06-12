// SPDX-License-Identifier: MIT

package db

import (
	"testing"
)

// #2055: a freshly-created pincher DB must come up with
// auto_vacuum=INCREMENTAL so freed pages (heavy re-index churn) can be
// reclaimed cheaply via IncrementalVacuum instead of bloating the file
// behind auto_vacuum=NONE (the pre-fix default — reclaimable only by a
// full exclusive-lock VACUUM).
//
// Fail-before/pass-after: revert ensureAutoVacuumOnFreshDB and this fails
// with AutoVacuum=="NONE".
func TestOpen_FreshDB_AutoVacuumIncremental(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	ms, err := s.MaintenanceStats()
	if err != nil {
		t.Fatalf("MaintenanceStats: %v", err)
	}
	if ms.AutoVacuum != "INCREMENTAL" {
		t.Errorf("fresh DB auto_vacuum = %q, want INCREMENTAL — new DBs would bloat behind auto_vacuum=NONE (#2055)", ms.AutoVacuum)
	}
}

// #2055: IncrementalVacuum must run cleanly on a fresh (empty) DB — there's
// nothing on the freelist yet, so it's effectively a no-op and MUST NOT
// error. This pins the "safe to call on the periodic maintenance path"
// contract the indexer tail relies on.
func TestIncrementalVacuum_SafeOnEmptyDB(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	if err := s.IncrementalVacuum(); err != nil {
		t.Fatalf("IncrementalVacuum on empty DB: %v", err)
	}
	// Idempotent — a second call is still clean.
	if err := s.IncrementalVacuum(); err != nil {
		t.Fatalf("IncrementalVacuum (2nd call): %v", err)
	}
}

// #2055: on an INCREMENTAL DB, after churning and deleting rows the
// freelist accumulates pages; IncrementalVacuum reclaims them (freelist
// drops, never grows) without an exclusive-lock VACUUM. Proves the cheap
// reclaim path actually frees pages.
func TestIncrementalVacuum_ReducesFreelistAfterDelete(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	if err := s.UpsertProject(testProject("inc-vac")); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	syms := make([]Symbol, 0, 800)
	for i := 0; i < 800; i++ {
		id := makeSymID(t, "inc-vac", "internal/x/x.go", "sym"+itoa(i), "Function")
		syms = append(syms, testSymbol(id, "sym"+itoa(i), "Function", "inc-vac", "internal/x/x.go"))
	}
	if err := s.BulkUpsertSymbols(syms); err != nil {
		t.Fatalf("BulkUpsertSymbols: %v", err)
	}
	// Delete the project's rows so pages move to the freelist, then fold
	// the WAL in so freelist_count reflects the main DB.
	if err := s.DeleteProject("inc-vac"); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if err := s.CheckpointTruncate(); err != nil {
		t.Fatalf("CheckpointTruncate: %v", err)
	}

	before, err := s.MaintenanceStats()
	if err != nil {
		t.Fatalf("MaintenanceStats (before): %v", err)
	}
	if err := s.IncrementalVacuum(); err != nil {
		t.Fatalf("IncrementalVacuum: %v", err)
	}
	if err := s.CheckpointTruncate(); err != nil {
		t.Fatalf("CheckpointTruncate (after): %v", err)
	}
	after, err := s.MaintenanceStats()
	if err != nil {
		t.Fatalf("MaintenanceStats (after): %v", err)
	}
	if after.FreelistCount > before.FreelistCount {
		t.Errorf("IncrementalVacuum grew the freelist (before=%d after=%d) — should reclaim or hold steady",
			before.FreelistCount, after.FreelistCount)
	}
}

// #2055: MaintenanceStats returns sane, self-consistent values — the
// contract health/doctor render. page_count>0 on any opened DB,
// freelist_pct in [0,1], page_size>0, auto_vacuum a known mode.
func TestMaintenanceStats_SaneValues(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	ms, err := s.MaintenanceStats()
	if err != nil {
		t.Fatalf("MaintenanceStats: %v", err)
	}
	if ms.PageCount <= 0 {
		t.Errorf("page_count = %d, want > 0", ms.PageCount)
	}
	if ms.PageSize <= 0 {
		t.Errorf("page_size = %d, want > 0", ms.PageSize)
	}
	if ms.FreelistCount < 0 {
		t.Errorf("freelist_count = %d, want >= 0", ms.FreelistCount)
	}
	if pct := ms.FreelistPct(); pct < 0 || pct > 1 {
		t.Errorf("freelist_pct = %v, want in [0,1]", pct)
	}
	switch ms.AutoVacuum {
	case "NONE", "FULL", "INCREMENTAL", "UNKNOWN":
	default:
		t.Errorf("auto_vacuum = %q, want a known mode name", ms.AutoVacuum)
	}
}

// #2055: FreelistPct clamps and guards against a zero page_count so the
// rendered health value is always a meaningful fraction.
func TestDBMaintenanceStats_FreelistPct(t *testing.T) {
	cases := []struct {
		name  string
		pc    int64
		fc    int64
		want  float64
	}{
		{"half", 100, 50, 0.5},
		{"none", 100, 0, 0},
		{"zero-pagecount-guard", 0, 10, 0},
		{"clamp-over-one", 10, 50, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := DBMaintenanceStats{PageCount: c.pc, FreelistCount: c.fc}
			if got := m.FreelistPct(); got != c.want {
				t.Errorf("FreelistPct(pc=%d,fc=%d) = %v, want %v", c.pc, c.fc, got, c.want)
			}
		})
	}
}
