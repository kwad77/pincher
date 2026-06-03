// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// `list min_edges=N` with a high N filters out projects but the
// pre-fix diagnosis blamed "active/dead" causes only, ignoring
// min_edges. Recovery next_steps listed active=false and
// include_dead=true but NOT min_edges=0 — so callers whose actual
// filter cause was min_edges had no path forward in the response.

func TestHandleList_HighMinEdgesDiagnosisNamesMinEdges(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)

	// Seed one project with low edge count so min_edges drops it.
	// Use a real existing path so the dead-on-disk filter doesn't
	// also fire and dominate the diagnosis text.
	projPath := t.TempDir()
	store.UpsertProject(db.Project{
		ID: "p-low", Path: projPath, Name: "p-low",
		IndexedAt: time.Now(), FileCount: 5, SymCount: 50, EdgeCount: 2,
	})

	res, err := srv.handleList(context.Background(), makeReq(map[string]any{
		"min_edges": 1000,
	}))
	if err != nil {
		t.Fatalf("handleList: %v", err)
	}
	body := decode(t, res)
	meta, _ := body["_meta"].(map[string]any)
	if meta == nil {
		t.Fatal("_meta missing")
	}

	// Diagnosis must name the min_edges cause.
	diag, _ := meta["diagnosis"].(string)
	if !strings.Contains(diag, "min_edges") {
		t.Errorf("diagnosis should name min_edges as the cause; got %q", diag)
	}

	// next_steps must include the min_edges=0 recovery hint.
	steps, _ := meta["next_steps"].([]any)
	foundMinEdges := false
	for _, s := range steps {
		if step, ok := s.(map[string]any); ok {
			if args, _ := step["args"].(string); strings.Contains(args, `"min_edges":0`) {
				foundMinEdges = true
				break
			}
		}
	}
	if !foundMinEdges {
		t.Errorf("expected min_edges=0 recovery next_step; got %v", steps)
	}
}

// Inactive-only baseline: diagnosis still names the active filter
// and the next_steps still surface active=false.
func TestHandleList_InactiveOnlyDiagnosisNamesActive(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)

	// One project: indexed long ago so it's "inactive". Plenty of edges
	// so min_edges doesn't drop it. Use a real path so dead-on-disk
	// doesn't fire either — testing the inactive cause in isolation.
	projPath := t.TempDir()
	store.UpsertProject(db.Project{
		ID: "p-old", Path: projPath, Name: "p-old",
		IndexedAt: time.Now().AddDate(0, -2, 0),
		FileCount: 50, SymCount: 5000, EdgeCount: 10000,
	})

	res, err := srv.handleList(context.Background(), makeReq(map[string]any{}))
	if err != nil {
		t.Fatalf("handleList: %v", err)
	}
	body := decode(t, res)
	meta, _ := body["_meta"].(map[string]any)
	diag, _ := meta["diagnosis"].(string)
	if !strings.Contains(diag, "inactive") {
		t.Errorf("diagnosis should name inactive cause; got %q", diag)
	}
	steps, _ := meta["next_steps"].([]any)
	foundActive := false
	for _, s := range steps {
		if step, ok := s.(map[string]any); ok {
			if args, _ := step["args"].(string); strings.Contains(args, `"active":false`) {
				foundActive = true
				break
			}
		}
	}
	if !foundActive {
		t.Errorf("expected active=false recovery next_step; got %v", steps)
	}
}
