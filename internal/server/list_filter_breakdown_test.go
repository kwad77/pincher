// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #505: filtered_out lump-sum is opaque. The breakdown + diagnosis
// surfaces what knob recovers each hidden entry.
func TestHandleList_FilterBreakdown_SurfacesByReason(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)

	now := time.Now()
	old := now.Add(-30 * 24 * time.Hour) // 30 days ago, past 14-day cutoff

	// 1 visible project: live path, recent, has edges
	if err := store.UpsertProject(db.Project{
		ID: "live", Path: t.TempDir(), Name: "live",
		IndexedAt: now, EdgeCount: 100,
	}); err != nil {
		t.Fatalf("UpsertProject live: %v", err)
	}
	// 1 dead-path project (path doesn't exist on disk)
	if err := store.UpsertProject(db.Project{
		ID: "dead", Path: "/nonexistent/path/xyz123", Name: "dead",
		IndexedAt: now, EdgeCount: 100,
	}); err != nil {
		t.Fatalf("UpsertProject dead: %v", err)
	}
	// 1 inactive project (recent path, indexed long ago)
	inactiveDir := t.TempDir()
	if err := store.UpsertProject(db.Project{
		ID: "inactive", Path: inactiveDir, Name: "inactive",
		IndexedAt: old, EdgeCount: 100,
	}); err != nil {
		t.Fatalf("UpsertProject inactive: %v", err)
	}
	// 1 low-edges project (live, recent, but 0 edges)
	lowDir := t.TempDir()
	if err := store.UpsertProject(db.Project{
		ID: "low", Path: lowDir, Name: "low",
		IndexedAt: now, EdgeCount: 0,
	}); err != nil {
		t.Fatalf("UpsertProject low: %v", err)
	}

	req := makeReq(map[string]any{})
	req.Params.Name = "list"
	result, err := srv.handleList(context.Background(), req)
	if err != nil {
		t.Fatalf("handleList: %v", err)
	}
	body := decode(t, result)

	// Only the live one should appear.
	rows, _ := body["projects"].([]any)
	if len(rows) != 1 {
		t.Errorf("expected 1 visible project; got %d", len(rows))
	}

	// Breakdown should account for the 3 hidden ones.
	breakdown, _ := body["filtered_breakdown"].(map[string]any)
	if breakdown == nil {
		t.Fatalf("missing filtered_breakdown")
	}
	if got := int(breakdown["dead_path"].(float64)); got != 1 {
		t.Errorf("dead_path=%d, want 1", got)
	}
	if got := int(breakdown["inactive"].(float64)); got != 1 {
		t.Errorf("inactive=%d, want 1", got)
	}
	if got := int(breakdown["low_edges"].(float64)); got != 1 {
		t.Errorf("low_edges=%d, want 1", got)
	}

	// _meta.filter_diagnosis should name the recovery args.
	meta, _ := body["_meta"].(map[string]any)
	diag, _ := meta["filter_diagnosis"].(string)
	if diag == "" {
		t.Fatalf("missing _meta.filter_diagnosis")
	}
	for _, want := range []string{"include_dead=true", "active=false", "min_edges=0"} {
		if !strings.Contains(diag, want) {
			t.Errorf("diagnosis should mention recovery arg %q; got %q", want, diag)
		}
	}
}

// When nothing was filtered, no diagnosis should appear (no signal needed).
func TestHandleList_NoFilter_NoDiagnosis(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)

	if err := store.UpsertProject(db.Project{
		ID: "only", Path: t.TempDir(), Name: "only",
		IndexedAt: time.Now(), EdgeCount: 100,
	}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}

	req := makeReq(map[string]any{})
	req.Params.Name = "list"
	result, err := srv.handleList(context.Background(), req)
	if err != nil {
		t.Fatalf("handleList: %v", err)
	}
	body := decode(t, result)

	if got := int(body["filtered_out"].(float64)); got != 0 {
		t.Errorf("expected filtered_out=0; got %d", got)
	}
	meta, _ := body["_meta"].(map[string]any)
	if meta != nil {
		if _, present := meta["filter_diagnosis"]; present {
			t.Errorf("filter_diagnosis must be absent when nothing filtered; got meta=%v", meta)
		}
	}
}
