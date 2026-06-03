// SPDX-License-Identifier: MIT

package server

import (
	"strings"
	"testing"
)

// #538 + #539 (umbrella #519): the dashboard JS must (a) abort
// in-flight fetches when the user switches tabs and (b) surface
// fetch failures in the tab body so the per-tab "loading…" never
// stays stuck on error. Without a JS runtime in the test harness,
// the next-best gate is byte-level inspection of the rendered JS:
// if the wiring isn't there, the contract was broken at template
// edit time.
//
// These tests don't prove the JS WORKS — that needs a browser —
// but they prove the wiring is PRESENT, which is what previous
// regressions of this class would have failed.

func TestDashboardJS_HasAbortControllerWiring(t *testing.T) {
	t.Parallel()
	js := renderDashboardJS("")

	// #539: tab fetch wrapper that registers AbortController.
	for _, needle := range []string{
		"_tabControllers",
		"AbortController",
		"function tabFetch",
		"_abortTab",
	} {
		if !strings.Contains(js, needle) {
			t.Errorf("dashboard JS missing %q — #539 abort wiring incomplete", needle)
		}
	}

	// #539: showTab must abort other tabs' controllers on switch.
	if !strings.Contains(js, "Object.keys(_tabControllers).forEach") {
		t.Errorf("showTab doesn't iterate _tabControllers to abort on switch (#539)")
	}
}

func TestDashboardJS_HasPerTabErrorState(t *testing.T) {
	t.Parallel()
	js := renderDashboardJS("")

	// #538: setTabError + extractErrMsg must be present and used.
	for _, needle := range []string{
		"function setTabError",
		"async function extractErrMsg",
	} {
		if !strings.Contains(js, needle) {
			t.Errorf("dashboard JS missing %q — #538 per-tab error state incomplete", needle)
		}
	}

	// At least one load*() must be calling setTabError on failure.
	// (Spot-check the three most-used tabs.)
	for _, callsite := range []string{
		"setTabError('projects-grid'",
		"setTabError('sessions-table-wrap'",
		"setTabError('adr-list'",
	} {
		if !strings.Contains(js, callsite) {
			t.Errorf("dashboard JS missing %q — #538 wiring missing on a tab", callsite)
		}
	}

	// #538: extractErrMsg must read body.error.message (the v0.25
	// envelope shape) AND fall back to body.error as string for the
	// pre-v0.25 transitional shape — so a partial-rollout proxy still
	// yields a useful message.
	if !strings.Contains(js, "body.error.message") {
		t.Errorf("extractErrMsg doesn't read body.error.message — v0.25 envelope shape (#537) not honored")
	}
}

// #539: rapid tab switching MUST NOT race late-arriving responses
// onto the wrong tab. The contract is: any catch-block in load*()
// short-circuits on AbortError before writing to the DOM.
func TestDashboardJS_AbortErrorDiscardedQuietly(t *testing.T) {
	t.Parallel()
	js := renderDashboardJS("")

	// At least three load*() catch handlers must check for AbortError.
	// Tolerate both "===" and "== " (with spaces) styles since both are
	// valid JS and prettier-style quirks shouldn't break the gate.
	count := strings.Count(js, "AbortError") - 1 // minus the comment in tabFetch
	if count < 3 {
		t.Errorf("dashboard JS only checks AbortError in %d places, want ≥3 (one per refactored loader)\n"+
			"This means at least one tab loader doesn't quietly discard aborted responses, "+
			"so a rapid tab-switch can overwrite the next tab's content with stale data.", count)
	}
}
