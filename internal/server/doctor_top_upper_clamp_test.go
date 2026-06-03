// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"
)

// #1016 / #1054: handleDoctor's `top` parameter has both a low-end
// (top <= 0 → 10) and high-end clamp. #1016 set the ceiling at 500.
// #1054 lowered it to 50 because doctor returns THREE sections at
// `top` each (projects / extraction_failures / slow_queries) plus
// per-row detail (file paths, multi-line stack traces) — at top=500
// the response still ran ~218 KB and exceeded the MCP per-call
// token cap. Dogfood-discovered: `doctor top=99999` (which #1016
// clamped to 500) still returned "result exceeds maximum allowed
// tokens" with no recovery affordance.

func TestHandleDoctor_HugeTopClampsTo50WithWarning(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)

	res, err := srv.handleDoctor(context.Background(), makeReq(map[string]any{
		"top": float64(99999),
	}))
	if err != nil {
		t.Fatalf("handleDoctor: %v", err)
	}
	body := decode(t, res)
	meta, _ := body["_meta"].(map[string]any)
	warnings, _ := meta["warnings"].([]any)
	foundClamp := false
	for _, w := range warnings {
		if s, _ := w.(string); strings.Contains(s, "top=99999") && strings.Contains(s, "clamped to 50") {
			foundClamp = true
			break
		}
	}
	if !foundClamp {
		t.Errorf("expected top upper-bound clamp warning; got warnings=%v", warnings)
	}
}

// #1054: top=500 (the pre-#1054 ceiling) is now over the new ceiling
// of 50 and must clamp + warn. Documents the behaviour change.
func TestHandleDoctor_Top500ClampsTo50WithWarning(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)

	res, err := srv.handleDoctor(context.Background(), makeReq(map[string]any{
		"top": float64(500),
	}))
	if err != nil {
		t.Fatalf("handleDoctor: %v", err)
	}
	body := decode(t, res)
	meta, _ := body["_meta"].(map[string]any)
	warnings, _ := meta["warnings"].([]any)
	saw := false
	for _, w := range warnings {
		if s, _ := w.(string); strings.Contains(s, "top=500") && strings.Contains(s, "clamped to 50") {
			saw = true
			break
		}
	}
	if !saw {
		t.Errorf("expected top=500 → clamped to 50 warning; got warnings=%v", warnings)
	}
}

// Regression guard: top inside the valid range produces no clamp.
func TestHandleDoctor_InRangeTopNoUpperClamp(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)

	res, err := srv.handleDoctor(context.Background(), makeReq(map[string]any{
		"top": float64(40),
	}))
	if err != nil {
		t.Fatalf("handleDoctor: %v", err)
	}
	body := decode(t, res)
	meta, _ := body["_meta"].(map[string]any)
	warnings, _ := meta["warnings"].([]any)
	for _, w := range warnings {
		if s, _ := w.(string); strings.Contains(s, "clamped to 50") {
			t.Errorf("top=40 (in range) must not trigger upper-bound clamp; got warning %q", s)
		}
	}
}

// Edge: top=50 (the ceiling) must not trip the clamp warning.
func TestHandleDoctor_TopAtCeiling_NoClampWarning(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)

	res, err := srv.handleDoctor(context.Background(), makeReq(map[string]any{
		"top": float64(50),
	}))
	if err != nil {
		t.Fatalf("handleDoctor: %v", err)
	}
	body := decode(t, res)
	meta, _ := body["_meta"].(map[string]any)
	warnings, _ := meta["warnings"].([]any)
	for _, w := range warnings {
		if s, _ := w.(string); strings.Contains(s, "top=50") && strings.Contains(s, "clamped") {
			t.Errorf("top=50 is at the ceiling — must not warn; got %q", s)
		}
	}
}
