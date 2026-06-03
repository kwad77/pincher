// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #1473 v0.78: when prune_dead actually deleted projects, the surviving
// projects' files/symbols/edges counts in the same response can be
// transiently collapsed (the CASCADE DELETE intersects shared paths
// before the watcher's reindex pass repopulates them). The response
// must surface `_meta.counts_may_be_transient: true` + a warning so
// callers don't read the collapsed counts as data loss.
//
// Audit shape: positive (prune happened → flag set), negative (prune
// asked but nothing dead → no flag), control (prune not asked → no
// flag regardless).

func TestHandleList_PruneDead_FlagsTransientCounts_1473(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	deadDir := filepath.Join(t.TempDir(), "ghost-doesnt-exist")
	store.UpsertProject(db.Project{
		ID: "ghost", Path: deadDir, Name: "ghost", IndexedAt: time.Now(),
		EdgeCount: 1,
	})
	aliveDir := t.TempDir()
	store.UpsertProject(db.Project{
		ID: "alive", Path: aliveDir, Name: "alive", IndexedAt: time.Now(),
		EdgeCount: 1,
	})

	result, err := srv.handleList(context.Background(), makeReq(map[string]any{
		"prune_dead": true,
	}))
	if err != nil {
		t.Fatalf("handleList: %v", err)
	}
	body := decode(t, result)
	meta, ok := body["_meta"].(map[string]any)
	if !ok {
		t.Fatal("expected _meta on response when prune_dead deleted something")
	}
	if v, _ := meta["counts_may_be_transient"].(bool); !v {
		t.Errorf("expected _meta.counts_may_be_transient=true after a real prune; meta=%v", meta)
	}
	warnings, _ := meta["warnings"].([]any)
	var sawTransientWarning bool
	for _, w := range warnings {
		s, _ := w.(string)
		if strings.Contains(s, "transiently collapsed") {
			sawTransientWarning = true
			break
		}
	}
	if !sawTransientWarning {
		t.Errorf("expected a transient-state warning in _meta.warnings; got: %v", warnings)
	}
}

func TestHandleList_PruneDead_NoFlagWhenNothingDeleted_1473(t *testing.T) {
	// Negative shape. prune_dead asked but nothing was actually
	// deleted (all projects' paths exist) → no transient state could
	// arise, so no flag and no warning.
	t.Parallel()
	srv, store, _ := newTestServer(t)
	aliveDir := t.TempDir()
	store.UpsertProject(db.Project{
		ID: "alive", Path: aliveDir, Name: "alive", IndexedAt: time.Now(),
		EdgeCount: 1,
	})

	result, err := srv.handleList(context.Background(), makeReq(map[string]any{
		"prune_dead": true,
	}))
	if err != nil {
		t.Fatalf("handleList: %v", err)
	}
	body := decode(t, result)
	if meta, ok := body["_meta"].(map[string]any); ok {
		if v, _ := meta["counts_may_be_transient"].(bool); v {
			t.Errorf("counts_may_be_transient must NOT be set when prune deleted nothing; meta=%v", meta)
		}
	}
}

func TestHandleList_NoPruneDead_NoTransientFlag_1473(t *testing.T) {
	// Control shape. prune_dead not requested at all → no flag
	// regardless of project state.
	t.Parallel()
	srv, store, _ := newTestServer(t)
	aliveDir := t.TempDir()
	store.UpsertProject(db.Project{
		ID: "alive", Path: aliveDir, Name: "alive", IndexedAt: time.Now(),
		EdgeCount: 1,
	})

	result, err := srv.handleList(context.Background(), makeReq(map[string]any{}))
	if err != nil {
		t.Fatalf("handleList: %v", err)
	}
	body := decode(t, result)
	if meta, ok := body["_meta"].(map[string]any); ok {
		if v, _ := meta["counts_may_be_transient"].(bool); v {
			t.Errorf("counts_may_be_transient must NOT be set when prune_dead not requested; meta=%v", meta)
		}
	}
}
