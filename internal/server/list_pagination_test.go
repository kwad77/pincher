// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #301: list defaults to limit=50 (was 0=unlimited). Dev machines
// with 100+ indexed projects (worktree fan-out from adjacent tools)
// were burning ~10K tokens per orientation lookup. Pagination caps
// the page size and surfaces the next page in _meta.next_steps.

// seedProjects inserts N alive projects with a recent indexed_at so
// they pass the active filter.
func seedProjects(t *testing.T, store *db.Store, n int) {
	t.Helper()
	now := time.Now()
	for i := 0; i < n; i++ {
		// Each project needs a real path so the !includeDead os.Stat
		// branch doesn't drop it.
		dir := t.TempDir()
		store.UpsertProject(db.Project{
			ID:        fmt.Sprintf("p%03d", i),
			Path:      dir,
			Name:      fmt.Sprintf("p%03d", i),
			IndexedAt: now,
			EdgeCount: 1, // #419: skip the default min_edges=1 empty-graph filter
		})
	}
}

// Default limit caps to 50 even when 100 projects are indexed.
func TestHandleList_DefaultLimitCapsToFifty(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	seedProjects(t, store, 100)
	result, err := srv.handleList(context.Background(), makeReq(map[string]any{}))
	if err != nil {
		t.Fatalf("handleList: %v", err)
	}
	m := decode(t, result)
	if got := m["count"].(float64); got != 100 {
		t.Errorf("count = %v, want 100 (total after filter)", got)
	}
	page, _ := m["page"].(map[string]any)
	if got := page["returned"].(float64); got != 50 {
		t.Errorf("page.returned = %v, want 50 (default limit)", got)
	}
	projects, _ := m["projects"].([]any)
	if len(projects) != 50 {
		t.Errorf("len(projects) = %d, want 50", len(projects))
	}
}

// _meta.next_steps surfaces the next page when more remain.
func TestHandleList_NextStepsSurfaceNextPage(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	seedProjects(t, store, 100)
	result, err := srv.handleList(context.Background(), makeReq(map[string]any{}))
	if err != nil {
		t.Fatalf("handleList: %v", err)
	}
	m := decode(t, result)
	meta, _ := m["_meta"].(map[string]any)
	steps, _ := meta["next_steps"].([]any)
	if len(steps) == 0 {
		t.Fatalf("expected _meta.next_steps for partial page; got %v", meta)
	}
	step, _ := steps[0].(map[string]any)
	if step["tool"] != "list" {
		t.Errorf("next_steps[0].tool = %v, want list", step["tool"])
	}
	args, _ := step["args"].(string)
	if !strings.Contains(args, `"offset":50`) {
		t.Errorf("next_steps args should advance offset to 50, got: %s", args)
	}
}

// Explicit offset returns the requested window.
func TestHandleList_OffsetReturnsWindow(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	seedProjects(t, store, 100)
	result, err := srv.handleList(context.Background(), makeReq(map[string]any{
		"limit":  10,
		"offset": 50,
	}))
	if err != nil {
		t.Fatalf("handleList: %v", err)
	}
	m := decode(t, result)
	page, _ := m["page"].(map[string]any)
	if page["offset"].(float64) != 50 {
		t.Errorf("page.offset = %v, want 50", page["offset"])
	}
	if page["returned"].(float64) != 10 {
		t.Errorf("page.returned = %v, want 10", page["returned"])
	}
}

// Tail page (last partial window) emits no list-pagination next_step.
func TestHandleList_TailPageHasNoNextStep(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	seedProjects(t, store, 100)
	result, err := srv.handleList(context.Background(), makeReq(map[string]any{
		"limit":  20,
		"offset": 90,
	}))
	if err != nil {
		t.Fatalf("handleList: %v", err)
	}
	m := decode(t, result)
	page, _ := m["page"].(map[string]any)
	if page["returned"].(float64) != 10 {
		t.Errorf("page.returned = %v, want 10 (tail)", page["returned"])
	}
	meta, _ := m["_meta"].(map[string]any)
	if steps, ok := meta["next_steps"].([]any); ok {
		for _, s := range steps {
			step, _ := s.(map[string]any)
			if step["tool"] == "list" {
				t.Errorf("tail page shouldn't surface a list pagination next_step: %v", step)
			}
		}
	}
}

// Out-of-range offset clamps to a zero-length window.
func TestHandleList_OutOfRangeOffsetReturnsEmpty(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	seedProjects(t, store, 5)
	result, err := srv.handleList(context.Background(), makeReq(map[string]any{
		"offset": 999,
	}))
	if err != nil {
		t.Fatalf("handleList: %v", err)
	}
	if result.IsError {
		m := decode(t, result)
		t.Fatalf("offset out of range should not error: %v", m)
	}
	m := decode(t, result)
	projects, _ := m["projects"].([]any)
	if len(projects) != 0 {
		t.Errorf("len(projects) = %d, want 0", len(projects))
	}
	if got := m["count"].(float64); got != 5 {
		t.Errorf("count = %v, want 5 (still reports total)", got)
	}
}

// limit=0 preserves the legacy unbounded behaviour for callers that
// explicitly opt back in.
func TestHandleList_LimitZeroReturnsAll(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	seedProjects(t, store, 75)
	result, err := srv.handleList(context.Background(), makeReq(map[string]any{
		"limit": 0,
	}))
	if err != nil {
		t.Fatalf("handleList: %v", err)
	}
	m := decode(t, result)
	projects, _ := m["projects"].([]any)
	if len(projects) != 75 {
		t.Errorf("len(projects) = %d, want 75 (limit=0 means unlimited per legacy contract)", len(projects))
	}
	// No next_steps when everything fits.
	meta, _ := m["_meta"].(map[string]any)
	if _, ok := meta["next_steps"]; ok {
		t.Errorf("limit=0 must not surface a pagination next_step")
	}
}
