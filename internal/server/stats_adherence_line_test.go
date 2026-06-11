// SPDX-License-Identifier: MIT

package server

// Guide-coaching PR-15/17: the stats SESSION box surfaces next_steps
// adherence ("next_steps followed: X% (E emitted)") so a 0%-followed
// session is visible instead of silently stamped in _meta only.
// Suppressed at E=0 — a fresh server stays noise-free.

import (
	"context"
	"strings"
	"testing"
)

func TestHandleStats_NextStepsAdherenceLine(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	seedAdherence(&srv.nextStepsAdherence, "trace", 4, 1) // 25%

	result, err := srv.handleStats(context.Background(), makeReq(map[string]any{}))
	if err != nil || result.IsError {
		t.Fatalf("handleStats: err=%v isErr=%v", err, result.IsError)
	}
	text := textOf(t, result)
	if !strings.Contains(text, "next_steps followed:") {
		t.Fatalf("SESSION box missing adherence line:\n%s", text)
	}
	if !strings.Contains(text, "25% (4 emitted)") {
		t.Errorf("adherence line value wrong, want \"25%% (4 emitted)\":\n%s", text)
	}
}

func TestHandleStats_NoAdherenceLineWhenNothingEmitted(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)

	result, err := srv.handleStats(context.Background(), makeReq(map[string]any{}))
	if err != nil || result.IsError {
		t.Fatalf("handleStats: err=%v isErr=%v", err, result.IsError)
	}
	if text := textOf(t, result); strings.Contains(text, "next_steps followed:") {
		t.Errorf("adherence line rendered with zero emissions:\n%s", text)
	}
}
