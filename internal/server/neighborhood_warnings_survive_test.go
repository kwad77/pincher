// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"
)

// #1021: handleNeighborhood used to clobber `data["_meta"]` when
// surfacing the next-page hint — wiping any input-clamp warning
// attached above. Same shape of bug as #1020 in handleList.
// Probe: 100 symbols seeded in one file, request limit=10 — that
// triggers the pagination block. Pair with limit=10 + clamp-shape
// to verify clamp warnings survive (the upper-bound clamp at
// limit=99999 also runs through the same block, so combine both).

func TestHandleNeighborhood_ClampWarningSurvivesPagination(t *testing.T) {
	t.Parallel()
	srv, _, _ := setupBigNeighborhood(t, 100) // file has 100 symbols

	// limit=99999 → clamped to 500 → still hits pagination (100 < 500
	// is technically not pagination, so use limit=20 + offset=0 +
	// 100-symbol file to land squarely inside the pager path AND
	// trigger the upper-bound clamp via a deliberate huge offset).
	// Actually offset=99999 will be clamped... let me use a different
	// shape: limit upper-clamp + 20 visible from a 100-file file is
	// not paginated. So combine the upper-limit clamp (which uses the
	// 500 cap) with offset=N where N < 100 - and let the file have
	// 600 entries.
	srv, _, _ = setupBigNeighborhood(t, 600)

	res, err := srv.handleNeighborhood(context.Background(), makeReq(map[string]any{
		"id":    "big::main.F0#Function",
		"limit": float64(99999), // triggers upper-bound clamp + page-too-small forces pagination
	}))
	if err != nil {
		t.Fatalf("handleNeighborhood: %v", err)
	}
	body := decode(t, res)
	meta, _ := body["_meta"].(map[string]any)
	if meta == nil {
		t.Fatal("_meta missing")
	}

	warnings, _ := meta["warnings"].([]any)
	foundClamp := false
	for _, w := range warnings {
		if s, _ := w.(string); strings.Contains(s, "clamped to 500") {
			foundClamp = true
			break
		}
	}
	if !foundClamp {
		t.Errorf("clamp warning lost when pagination next_step surfaced; got warnings=%v", warnings)
	}

	steps, _ := meta["next_steps"].([]any)
	foundPager := false
	for _, s := range steps {
		step, _ := s.(map[string]any)
		if tool, _ := step["tool"].(string); tool == "neighborhood" {
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
