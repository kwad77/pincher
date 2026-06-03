// SPDX-License-Identifier: MIT

package server

import (
	"strings"
	"testing"
)

// v0.27 contract tests for the dashboard search-polish batch
// (#547/#548/#555/#533). All four issues touch dashboard JS only;
// without a JS runtime the test gates inspect the rendered template
// to verify the wiring is present. Snapshot+wiring is the closest
// non-runtime guarantee.

// #547: search input must debounce input events. The pre-fix bug was
// every keystroke firing a fetch; the fix wraps doSearch in a 200ms
// debounce + binds it via data-action-input on the search-q element.
func TestDashboardJS_SearchDebounce(t *testing.T) {
	t.Parallel()
	js := renderDashboardJS("")
	html := renderDashboard("")

	for _, needle := range []string{
		"function debounce",
		"const debouncedSearch",
		"setTimeout",
		"clearTimeout",
	} {
		if !strings.Contains(js, needle) {
			t.Errorf("dashboard JS missing %q — #547 debounce wiring incomplete", needle)
		}
	}

	// The search input must use data-action-input="debouncedSearch"
	// so the existing global input-delegation handler routes through
	// the debounced wrapper instead of the raw doSearch.
	if !strings.Contains(html, `data-action-input="debouncedSearch"`) {
		t.Errorf("search input missing data-action-input=debouncedSearch — typing won't trigger search (#547)")
	}
}

// #548: snippet results must include match-term highlights via <mark>.
// The highlightSnippet helper escapes the snippet first, then wraps
// matches — so the HTML is XSS-safe and the highlighting is visible.
func TestDashboardJS_SnippetHighlight(t *testing.T) {
	t.Parallel()
	js := renderDashboardJS("")

	for _, needle := range []string{
		"function highlightSnippet",
		"<mark>",
		// XSS guard: the function must call esc() BEFORE wrapping,
		// not after — otherwise an attacker-controlled snippet could
		// inject HTML that survives the highlight pass.
		"const escaped = esc(snippet || '')",
	} {
		if !strings.Contains(js, needle) {
			t.Errorf("dashboard JS missing %q — #548 snippet highlight incomplete", needle)
		}
	}

	// CSS for <mark> must be present or the highlights are invisible.
	css := renderDashboardCSS()
	if !strings.Contains(css, ".result-snippet mark") {
		t.Errorf("dashboard CSS missing .result-snippet mark — #548 highlight is invisible")
	}
}

// #555: sparkline must render a tooltip on mouseover. Wiring includes
// the data cache (_sparklineData), a mousemove handler that maps
// cursor x→nearest data point, and CSS for the floating tip.
func TestDashboardJS_SparklineTooltip(t *testing.T) {
	t.Parallel()
	js := renderDashboardJS("")

	for _, needle := range []string{
		"_sparklineData",
		"function _attachSparklineTooltip",
		"sparkline-tip",
		"svg.onmousemove",
		// Touch support per the issue's acceptance.
		"svg.ontouchstart",
	} {
		if !strings.Contains(js, needle) {
			t.Errorf("dashboard JS missing %q — #555 sparkline tooltip incomplete", needle)
		}
	}

	css := renderDashboardCSS()
	if !strings.Contains(css, ".sparkline-tip") {
		t.Errorf("dashboard CSS missing .sparkline-tip — #555 tooltip is invisible")
	}
}

// #533: architecture detail panel must show "X of Y" + a Show all
// toggle when entry-points or hotspots exceed the default cap (8/10).
func TestDashboardJS_ArchitectureShowAll(t *testing.T) {
	t.Parallel()
	js := renderDashboardJS("")

	for _, needle := range []string{
		"const renderTruncatable",
		"function toggleDetailExpanded",
		"const _detailExpanded",
		"detail-section-count",
		"show-all-btn",
		// The expanded cap should be 50 per acceptance.
		`8, 50, 'eps_'+id`,
		`10, 50, 'hotspots_'+id`,
	} {
		if !strings.Contains(js, needle) {
			t.Errorf("dashboard JS missing %q — #533 show-all wiring incomplete", needle)
		}
	}

	css := renderDashboardCSS()
	if !strings.Contains(css, ".show-all-btn") {
		t.Errorf("dashboard CSS missing .show-all-btn — #533 toggle button is unstyled")
	}
}
