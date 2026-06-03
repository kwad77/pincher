// SPDX-License-Identifier: MIT

package server

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #707: /v1/projects gained MCP-parity filter query params (?active,
// ?active_within_days, ?include_dead, ?min_edges) to close the
// dashboard↔MCP gap. Defaults stay unfiltered (preserves dashboard
// dropdown completeness — loadProjects / populateSearchProjects /
// ADR dropdown all want every row); explicit flags engage filtering.

func seedMixedProjects(t *testing.T, store *db.Store, now time.Time) {
	t.Helper()
	// Use t.TempDir for "real" paths so os.Stat finds them — that's
	// how the filter distinguishes alive vs dead.
	realRoot := t.TempDir()
	// Real + recent + edged — survives every default filter.
	if err := store.UpsertProject(db.Project{
		ID: "alive", Path: realRoot, Name: "alive",
		IndexedAt: now, EdgeCount: 10, SymCount: 50, FileCount: 5,
	}); err != nil {
		t.Fatalf("seed alive: %v", err)
	}
	// Dead-path (path doesn't exist on disk).
	if err := store.UpsertProject(db.Project{
		ID: "dead_path", Path: "/nonexistent/dead_xyz_no_match", Name: "dead_path",
		IndexedAt: now, EdgeCount: 10,
	}); err != nil {
		t.Fatalf("seed dead_path: %v", err)
	}
	// Inactive — indexed 30 days ago; path is real to isolate the test.
	inactiveDir := t.TempDir()
	if err := store.UpsertProject(db.Project{
		ID: "inactive", Path: inactiveDir, Name: "inactive",
		IndexedAt: now.Add(-30 * 24 * time.Hour), EdgeCount: 10,
	}); err != nil {
		t.Fatalf("seed inactive: %v", err)
	}
	// Low-edges — recent, real, but EdgeCount=0.
	lowDir := t.TempDir()
	if err := store.UpsertProject(db.Project{
		ID: "low_edges", Path: lowDir, Name: "low_edges",
		IndexedAt: now, EdgeCount: 0,
	}); err != nil {
		t.Fatalf("seed low_edges: %v", err)
	}
}

func TestProjectsHTTP_DefaultUnfiltered(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	seedMixedProjects(t, store, time.Now())

	// No query params → unfiltered: all 4 rows visible.
	w := httpGet(t, srv, "/v1/projects")
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Projects []map[string]any `json:"projects"`
		Total    int              `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Total != 4 {
		t.Errorf("default-unfiltered: total=%d want 4 (preserves dashboard contract)", resp.Total)
	}
}

func TestProjectsHTTP_FilterMinEdges(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	seedMixedProjects(t, store, time.Now())

	w := httpGet(t, srv, "/v1/projects?min_edges=1")
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Projects          []map[string]any `json:"projects"`
		Total             int              `json:"total"`
		FilteredBreakdown map[string]int   `json:"filtered_breakdown"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// low_edges has EdgeCount=0 → dropped; the other 3 survive.
	if resp.Total != 3 {
		t.Errorf("min_edges=1: total=%d want 3", resp.Total)
	}
	if resp.FilteredBreakdown["low_edges"] != 1 {
		t.Errorf("expected 1 low_edges drop; got %v", resp.FilteredBreakdown)
	}
}

func TestProjectsHTTP_FilterActive(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	seedMixedProjects(t, store, time.Now())

	w := httpGet(t, srv, "/v1/projects?active=true&active_within_days=14")
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Total             int            `json:"total"`
		FilteredBreakdown map[string]int `json:"filtered_breakdown"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// inactive (30 days old) dropped; other 3 survive.
	if resp.Total != 3 {
		t.Errorf("active=true: total=%d want 3", resp.Total)
	}
	if resp.FilteredBreakdown["inactive"] != 1 {
		t.Errorf("expected 1 inactive drop; got %v", resp.FilteredBreakdown)
	}
}

func TestProjectsHTTP_FilterIncludeDeadFalse(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	seedMixedProjects(t, store, time.Now())

	w := httpGet(t, srv, "/v1/projects?include_dead=false")
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Total             int            `json:"total"`
		FilteredBreakdown map[string]int `json:"filtered_breakdown"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Total != 3 {
		t.Errorf("include_dead=false: total=%d want 3", resp.Total)
	}
	if resp.FilteredBreakdown["dead_path"] != 1 {
		t.Errorf("expected 1 dead_path drop; got %v", resp.FilteredBreakdown)
	}
}

func TestProjectsHTTP_MCPParityCombo(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	seedMixedProjects(t, store, time.Now())

	// All MCP-default filters at once — only "alive" survives.
	w := httpGet(t, srv, "/v1/projects?active=true&include_dead=false&min_edges=1")
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Projects []map[string]any `json:"projects"`
		Total    int              `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Total != 1 {
		t.Errorf("mcp-parity combo: total=%d want 1 (only alive survives)", resp.Total)
	}
	if len(resp.Projects) == 1 {
		if id, _ := resp.Projects[0]["id"].(string); id != "alive" {
			t.Errorf("expected the surviving project to be 'alive'; got %v", resp.Projects[0])
		}
	}
}

func TestProjectsHTTP_FilterBreakdownAlwaysPresent(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	seedMixedProjects(t, store, time.Now())

	// Unfiltered request — breakdown should still be present with all
	// zeros so dashboard consumers can rely on the shape.
	w := httpGet(t, srv, "/v1/projects")
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var raw map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := raw["filtered_breakdown"]; !ok {
		t.Error("filtered_breakdown must be present even on unfiltered requests (shape stability)")
	}
	if _, ok := raw["filtered_out"]; !ok {
		t.Error("filtered_out must be present even on unfiltered requests")
	}
	if fo, ok := raw["filtered_out"].(float64); !ok || fo != 0 {
		t.Errorf("filtered_out should be 0 on unfiltered request; got %v", raw["filtered_out"])
	}
	_ = fmt.Sprintf // silence unused-import in some shapes
}
