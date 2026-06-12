// SPDX-License-Identifier: MIT

package server

import (
	"strings"
	"testing"
)

// Schema diet (#2003) — toolset-mode surface contracts + lean-transform
// unit tests. The weight numbers themselves are pinned in
// schema_weight_test.go; the FULL/rich schema bytes are pinned by the
// tool-contract golden (which sets the env explicitly — the contract
// documents the complete surface, independent of the core/lean default
// shipped since v1.6, #2005).

func TestParseToolsetEnv(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"":      toolsetFull, // #2054 default flip back to full (core omitted bootstrap-essential tools)
		"full":  toolsetFull,
		" FULL": toolsetFull,
		"core":  toolsetCore, // explicit opt-in for token-tight setups
		" CORE": toolsetCore,
		"coree": toolsetFull, // unknown values land on the default (full), never a third state
		"lean":  toolsetFull,
	}
	for in, want := range cases {
		if got := parseToolsetEnv(in); got != want {
			t.Errorf("parseToolsetEnv(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseSchemaStyleEnv(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"":      schemaStyleLean, // v1.6 default flip (#2005)
		"rich":  schemaStyleRich,
		"RICH ": schemaStyleRich,
		"lean":  schemaStyleLean,
		"LEAN ": schemaStyleLean,
		"short": schemaStyleLean, // PINCHER_TOOL_DESCRIPTIONS=short is a different knob; unknowns land on the default
	}
	for in, want := range cases {
		if got := parseSchemaStyleEnv(in); got != want {
			t.Errorf("parseSchemaStyleEnv(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFirstSentence(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		// Markdown-bold terminator: '.' inside '**...**' followed by space.
		{"**Call once per project before using any other tool.** Indexes a repository.",
			"**Call once per project before using any other tool.**"},
		// Plain sentence.
		{"Project name or ID. Defaults to session project.", "Project name or ID."},
		// Decimals are not boundaries.
		{"Minimum extraction_confidence (0.0-1.0). Default 0.0 (no filter).",
			"Minimum extraction_confidence (0.0-1.0)."},
		// Abbreviations are not boundaries.
		{"Filter hops, e.g. CALLS only. Second sentence.", "Filter hops, e.g. CALLS only."},
		// File names are not boundaries ('.' followed by non-space).
		{"See docs/reference/tools.md for details. More.", "See docs/reference/tools.md for details."},
		// No terminator: returned whole.
		{"Filter by language: Go|Python|TypeScript", "Filter by language: Go|Python|TypeScript"},
	}
	for _, c := range cases {
		if got := firstSentence(c.in); got != c.want {
			t.Errorf("firstSentence(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

func TestLeanArgDescription_Cap(t *testing.T) {
	t.Parallel()
	long := "Filter by symbol kind which can be any of a very long colon-separated enumeration that has no sentence terminator at all " + strings.Repeat("x ", 40)
	got := leanArgDescription(long)
	if len(got) > leanArgDescMax+4 { // +4 for the " …" marker
		t.Errorf("leanArgDescription did not cap: %d chars: %q", len(got), got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("capped description should end with truncation marker, got %q", got)
	}
	short := "Project name or ID."
	if got := leanArgDescription(short + " Defaults to session project."); got != short {
		t.Errorf("leanArgDescription = %q, want %q", got, short)
	}
}

// TestToolset_CoreSurface pins the core-mode contract: tools/list
// advertises exactly coreToolset, while s.tools and s.handlers keep the
// FULL surface — every tool stays reachable over HTTP /v1/<tool> and as
// a `batch` sub-query, and the tool-contract golden (which reads
// s.tools) is mode-independent.
func TestToolset_CoreSurface(t *testing.T) {
	t.Setenv("PINCHER_TOOLSET", "core")
	srv, _, _ := newTestServer(t)

	for name := range srv.mcpVisible {
		if !coreToolset[name] {
			t.Errorf("core mode advertises %q over MCP — not in coreToolset", name)
		}
	}
	for name := range coreToolset {
		if !srv.mcpVisible[name] {
			t.Errorf("core tool %q not advertised over MCP in core mode", name)
		}
	}
	// Full registration preserved underneath.
	for name := range expectedMCPTools {
		if _, ok := srv.handlers[name]; !ok {
			t.Errorf("core mode dropped %q from s.handlers — HTTP /v1/%s would 404", name, name)
		}
		if _, ok := srv.tools[name]; !ok {
			t.Errorf("core mode dropped %q from s.tools — OpenAPI/contract surface broken", name)
		}
	}
}

// TestToolset_FullOptOut pins the opt-out: PINCHER_TOOLSET=full
// restores the pre-v1.6 behavior — every registered tool MCP-visible.
// (The core default itself is pinned by TestToolContract_DefaultSurface.)
//
// Exception (router-loop B5, plan §A6): the routerConditionalTools key
// their advertisement off router DETECTION, not the toolset knob —
// absent ⇒ zero MCP surface even under full. Their dual-state
// advertisement is pinned by TestRouterTools_AdvertisementMatrix and
// TestToolContract_RouterAbsent_FullToolset_ZeroSurface.
func TestToolset_FullOptOut(t *testing.T) {
	t.Setenv("PINCHER_TOOLSET", "full")
	t.Setenv("PINCHER_ROUTER", "off")
	srv, _, _ := newTestServer(t)
	for name := range expectedMCPTools {
		if routerConditionalTools[name] {
			continue // detection-gated, not toolset-gated (plan §A6)
		}
		if !srv.mcpVisible[name] {
			t.Errorf("full mode does not advertise %q over MCP", name)
		}
	}
}

// TestToolset_CoreIsSubsetOfContract keeps coreToolset honest against
// the MCP surface contract: a core tool that isn't in expectedMCPTools
// is a typo.
func TestToolset_CoreIsSubsetOfContract(t *testing.T) {
	t.Parallel()
	for name := range coreToolset {
		if !expectedMCPTools[name] {
			t.Errorf("coreToolset[%q] is not in expectedMCPTools — typo?", name)
		}
	}
}

// TestGuide_CoreMode_NamesEscapeHatch: guide stays on the core surface
// because it routes to everything else — when it recommends a tool that
// is registered but not advertised in core mode, the recommendation
// must say so and name the escape hatches.
func TestGuide_CoreMode_NamesEscapeHatch(t *testing.T) {
	t.Setenv("PINCHER_TOOLSET", "core")
	srv, _, _ := newTestServer(t)

	_, _, recs, _ := srv.computeGuide("onboard me on internal/ast", "")
	if len(recs) == 0 {
		t.Fatal("computeGuide returned no recommendations")
	}
	sawNonCore := false
	for _, r := range recs {
		name := r["tool"]
		if name == "" || coreToolset[name] {
			continue
		}
		sawNonCore = true
		if !strings.Contains(r["why"], "PINCHER_TOOLSET=full") || !strings.Contains(r["why"], "/v1/"+name) {
			t.Errorf("core-mode guide rec for non-core tool %q lacks escape-hatch note: %q", name, r["why"])
		}
	}
	if !sawNonCore {
		t.Fatal("test premise broken: onboarding task no longer recommends any non-core tool — pick another task shape")
	}

	// Full mode: no note injected.
	t.Setenv("PINCHER_TOOLSET", "full")
	srvFull, _, _ := newTestServer(t)
	_, _, recsFull, _ := srvFull.computeGuide("onboard me on internal/ast", "")
	for _, r := range recsFull {
		if strings.Contains(r["why"], "PINCHER_TOOLSET=full") {
			t.Errorf("full-mode guide rec carries core-mode note: %q", r["why"])
		}
	}
}
