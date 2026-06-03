// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"
)

// #1013: handleNeighborhood used to clamp limit only on the low end
// (limit <= 0 → 50) and ship anything else through unchanged. A
// caller passing limit=99999 got "every symbol in the file" — on big
// files (server.go has 200+ symbols) the response blew the MCP
// per-call token cap and the agent saw a truncation error with no
// recovery path. Now: clamp at 500 (matching search's #532 ceiling)
// and surface the clamp in _meta.warnings, same UX as elsewhere.

func TestHandleNeighborhood_HugeLimitClampsTo500WithWarning(t *testing.T) {
	t.Parallel()
	srv, _, _ := setupBigNeighborhood(t, 100)

	res, err := srv.handleNeighborhood(context.Background(), makeReq(map[string]any{
		"id":    "big::main.F0#Function",
		"limit": 99999,
	}))
	if err != nil {
		t.Fatalf("handleNeighborhood: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success; got error %s", textOf(t, res))
	}

	body := decode(t, res)
	meta, _ := body["_meta"].(map[string]any)
	warnings, _ := meta["warnings"].([]any)
	foundClamp := false
	for _, w := range warnings {
		if s, _ := w.(string); strings.Contains(s, "clamped to 500") {
			foundClamp = true
			break
		}
	}
	if !foundClamp {
		t.Errorf("expected limit-clamp warning naming 500; got warnings=%v", warnings)
	}

	// The neighbors slice itself is bounded by what setupBigNeighborhood
	// seeded (100), but the limit was clamped to 500 — we just want to
	// confirm the response landed cleanly with the warning surfaced.
	neighbors, _ := body["neighbors"].([]any)
	if len(neighbors) == 0 {
		t.Errorf("expected non-empty neighbors; got 0")
	}
}

// Confirm a request inside the valid range (limit=200) is unchanged
// and does NOT carry the upper-bound clamp warning — regression guard
// against the clamp firing too aggressively.
func TestHandleNeighborhood_InRangeLimitNoClampWarning(t *testing.T) {
	t.Parallel()
	srv, _, _ := setupBigNeighborhood(t, 100)

	res, err := srv.handleNeighborhood(context.Background(), makeReq(map[string]any{
		"id":    "big::main.F0#Function",
		"limit": 200,
	}))
	if err != nil {
		t.Fatalf("handleNeighborhood: %v", err)
	}
	body := decode(t, res)
	meta, _ := body["_meta"].(map[string]any)
	warnings, _ := meta["warnings"].([]any)
	for _, w := range warnings {
		if s, _ := w.(string); strings.Contains(s, "clamped to 500") {
			t.Errorf("limit=200 (in range) must not trigger upper-bound clamp; got warning %q", s)
		}
	}
}
