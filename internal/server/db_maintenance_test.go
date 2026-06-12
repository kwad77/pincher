// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// #2055: dbMaintenanceSection is the pure renderer for the db_maintenance
// health/doctor block. It must compute freelist_pct, flip high_bloat when
// the freelist exceeds the threshold, and flip wal_near_cap as the WAL
// approaches the 256 MiB soft cap. Table-driven so the thresholds are
// pinned without a live DB.
func TestDBMaintenanceSection_Thresholds(t *testing.T) {
	cases := []struct {
		name          string
		stats         db.DBMaintenanceStats
		walBytes      int64
		wantPct       float64
		wantHighBloat bool
		wantWALNear   bool
	}{
		{
			name:          "healthy",
			stats:         db.DBMaintenanceStats{PageCount: 1000, FreelistCount: 50, PageSize: 4096, AutoVacuum: "INCREMENTAL"},
			walBytes:      1 << 20, // 1 MiB
			wantPct:       0.05,
			wantHighBloat: false,
			wantWALNear:   false,
		},
		{
			name:          "high-bloat-fires-above-25pct",
			stats:         db.DBMaintenanceStats{PageCount: 1000, FreelistCount: 720, PageSize: 4096, AutoVacuum: "INCREMENTAL"}, // 0.72, hermes-shaped
			walBytes:      0,
			wantPct:       0.72,
			wantHighBloat: true,
			wantWALNear:   false,
		},
		{
			name:          "bloat-just-under-threshold-stays-quiet",
			stats:         db.DBMaintenanceStats{PageCount: 1000, FreelistCount: 250, PageSize: 4096, AutoVacuum: "NONE"}, // exactly 0.25, not > 0.25
			walBytes:      0,
			wantPct:       0.25,
			wantHighBloat: false,
			wantWALNear:   false,
		},
		{
			name:          "wal-near-cap-fires",
			stats:         db.DBMaintenanceStats{PageCount: 1000, FreelistCount: 10, PageSize: 4096, AutoVacuum: "INCREMENTAL"},
			walBytes:      250 << 20, // 250 MiB — past 90% of the 256 MiB cap
			wantPct:       0.01,
			wantHighBloat: false,
			wantWALNear:   true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sec := dbMaintenanceSection(c.stats, c.walBytes)
			if got := sec["freelist_pct"].(float64); got != c.wantPct {
				t.Errorf("freelist_pct = %v, want %v", got, c.wantPct)
			}
			if got := sec["high_bloat"].(bool); got != c.wantHighBloat {
				t.Errorf("high_bloat = %v, want %v", got, c.wantHighBloat)
			}
			if got := sec["wal_near_cap"].(bool); got != c.wantWALNear {
				t.Errorf("wal_near_cap = %v, want %v", got, c.wantWALNear)
			}
			if got := sec["auto_vacuum"].(string); got != c.stats.AutoVacuum {
				t.Errorf("auto_vacuum = %q, want %q", got, c.stats.AutoVacuum)
			}
			if got := sec["wal_size_bytes"].(int64); got != c.walBytes {
				t.Errorf("wal_size_bytes = %d, want %d", got, c.walBytes)
			}
		})
	}
}

// #2055: handleHealth surfaces the db_maintenance block with sane values
// from the live session DB — page_count>0, freelist_pct in [0,1], a known
// auto_vacuum mode (INCREMENTAL on a fresh test DB). Pins the wiring, not
// just the renderer in isolation.
func TestHandleHealth_IncludesDBMaintenance(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)

	result, err := srv.handleHealth(context.Background(), makeReq(map[string]any{}))
	if err != nil {
		t.Fatalf("handleHealth: %v", err)
	}
	body := decode(t, result)
	raw, ok := body["db_maintenance"]
	if !ok {
		t.Fatal("health response missing 'db_maintenance' — wiring broken (#2055)")
	}
	assertDBMaintenanceShape(t, raw)
}

// #2055: doctor carries the same db_maintenance block (operators reading
// doctor --json see freelist/WAL/auto_vacuum state alongside the existing
// advisories).
func TestHandleDoctor_IncludesDBMaintenance(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)

	result, err := srv.handleDoctor(context.Background(), makeReq(map[string]any{}))
	if err != nil {
		t.Fatalf("handleDoctor: %v", err)
	}
	body := decode(t, result)
	raw, ok := body["db_maintenance"]
	if !ok {
		t.Fatal("doctor response missing 'db_maintenance' — wiring broken (#2055)")
	}
	assertDBMaintenanceShape(t, raw)
}

// assertDBMaintenanceShape checks the live-DB db_maintenance block has the
// documented fields with sane values. JSON round-trips numbers to float64.
func assertDBMaintenanceShape(t *testing.T, raw any) {
	t.Helper()
	m, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("db_maintenance is %T, want map", raw)
	}
	pageCount, ok := m["page_count"].(float64)
	if !ok || pageCount <= 0 {
		t.Errorf("page_count = %v, want > 0", m["page_count"])
	}
	pct, ok := m["freelist_pct"].(float64)
	if !ok || pct < 0 || pct > 1 {
		t.Errorf("freelist_pct = %v, want in [0,1]", m["freelist_pct"])
	}
	av, ok := m["auto_vacuum"].(string)
	if !ok || av == "" {
		t.Errorf("auto_vacuum = %v, want non-empty mode name", m["auto_vacuum"])
	}
	if av != "INCREMENTAL" {
		t.Errorf("fresh test DB auto_vacuum = %q, want INCREMENTAL (#2055)", av)
	}
	if _, ok := m["freelist_count"]; !ok {
		t.Error("db_maintenance missing freelist_count")
	}
	if _, ok := m["wal_size_bytes"]; !ok {
		t.Error("db_maintenance missing wal_size_bytes")
	}
	if _, ok := m["high_bloat"].(bool); !ok {
		t.Errorf("high_bloat = %v, want bool", m["high_bloat"])
	}
}
