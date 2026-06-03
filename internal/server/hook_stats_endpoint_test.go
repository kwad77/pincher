// SPDX-License-Identifier: MIT

package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #628: GET /v1/hook-stats returns 7d conversion rate + raw counts.
// Backs the dashboard headline panel. Telemetry is local-only — no
// auth gymnastics needed for the panel to render in a default-deny
// loopback dashboard.

func TestHTTP_HookStats_EmptyDB(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/hook-stats", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, readBody(t, rr))
	}
	body := string(readBody(t, rr))
	for _, want := range []string{
		`"window":"7d"`, `"redirects":0`, `"taken":0`, `"conversion_pct":0`,
		// #629: triangulating panels — empty store still emits the
		// shape so the dashboard can render zero-state without
		// undefined-key checks.
		`"resolved":0`, `"overrides":0`, `"override_pct":0`, `"by_tool":{}`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body should contain %s; got %s", want, body)
		}
	}
}

func TestHTTP_HookStats_WithData(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	now := time.Now().UnixNano()

	// Two redirects, one taken.
	if err := store.LogHookInvocation(db.HookInvocation{
		TS: now, SessionID: "sess1", ToolName: "Read",
		Decision: "redirect", SuggestedTool: "context",
	}); err != nil {
		t.Fatalf("log 1: %v", err)
	}
	if err := store.LogHookInvocation(db.HookInvocation{
		TS: now + 1, SessionID: "sess1", ToolName: "Grep",
		Decision: "redirect", SuggestedTool: "search",
	}); err != nil {
		t.Fatalf("log 2: %v", err)
	}
	if _, err := store.ResolveHookInvocationsForSession("sess1", []db.HookSessionCall{
		{TS: now + 10, ToolName: "context"},
		{TS: now + 11, ToolName: "Read"}, // not search
		{TS: now + 12, ToolName: "Edit"},
	}); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/hook-stats", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := string(readBody(t, rr))
	if !strings.Contains(body, `"redirects":2`) {
		t.Errorf("expected redirects=2; got %s", body)
	}
	if !strings.Contains(body, `"taken":1`) {
		t.Errorf("expected taken=1; got %s", body)
	}
	// #629: triangulating panels. One redirect suggested context (taken),
	// one suggested search (rejected — search not in next 3 calls).
	// resolved=2, overrides=1 → override_pct=50.
	if !strings.Contains(body, `"resolved":2`) {
		t.Errorf("expected resolved=2; got %s", body)
	}
	if !strings.Contains(body, `"overrides":1`) {
		t.Errorf("expected overrides=1; got %s", body)
	}
	// by_tool should report Read+Grep with one redirect each.
	if !strings.Contains(body, `"Read"`) || !strings.Contains(body, `"Grep"`) {
		t.Errorf("expected by_tool to include Read and Grep; got %s", body)
	}
}

func TestHTTP_HookStats_PostReturns405(t *testing.T) {
	t.Parallel()
	// /v1/hook-stats is GET-only per #609 + the v0.37 hookGetOnlyRoutes
	// addition. POST returns 405 with Allow: GET, HEAD.
	srv, _, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/hook-stats", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /v1/hook-stats status = %d, want 405; body=%s", rr.Code, readBody(t, rr))
	}
	if got := rr.Header().Get("Allow"); got != "GET, HEAD" {
		t.Errorf("Allow header = %q, want GET, HEAD", got)
	}
}
