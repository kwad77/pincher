// SPDX-License-Identifier: MIT

package server

import (
	"strings"
	"testing"
)

// v0.28 contract tests for the auto-refresh polish batch
// (#544/#545/#546/#549). All four touch dashboard JS/CSS; tests
// inspect the rendered template to verify wiring is present.

// #544: projection banner guards against insufficient data.
// computeProjection extracted as a pure function returning null
// when there aren't enough sessions/days to project.
func TestDashboardJS_ProjectionGuard(t *testing.T) {
	t.Parallel()
	js := renderDashboardJS("")
	for _, needle := range []string{
		"function computeProjection",
		"PROJECTION_MIN_DAYS",
		"PROJECTION_MAX_TOKENS_PER_MONTH",
		"needsMoreData",
		// NaN/Infinity guard.
		"!isFinite(dailyTokens)",
		// 7-day floor per acceptance.
		"PROJECTION_MIN_DAYS = 7",
	} {
		if !strings.Contains(js, needle) {
			t.Errorf("dashboard JS missing %q — #544 projection guard incomplete", needle)
		}
	}
}

// #545 + #546: pollManager wraps setInterval with last-fetch tracking
// + visibility-aware pause/resume. Replaces the bare setInterval
// calls.
func TestDashboardJS_PollManagerAndStaleness(t *testing.T) {
	t.Parallel()
	js := renderDashboardJS("")
	for _, needle := range []string{
		"function pollManager",
		"_lastRefresh",
		"_pollers",
		"visibilitychange",
		"pollManager('overview', load",
		"pollManager('projection', loadProjection",
		// #545: staleness indicator render loop.
		"function _renderStaleness",
		".updated-ago",
		"data-source",
	} {
		if !strings.Contains(js, needle) {
			t.Errorf("dashboard JS missing %q — #545/#546 polling+staleness incomplete", needle)
		}
	}

	// Bare setInterval(load, 30000) and setInterval(loadProjection, 60000)
	// must have been replaced by pollManager — the bare calls would
	// short-circuit visibility pause.
	if strings.Contains(js, "setInterval(load,") {
		t.Errorf("found bare setInterval(load,) — #546 visibility wrapper bypassed")
	}
	if strings.Contains(js, "setInterval(loadProjection,") {
		t.Errorf("found bare setInterval(loadProjection,) — #546 visibility wrapper bypassed")
	}

	// Staleness indicator must appear in the header markup.
	html := renderDashboard("", false)
	if !strings.Contains(html, `class="badge badge-muted updated-ago" data-source="overview"`) {
		t.Errorf("header missing .updated-ago indicator for overview source (#545)")
	}
}

// #549: dark/light/auto theme toggle. Auto = no data-theme attr +
// @media (prefers-color-scheme); explicit attr wins.
func TestDashboardJS_ThemeToggle(t *testing.T) {
	t.Parallel()
	js := renderDashboardJS("")
	for _, needle := range []string{
		"function applyStoredTheme",
		"function cycleTheme",
		"THEME_STORAGE",
		`localStorage.removeItem(THEME_STORAGE)`,
	} {
		if !strings.Contains(js, needle) {
			t.Errorf("dashboard JS missing %q — #549 theme toggle incomplete", needle)
		}
	}

	html := renderDashboard("", false)
	if !strings.Contains(html, `id="theme-btn"`) {
		t.Errorf("header missing #theme-btn — #549 toggle button not present")
	}
	if !strings.Contains(html, `data-action="cycleTheme"`) {
		t.Errorf("theme button not wired to cycleTheme via data-action (#549)")
	}

	// CSS must include both theme palettes + the system @media query.
	css := renderDashboardCSS()
	for _, needle := range []string{
		`:root[data-theme="light"]`,
		"prefers-color-scheme: light",
	} {
		if !strings.Contains(css, needle) {
			t.Errorf("dashboard CSS missing %q — #549 theme variants incomplete", needle)
		}
	}
}
