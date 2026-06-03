// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"
	"time"
)

// #420: stats SESSION view exposes "Process up:" so the agent can tell
// the session counters are scoped to the current inner process. The
// supervisor respawns the inner on binary swaps / crashes, which
// resets the in-memory counters — without this signal the agent
// can't distinguish "session is empty because nothing happened" from
// "session looks empty because the inner just respawned and prior
// stats rolled into ALL-TIME".

func TestHandleStats_SurfacesProcessUptime(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	srv.sessionStartedAt = time.Now().Add(-7 * time.Minute)

	result, err := srv.handleStats(context.Background(), makeReq(nil))
	if err != nil {
		t.Fatalf("handleStats: %v", err)
	}
	body := textOf(t, result)
	if !strings.Contains(body, "Process up:") {
		t.Errorf("stats output missing 'Process up:' line; body=%q", body)
	}
	if !strings.Contains(body, "7m") {
		t.Errorf("stats output should show ~7m uptime; body=%q", body)
	}
}

func TestHumanDuration(t *testing.T) {
	t.Parallel()
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "0s"},
		{45 * time.Second, "45s"},
		{1 * time.Minute, "1m"},
		{59 * time.Minute, "59m"},
		{1 * time.Hour, "1h"},
		{1*time.Hour + 30*time.Minute, "1h30m"},
		{3 * time.Hour, "3h"},
	}
	for _, c := range cases {
		got := humanDuration(c.d)
		if got != c.want {
			t.Errorf("humanDuration(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}
