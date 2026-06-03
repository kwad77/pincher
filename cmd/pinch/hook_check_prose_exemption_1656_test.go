// SPDX-License-Identifier: MIT

package main

import (
	"path/filepath"
	"testing"
)

// #1656 v0.86: hook narrowing. Three changes pinned here:
//   1. isProseFile matrix — Markdown / RST / .planning/ / docs/ all pass through.
//   2. decideReadHook passes prose files through even when large + indexed.
//   3. Grep redirect is advisory (Continue=true + redirect_advisory).
//
// Why: hook visibly fires on every Read/Grep regardless of whether
// the redirect would actually help. Prose / planning files are the
// dominant false-positive class — `context lite=true` returns nothing
// useful on Markdown. Plus the size floor (4 KB) caught too much; the
// new floor (16 KB) keeps the noise level down.

func TestIsProseFile_Matrix_1656(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path string
		want bool
		why  string
	}{
		// Extensions
		{"README.md", true, "Markdown root"},
		{"CHANGELOG.md", true, "Markdown CHANGELOG"},
		{"notes.markdown", true, "long-form Markdown extension"},
		{"page.mdx", true, "MDX is Markdown"},
		{"intro.rst", true, "ReStructuredText"},
		{"notes.txt", true, "plain text"},
		{"manual.adoc", true, "AsciiDoc"},
		{"manual.asciidoc", true, "AsciiDoc long form"},

		// Directory-segment matches
		{".planning/v0.86.md", true, ".planning/ directory"},
		{".planning-roadmap-to-v1.md", true, ".planning- prefix (gitignored)"},
		{"docs/index.html", true, "docs/ directory — even HTML there is prose-context"},
		{"doc/api.txt", true, "doc/ singular variant"},
		{"notes/session-2026.md", true, "notes/ directory"},
		{"note/personal.md", true, "note/ singular variant"},
		{"src/docs/api.go", true, "docs/ anywhere in the path"},

		// Code that shouldn't be prose-exempt
		{"main.go", false, "Go source"},
		{"server.py", false, "Python source"},
		{"internal/server/server.go", false, "nested Go source"},
		{"README", false, "README without extension — assume non-prose"},

		// Edge cases
		{"", false, "empty path"},
		{"src/Containers.md", true, "extension wins regardless of parent dir"},
		{"src/contests/leaderboard.go", false, "`contests/` must not match `docs/`"},

		// Windows separators normalize
		{`.planning\v0.86.md`, true, "Windows backslash separators"},
	}
	for _, c := range cases {
		if got := isProseFile(c.path); got != c.want {
			t.Errorf("isProseFile(%q) = %v, want %v (%s)", c.path, got, c.want, c.why)
		}
	}
}

// decideReadHook integration: a Markdown file in an indexed project
// passes through as a silent (no systemMessage) decision even when
// large. Mirrors the test-file exemption (#1646) shape — prose
// pass-through must be silent, not advisory, because there's nothing
// useful to advise.
func TestDecideReadHook_ProseFile_PassesThrough_1656(t *testing.T) {
	t.Parallel()
	store := newHookTestStore(t)
	projectDir := t.TempDir()
	relPath := "docs/architecture.md"
	indexLargeFakeFile(t, store, projectDir, relPath, 50_000)

	in := hookCheckInput{
		ToolName: "Read",
		ToolInput: map[string]any{
			"file_path": filepath.Join(projectDir, relPath),
		},
	}
	d := decideReadHook(store, in, false)
	if !d.Continue {
		t.Fatalf("prose file must pass through; got Continue=false: %+v", d)
	}
	if d.Decision != "pass_through" {
		t.Errorf("decision = %q, want pass_through (prose has no useful redirect)", d.Decision)
	}
	if d.SystemMessage != "" {
		t.Errorf("prose pass-through must be silent; got SystemMessage=%q", d.SystemMessage)
	}
}

// Negative — verify isProseFile is NOT a blanket pass-through. A
// non-prose Go file at the same size still produces the advisory
// redirect (#1654 contract). Without this, a regression that always
// returned isProseFile=true would silently disable the hook for code.
func TestDecideReadHook_CodeFile_StillAdvises_1656(t *testing.T) {
	t.Parallel()
	store := newHookTestStore(t)
	projectDir := t.TempDir()
	relPath := "internal/server/server.go"
	indexLargeFakeFile(t, store, projectDir, relPath, 50_000)

	in := hookCheckInput{
		ToolName: "Read",
		ToolInput: map[string]any{
			"file_path": filepath.Join(projectDir, relPath),
		},
	}
	d := decideReadHook(store, in, false)
	if !d.Continue {
		t.Fatalf("advisory mode must always pass through; got Continue=false")
	}
	if d.Decision != "redirect_advisory" {
		t.Errorf("decision = %q, want redirect_advisory (code file still merits the hint)", d.Decision)
	}
}

// #1656 v0.86: 16 KB size floor (raised from 4 KB). A 12 KB indexed
// code file is now under the floor and passes through silently.
func TestDecideReadHook_BelowNewSizeFloor_PassesThrough_1656(t *testing.T) {
	t.Parallel()
	store := newHookTestStore(t)
	projectDir := t.TempDir()
	relPath := "internal/server/small.go"
	indexLargeFakeFile(t, store, projectDir, relPath, 12_000) // 12 KB < 16 KB floor

	in := hookCheckInput{
		ToolName: "Read",
		ToolInput: map[string]any{
			"file_path": filepath.Join(projectDir, relPath),
		},
	}
	d := decideReadHook(store, in, false)
	if !d.Continue {
		t.Fatalf("12 KB file is below 16 KB floor; must pass through silently")
	}
	if d.Decision != "pass_through" {
		t.Errorf("decision = %q, want pass_through", d.Decision)
	}
	if d.SystemMessage != "" {
		t.Errorf("below-floor pass-through must be silent; got SystemMessage=%q", d.SystemMessage)
	}
}

// Grep advisory contract — sibling of the Read contract test in
// hook_check_test.go. Pins the #1656 conversion: continue=true +
// redirect_advisory + suggestion still in SystemMessage.
func TestDecideGrepHook_AdvisoryMode_1656(t *testing.T) {
	t.Parallel()
	store := newHookTestStore(t)
	projectDir := t.TempDir()
	indexLargeFakeFile(t, store, projectDir, "f.go", 50_000)

	in := hookCheckInput{
		ToolName: "Grep",
		ToolInput: map[string]any{
			"pattern": "classifyTaskShape",
		},
	}
	d := decideHook(store, in, false)
	if !d.Continue {
		t.Fatalf("Grep advisory mode must pass through; got Continue=false: %+v", d)
	}
	if d.Decision != "redirect_advisory" {
		t.Errorf("decision = %q, want redirect_advisory", d.Decision)
	}
	if d.SuggestedTool != "search" {
		t.Errorf("suggested tool = %q, want search", d.SuggestedTool)
	}
	if d.SystemMessage == "" {
		t.Errorf("advisory Grep must carry a systemMessage hint")
	}
	if d.StopReason != "" {
		t.Errorf("advisory mode must not set StopReason; got %q", d.StopReason)
	}
}
