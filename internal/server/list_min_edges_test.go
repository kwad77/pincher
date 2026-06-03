// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #419: list defaults to hiding empty-graph projects so the orientation
// view stays useful when a dev machine has worktree fan-out
// (.claude/worktrees/{adjective-scientist} slugs) cluttering the index.
// Pass min_edges=0 to opt back into the legacy unfiltered shape.

func TestHandleList_DefaultHidesEmptyGraphProjects(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	now := time.Now()

	// Two alive-on-disk projects: one with edges, one empty.
	withEdges := t.TempDir()
	empty := t.TempDir()
	store.UpsertProject(db.Project{
		ID: "withEdges", Path: withEdges, Name: "withEdges", IndexedAt: now,
		FileCount: 5, SymCount: 20, EdgeCount: 12,
	})
	store.UpsertProject(db.Project{
		ID: "empty", Path: empty, Name: "empty", IndexedAt: now,
		FileCount: 100, SymCount: 0, EdgeCount: 0,
	})

	result, err := srv.handleList(context.Background(), makeReq(map[string]any{}))
	if err != nil {
		t.Fatalf("handleList: %v", err)
	}
	body := decode(t, result)
	count, _ := body["count"].(float64)
	if int(count) != 1 {
		t.Errorf("default count = %d, want 1 (empty-graph project hidden by min_edges=1 default); body=%v", int(count), body)
	}
	projects, _ := body["projects"].([]any)
	if len(projects) != 1 {
		t.Fatalf("returned %d projects, want 1", len(projects))
	}
	p, _ := projects[0].(map[string]any)
	if name, _ := p["name"].(string); name != "withEdges" {
		t.Errorf("returned project name = %q, want withEdges", name)
	}
}

// Caller can opt back into the unfiltered view via min_edges=0.
func TestHandleList_MinEdgesZeroIncludesEmpty(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	now := time.Now()
	withEdges := t.TempDir()
	empty := t.TempDir()
	store.UpsertProject(db.Project{
		ID: "withEdges", Path: withEdges, Name: "withEdges", IndexedAt: now,
		EdgeCount: 5,
	})
	store.UpsertProject(db.Project{
		ID: "empty", Path: empty, Name: "empty", IndexedAt: now,
		EdgeCount: 0,
	})

	result, err := srv.handleList(context.Background(), makeReq(map[string]any{
		"min_edges": float64(0),
	}))
	if err != nil {
		t.Fatalf("handleList: %v", err)
	}
	body := decode(t, result)
	count, _ := body["count"].(float64)
	if int(count) != 2 {
		t.Errorf("min_edges=0 count = %d, want 2 (both projects); body=%v", int(count), body)
	}
}
