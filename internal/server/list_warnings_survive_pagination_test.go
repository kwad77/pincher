// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #1020: handleList used to overwrite `data["_meta"]` when surfacing
// the next-page hint on a paginated response — clobbering the input-
// clamp warnings the earlier block attached. Probe:
//   list active_within_days=-5 limit=2
// hits both the clamp warning (active_within_days <=0 → default 14)
// AND the pagination next_steps (3+ projects after filters). Pre-fix,
// the clamp warning disappeared from the response.

func TestHandleList_ClampWarningSurvivesPaginationNextSteps(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)

	// Seed enough projects that limit=2 triggers pagination next_steps.
	for i := 0; i < 5; i++ {
		store.UpsertProject(db.Project{
			ID:        "p" + string(rune('a'+i)),
			Path:      "/tmp/p" + string(rune('a'+i)),
			Name:      "p" + string(rune('a'+i)),
			IndexedAt: time.Now(),
			FileCount: 1,
			SymCount:  10,
			EdgeCount: 5, // > min_edges=1 default
		})
	}

	res, err := srv.handleList(context.Background(), makeReq(map[string]any{
		"active_within_days": float64(-5),
		"limit":              float64(2),
		// include_dead=true so the fake project paths don't filter out;
		// the test isolates the warnings+next_steps interaction, not
		// dead-path detection.
		"include_dead": true,
	}))
	if err != nil {
		t.Fatalf("handleList: %v", err)
	}
	body := decode(t, res)
	meta, _ := body["_meta"].(map[string]any)
	if meta == nil {
		t.Fatal("_meta missing")
	}

	// Both must survive: the clamp warning AND the pagination next_steps.
	warnings, _ := meta["warnings"].([]any)
	foundClamp := false
	for _, w := range warnings {
		if s, _ := w.(string); strings.Contains(s, "active_within_days") {
			foundClamp = true
			break
		}
	}
	if !foundClamp {
		t.Errorf("clamp warning lost when next_steps surface; got warnings=%v", warnings)
	}

	steps, _ := meta["next_steps"].([]any)
	foundPager := false
	for _, s := range steps {
		step, _ := s.(map[string]any)
		if tool, _ := step["tool"].(string); tool == "list" {
			if args, _ := step["args"].(string); strings.Contains(args, "offset") {
				foundPager = true
				break
			}
		}
	}
	if !foundPager {
		t.Errorf("pagination next_step missing; got steps=%v", steps)
	}
}
