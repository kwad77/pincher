// SPDX-License-Identifier: MIT

package server

import (
	"strings"
	"testing"
)

// v0.29 contract tests for the interactive-polish batch
// (#540/#541/#542/#543/#552/#553). All six touch dashboard JS/CSS.

func TestDashboardJS_EmptyStateCTA(t *testing.T) {
	t.Parallel()
	js := renderDashboardJS("")
	for _, needle := range []string{
		"function emptyStateCTA",
		`'projects'`, `'sessions'`, `'adrs'`,
		"empty-cta-title",
	} {
		if !strings.Contains(js, needle) {
			t.Errorf("dashboard JS missing %q — #540 empty-state CTA incomplete", needle)
		}
	}
	// Must be wired into both the projects and sessions empty paths.
	for _, needle := range []string{
		"emptyStateCTA('projects')",
		"emptyStateCTA('sessions')",
	} {
		if !strings.Contains(js, needle) {
			t.Errorf("dashboard JS missing %q — #540 wiring missing in a tab", needle)
		}
	}
	css := renderDashboardCSS()
	if !strings.Contains(css, ".empty-cta") {
		t.Errorf("dashboard CSS missing .empty-cta — #540 styling missing")
	}
}

func TestDashboardJS_LoadingSkeletons(t *testing.T) {
	t.Parallel()
	js := renderDashboardJS("")
	if !strings.Contains(js, "function skeletonRows") {
		t.Errorf("dashboard JS missing skeletonRows helper — #541")
	}
	for _, needle := range []string{
		`skeletonRows(8, 'line')`,
		`skeletonRows(4, 'card')`,
	} {
		if !strings.Contains(js, needle) {
			t.Errorf("dashboard JS missing %q — #541 skeleton wiring missing", needle)
		}
	}
	css := renderDashboardCSS()
	for _, needle := range []string{".sk-line", ".sk-card", "@keyframes sk-pulse"} {
		if !strings.Contains(css, needle) {
			t.Errorf("dashboard CSS missing %q — #541 skeleton styles missing", needle)
		}
	}
}

func TestDashboardJS_ToastVariants(t *testing.T) {
	t.Parallel()
	js := renderDashboardJS("")
	// showToast must support both legacy (msg, ok) and new (msg, kind, opts) forms.
	for _, needle := range []string{
		"showToast(msg, kindOrOk='success', opts={})",
		"aria-live",
		`'error'`, `'info'`,
	} {
		if !strings.Contains(js, needle) {
			t.Errorf("dashboard JS missing %q — #542 toast variants incomplete", needle)
		}
	}
}

func TestDashboardJS_CustomConfirmDialog(t *testing.T) {
	t.Parallel()
	js := renderDashboardJS("")
	for _, needle := range []string{
		"function showConfirmDialog",
		`role', 'dialog'`,
		`aria-modal`,
		"confirm-modal-title",
		"destructive",
	} {
		if !strings.Contains(js, needle) {
			t.Errorf("dashboard JS missing %q — #543 confirm dialog incomplete", needle)
		}
	}
	// Native confirm() calls must be GONE from the action sites.
	if strings.Contains(js, `if(!confirm(`) || strings.Contains(js, `if (!confirm(`) {
		t.Errorf("dashboard JS still contains native confirm() calls — #543 not fully migrated")
	}
	css := renderDashboardCSS()
	if !strings.Contains(css, ".confirm-modal") {
		t.Errorf("dashboard CSS missing .confirm-modal — #543 dialog is unstyled")
	}
}

func TestDashboardJS_ConfigurableRefresh(t *testing.T) {
	t.Parallel()
	js := renderDashboardJS("")
	for _, needle := range []string{
		"REFRESH_STORAGE",
		"function applyRefreshInterval",
		"function onRefreshIntervalChange",
		// Re-uses v0.28's _pollers (visibility-aware) instead of bare
		// setInterval timers, so the off setting + tab-pause stay
		// consistent.
		"_pollers.length = 0",
	} {
		if !strings.Contains(js, needle) {
			t.Errorf("dashboard JS missing %q — #552 configurable refresh incomplete", needle)
		}
	}
	html := renderDashboard("")
	if !strings.Contains(html, `id="refresh-select"`) {
		t.Errorf("header missing #refresh-select — #552 control not present")
	}
	// "off" option must be present.
	if !strings.Contains(html, `value="0">Refresh: off`) {
		t.Errorf("refresh select missing 'off' option — #552 acceptance not met")
	}
}

func TestDashboardJS_ADRRichRender(t *testing.T) {
	t.Parallel()
	js := renderDashboardJS("")
	// ADR value must be wrapped in <pre> with class adr-val (was <div>).
	if !strings.Contains(js, `'<pre class="adr-val">'+esc(e.value||'')+'</pre>'`) {
		t.Errorf("dashboard JS doesn't render ADR value as <pre class='adr-val'> — #553")
	}
	css := renderDashboardCSS()
	if !strings.Contains(css, ".adr-val") || !strings.Contains(css, "white-space:pre-wrap") {
		t.Errorf("dashboard CSS missing .adr-val with pre-wrap — #553 styling missing")
	}
}
