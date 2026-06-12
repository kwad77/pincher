// SPDX-License-Identifier: MIT

package main

import (
	"strings"
	"testing"

	"github.com/kwad77/pincher/internal/server"
)

// #2011: the Glob advisory must resolve its recommendation against the
// active toolset. Under the v1.6 core default, onboard_module is not on
// the session's tools/list (tools/call → -32602), so the advisory must
// recommend a tool the agent can actually call (`search`, advertised in
// both modes) and name the escape hatches. Under PINCHER_TOOLSET=full
// the original onboard_module recommendation is unchanged.

func TestDecideHook_Glob_CoreToolset_RecommendationIsCallable(t *testing.T) {
	t.Setenv("PINCHER_TOOLSET", "core")
	store := newHookTestStore(t)
	projectDir := t.TempDir()
	indexLargeFakeFile(t, store, projectDir, "internal/server/server.go", 50000)

	in := hookCheckInput{
		ToolName: "Glob",
		ToolInput: map[string]any{
			"pattern": "**/*.go",
			"path":    projectDir,
		},
	}
	d := decideHook(store, in, false)
	if !d.Continue || d.Decision != "redirect_advisory" {
		t.Fatalf("want non-blocking redirect_advisory; got %+v", d)
	}
	// The recommendation must be callable in the session that received
	// it — i.e. advertised under the same (core) toolset.
	if !server.ToolAdvertised(d.SuggestedTool) {
		t.Errorf("core-mode advisory recommends %q, which is NOT advertised under the core toolset (#2011 regression)", d.SuggestedTool)
	}
	if d.SuggestedTool != "search" {
		t.Errorf("suggested tool = %q, want search", d.SuggestedTool)
	}
	// The escape hatches to the richer tool must be named so the agent
	// can still reach onboard_module deliberately.
	if !strings.Contains(d.SystemMessage, "PINCHER_TOOLSET=full") {
		t.Errorf("core-mode advisory should name the PINCHER_TOOLSET=full escape hatch; got %q", d.SystemMessage)
	}
	if !strings.Contains(d.SystemMessage, "batch") {
		t.Errorf("core-mode advisory should mention batch sub-query routing; got %q", d.SystemMessage)
	}
}

func TestDecideHook_Glob_UnsetToolset_DefaultsToFullRecommendation(t *testing.T) {
	// Unset == full default since #2054: onboard_module is advertised, so
	// the advisory recommends it directly (same as an explicit "full").
	t.Setenv("PINCHER_TOOLSET", "")
	store := newHookTestStore(t)
	projectDir := t.TempDir()
	indexLargeFakeFile(t, store, projectDir, "internal/server/server.go", 50000)

	in := hookCheckInput{
		ToolName: "Glob",
		ToolInput: map[string]any{
			"pattern": "**/*.go",
			"path":    projectDir,
		},
	}
	d := decideHook(store, in, false)
	if d.SuggestedTool != "onboard_module" {
		t.Errorf("unset toolset must resolve to the full default; suggested tool = %q, want onboard_module", d.SuggestedTool)
	}
}

func TestDecideHook_Glob_FullToolset_OnboardModuleUnchanged(t *testing.T) {
	t.Setenv("PINCHER_TOOLSET", "full")
	store := newHookTestStore(t)
	projectDir := t.TempDir()
	indexLargeFakeFile(t, store, projectDir, "internal/server/server.go", 50000)

	in := hookCheckInput{
		ToolName: "Glob",
		ToolInput: map[string]any{
			"pattern": "**/*.go",
			"path":    projectDir,
		},
	}
	d := decideHook(store, in, false)
	if !d.Continue || d.Decision != "redirect_advisory" {
		t.Fatalf("want non-blocking redirect_advisory; got %+v", d)
	}
	if d.SuggestedTool != "onboard_module" {
		t.Errorf("full toolset advertises onboard_module; suggested tool = %q, want onboard_module", d.SuggestedTool)
	}
	if !strings.Contains(d.SuggestedArgs, `"directory"`) {
		t.Errorf("full-mode suggested args should carry the directory; got %s", d.SuggestedArgs)
	}
	if !strings.Contains(d.SystemMessage, "onboard_module") {
		t.Errorf("full-mode system message should name the suggested tool; got %q", d.SystemMessage)
	}
}

func TestDecideHook_Glob_ExplicitCoreToolset_FallsBackToCoreSafe(t *testing.T) {
	// parseToolsetEnv's rule (#2054): only the explicit canonical "core"
	// narrows the surface; the advisory must follow the same rule and
	// recommend the core-safe `search` when (and only when) core is set.
	t.Setenv("PINCHER_TOOLSET", "core")
	store := newHookTestStore(t)
	projectDir := t.TempDir()
	indexLargeFakeFile(t, store, projectDir, "internal/server/server.go", 50000)

	in := hookCheckInput{
		ToolName: "Glob",
		ToolInput: map[string]any{
			"pattern": "**/*.go",
			"path":    projectDir,
		},
	}
	d := decideHook(store, in, false)
	if d.SuggestedTool != "search" {
		t.Errorf("explicit core toolset must use the core-safe recommendation; suggested tool = %q, want search", d.SuggestedTool)
	}
}

func TestDecideHook_Glob_TypoToolset_FallsBackToFullDefault(t *testing.T) {
	// parseToolsetEnv's rule (#2054): typos land on the full default, so
	// the advisory recommends onboard_module — never a third state.
	t.Setenv("PINCHER_TOOLSET", "fulll")
	store := newHookTestStore(t)
	projectDir := t.TempDir()
	indexLargeFakeFile(t, store, projectDir, "internal/server/server.go", 50000)

	in := hookCheckInput{
		ToolName: "Glob",
		ToolInput: map[string]any{
			"pattern": "**/*.go",
			"path":    projectDir,
		},
	}
	d := decideHook(store, in, false)
	if d.SuggestedTool != "onboard_module" {
		t.Errorf("typo'd toolset must fall back to the full default; suggested tool = %q, want onboard_module", d.SuggestedTool)
	}
}
