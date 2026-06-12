// SPDX-License-Identifier: MIT

package server

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// Router-loop item B12: the dashboard Models tab is router-gated under
// the same conditional-surface discipline as the models/route MCP
// advertisement (plan §A6) — present when the startup detection ladder
// found a live pincher-router, byte-for-byte ABSENT otherwise. These
// tests pin the dual state plus the read-only governance line (the
// pane renders no registry-mutation controls; the router owns all
// registry state and pincher never writes workers.yaml).
//
// The byte-level absent-state guarantee is carried by
// TestDashboardHTMLSnapshot: the no-basepath/with-basepath fixtures
// are pinned with PINCHER_ROUTER=off and were not regenerated for B12.

func dashboardHTML(t *testing.T, routerMode string) string {
	t.Helper()
	t.Setenv("PINCHER_ROUTER", routerMode)
	srv, _, _ := newTestServer(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1/dashboard", nil)
	srv.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("GET /v1/dashboard: status %d, want 200", w.Code)
	}
	return w.Body.String()
}

// TestDashboardModelsTab_AbsentWithoutRouter: zero surface when no
// router is detected — no tab button, no pane, and no leftover
// substitution tokens.
func TestDashboardModelsTab_AbsentWithoutRouter(t *testing.T) {
	html := dashboardHTML(t, "off")
	for _, banned := range []string{
		`data-args='["models"]'`, // the tab button
		`id="tab-models"`,        // the pane
		"__PINCHER_ROUTER_TAB__", // unsubstituted tokens must never leak
		"__PINCHER_ROUTER_PANE__",
	} {
		if strings.Contains(html, banned) {
			t.Errorf("router-absent dashboard HTML contains %q — the Models tab must have zero surface when no router is detected (plan §A6)", banned)
		}
	}
}

// TestDashboardModelsTab_PresentWithRouter: PINCHER_ROUTER=on (forced
// detection, no network — the item-B4 override) renders the tab button,
// the pane, and the table/handshake mount points the JS fills.
func TestDashboardModelsTab_PresentWithRouter(t *testing.T) {
	html := dashboardHTML(t, "on")
	for _, want := range []string{
		`data-args='["models"]'`,
		">Models<",
		`id="tab-models"`,
		`id="models-handshake"`,
		`id="models-table-wrap"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("router-present dashboard HTML missing %q", want)
		}
	}
	// Substitution hygiene: tokens fully consumed.
	if strings.Contains(html, "__PINCHER_ROUTER_") {
		t.Error("router-present dashboard HTML leaks a __PINCHER_ROUTER_ substitution token")
	}
}

// TestDashboardModelsTab_ReadOnly: the governance line — the pane ships
// ZERO mutation controls. enable/disable/test are reserved actions that
// error at the tool layer; the dashboard must not even render buttons
// for them (paid workers are listed, never enabled, from here).
func TestDashboardModelsTab_ReadOnly(t *testing.T) {
	html := dashboardHTML(t, "on")
	pane := html[strings.Index(html, `id="tab-models"`):]
	if end := strings.Index(pane, "<div class=\"footer\">"); end > 0 {
		pane = pane[:end]
	}
	for _, banned := range []string{
		"data-action=\"enableModel", "data-action=\"disableModel",
		"data-action=\"testModel", "<button", "<input", "<select", "<form",
	} {
		if strings.Contains(pane, banned) {
			t.Errorf("Models pane contains %q — the tab is read-only by design (router owns registry state; mutations error at the tool layer)", banned)
		}
	}
}

// TestDashboardJS_ModelsLoaderWiring: the JS half of the gate. The
// loader exists, is dispatched from showTab, fetches ONLY pincher's own
// /v1/models proxy (never a router port directly — connect-src 'self'
// backs this in CSP), and showTab falls back to overview when the pane
// was not rendered, so a stale #models hash on a router-less server
// triggers no fetch.
func TestDashboardJS_ModelsLoaderWiring(t *testing.T) {
	js := renderDashboardJS("")
	for _, want := range []string{
		"async function loadModels()",
		"if (name === 'models') loadModels();",
		"tabFetch('models', '/v1/models'",
		// The stale-hash guard: missing pane ⇒ overview, no fetch.
		"if (!document.getElementById('tab-'+name)) name = 'overview';",
		// Win-rate placeholder column ships with an honest tooltip.
		"Win-rate",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("dashboard JS missing %q — Models tab wiring (B12)", want)
		}
	}
	// loadModels is the ONLY fetch site for /v1/models: no poller or
	// boot-path fetch may touch it (no fetch when the tab is absent).
	if n := strings.Count(js, "'/v1/models'"); n != 1 {
		t.Errorf("dashboard JS references '/v1/models' %d times, want exactly 1 (inside loadModels) — extra call sites would fetch on router-less servers", n)
	}
}
