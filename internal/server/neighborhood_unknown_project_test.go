// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"
)

// #1025: handleNeighborhood silently fell back to global symbol
// lookup when the caller passed a `project` arg that didn't
// resolve. A typo'd project name + a valid id from a different
// project returned that other project's siblings with no warning.
// Same silent-fallback pattern as #1023 (health) and #1024 (stats).
// Now: clamp warning naming the failed lookup.

func TestHandleNeighborhood_UnknownProject_WarnsAndFallsBack(t *testing.T) {
	t.Parallel()
	srv, _, _ := setupBigNeighborhood(t, 30)

	res, err := srv.handleNeighborhood(context.Background(), makeReq(map[string]any{
		"id":      "big::main.F0#Function",
		"project": "totally-bogus-project",
	}))
	if err != nil {
		t.Fatalf("handleNeighborhood: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success (fallback); got error: %s", textOf(t, res))
	}
	body := decode(t, res)
	meta, _ := body["_meta"].(map[string]any)
	warnings, _ := meta["warnings"].([]any)
	foundWarn := false
	for _, w := range warnings {
		if s, _ := w.(string); strings.Contains(s, "totally-bogus-project") && strings.Contains(s, "did not resolve") {
			foundWarn = true
			break
		}
	}
	if !foundWarn {
		t.Errorf("expected project-resolution warning naming the failed lookup; got warnings=%v", warnings)
	}
}
