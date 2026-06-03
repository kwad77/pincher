// SPDX-License-Identifier: MIT

package ast

import (
	"strings"
	"testing"
)

// mdExtract is a focused helper for Markdown tests.
func mdExtract(t *testing.T, src string) []ExtractedSymbol {
	t.Helper()
	r := Extract([]byte(src), "Markdown", "docs/test.md")
	if r == nil {
		t.Fatal("nil result")
	}
	return r.Symbols
}

// TestExtractMarkdown_HierarchicalQNs pins the dotted-path qualified
// name semantics: H1 is the root, H2 nests under the most recent H1,
// H3 under the most recent H2, etc.
func TestExtractMarkdown_HierarchicalQNs(t *testing.T) {
	syms := mdExtract(t, `# Intro

text

## Getting Started

text

### Installation

steps

## Architecture

text

# Reference

text
`)

	want := []struct{ name, qn string }{
		{"Intro", "intro"},
		{"Getting Started", "intro.getting_started"},
		{"Installation", "intro.getting_started.installation"},
		{"Architecture", "intro.architecture"},
		{"Reference", "reference"},
	}
	if len(syms) != len(want) {
		t.Fatalf("got %d symbols, want %d (%+v)", len(syms), len(want), syms)
	}
	for i, w := range want {
		if syms[i].Name != w.name {
			t.Errorf("symbol %d: name = %q, want %q", i, syms[i].Name, w.name)
		}
		if syms[i].QualifiedName != w.qn {
			t.Errorf("symbol %d: qn = %q, want %q", i, syms[i].QualifiedName, w.qn)
		}
		if syms[i].Kind != "Section" {
			t.Errorf("symbol %d: kind = %q, want Section", i, syms[i].Kind)
		}
	}
}

// TestExtractMarkdown_ByteRangeIncludesSection asserts each section's
// byte range covers its full body — heading line through the line
// before the next same-or-shallower heading. This is the invariant that
// makes `symbol` tool retrieval round-trip cleanly: fetching a Section
// returns the heading + all subordinate content.
func TestExtractMarkdown_ByteRangeIncludesSection(t *testing.T) {
	src := `# A

body of A

## A1

body of A1

# B

body of B
`
	syms := mdExtract(t, src)
	if len(syms) != 3 {
		t.Fatalf("got %d sections, want 3", len(syms))
	}

	// Section A should contain its body AND A1's heading + body.
	a := syms[0]
	got := src[a.StartByte:a.EndByte]
	for _, want := range []string{"# A", "body of A", "## A1", "body of A1"} {
		if !strings.Contains(got, want) {
			t.Errorf("Section A missing %q in byte range:\n%s", want, got)
		}
	}
	// A's range MUST NOT include B (siblings are disjoint).
	if strings.Contains(got, "# B") {
		t.Errorf("Section A leaked into B:\n%s", got)
	}

	// Section A1 should be just A1, not A's body, not B.
	a1 := syms[1]
	got1 := src[a1.StartByte:a1.EndByte]
	if !strings.Contains(got1, "## A1") {
		t.Errorf("Section A1 missing its own heading:\n%s", got1)
	}
	if strings.Contains(got1, "# B") {
		t.Errorf("Section A1 leaked into B:\n%s", got1)
	}
	if strings.Contains(got1, "body of A\n") {
		// "body of A" is a substring of "body of A1" — match the trailing
		// newline to disambiguate.
		t.Errorf("Section A1 leaked into A's body:\n%s", got1)
	}

	// Section B should be just B's content.
	b := syms[2]
	gotB := src[b.StartByte:b.EndByte]
	if !strings.Contains(gotB, "# B") || !strings.Contains(gotB, "body of B") {
		t.Errorf("Section B missing its content:\n%s", gotB)
	}
	if strings.Contains(gotB, "# A") {
		t.Errorf("Section B leaked into A:\n%s", gotB)
	}
}

// TestExtractMarkdown_EmptyDocument — an empty file MUST produce zero
// symbols and not crash.
func TestExtractMarkdown_EmptyDocument(t *testing.T) {
	syms := mdExtract(t, "")
	if len(syms) != 0 {
		t.Errorf("empty doc produced %d symbols, want 0", len(syms))
	}
}

// TestExtractMarkdown_ProseOnly — a file with prose but no headings.
// #1097 update (2026-05-16): pre-#1097 this produced zero symbols
// (prose disappeared from the docs corpus). Post-#1097 it produces
// exactly one synthetic preamble Section covering the whole file so
// the content is searchable. The pre-#1097 contract was wrong — it
// dropped legitimate README / design-doc content from the index.
func TestExtractMarkdown_ProseOnly(t *testing.T) {
	syms := mdExtract(t, "Just some prose with no headings.\n\nAnother paragraph.\n")
	if len(syms) != 1 {
		t.Errorf("prose-only doc produced %d symbols, want 1 (the preamble)", len(syms))
	}
	if len(syms) > 0 && syms[0].QualifiedName != "preamble" {
		t.Errorf("prose-only doc's single symbol QN = %q, want preamble", syms[0].QualifiedName)
	}
}

// TestExtractMarkdown_SlugSafety — heading slugs MUST NOT contain the
// QN separator (`.`) or whitespace. Otherwise `services.web` and
// `services.web` (one a slugged "services. web") would conflict.
func TestExtractMarkdown_SlugSafety(t *testing.T) {
	syms := mdExtract(t, `# Version 1.0

stuff

## API/v1

stuff

### Hello, World!

stuff
`)
	for _, s := range syms {
		// QN can contain `.` as the level separator, but each segment
		// MUST NOT contain spaces or punctuation that'd fragment the
		// path. Walk the segments and check.
		for _, seg := range strings.Split(s.QualifiedName, ".") {
			if strings.ContainsAny(seg, " \t,!?/") {
				t.Errorf("qn segment %q contains unsafe chars (full qn: %q)", seg, s.QualifiedName)
			}
		}
		// Slugged version of "Version 1.0" should collapse the `.` to
		// `_` so it doesn't read as a hierarchy separator.
		if s.Name == "Version 1.0" && s.QualifiedName != "version_1_0" {
			t.Errorf("Version 1.0 slug = %q, want version_1_0", s.QualifiedName)
		}
	}
}

// TestExtractMarkdown_PreservesOriginalTitle — the slug normalises but
// the Name field MUST keep the original heading text for display.
func TestExtractMarkdown_PreservesOriginalTitle(t *testing.T) {
	syms := mdExtract(t, "# Hello, World!\n\nstuff\n")
	if len(syms) != 1 {
		t.Fatalf("got %d symbols, want 1", len(syms))
	}
	if syms[0].Name != "Hello, World!" {
		t.Errorf("Name = %q, want %q (original title preserved)", syms[0].Name, "Hello, World!")
	}
	if syms[0].Signature != "# Hello, World!" {
		t.Errorf("Signature = %q, want %q", syms[0].Signature, "# Hello, World!")
	}
}

// TestExtractMarkdown_HighConfidence confirms goldmark routing produces
// parser-backed (high) confidence — was "exactly 1.0" before #34 Phase 2;
// now asserts >= 0.7 (default min_confidence threshold) since per-symbol
// composition shifts the post-composition score.
func TestExtractMarkdown_HighConfidence(t *testing.T) {
	r := Extract([]byte("# H\n\ntext\n"), "Markdown", "docs/x.md")
	if len(r.Symbols) != 1 {
		t.Fatalf("got %d, want 1", len(r.Symbols))
	}
	if r.Symbols[0].ExtractionConfidence < 0.7 {
		t.Errorf("confidence = %v, want >= 0.7", r.Symbols[0].ExtractionConfidence)
	}
}

// TestExtractMarkdown_SkipLevel — a doc that jumps from H1 to H3
// (skipping H2) must still produce a sensible hierarchy. The H3 nests
// under the most recent shallower heading (the H1).
func TestExtractMarkdown_SkipLevel(t *testing.T) {
	syms := mdExtract(t, "# Top\n\n### Deep\n\ntext\n")
	if len(syms) != 2 {
		t.Fatalf("got %d, want 2", len(syms))
	}
	if syms[1].QualifiedName != "top.deep" {
		t.Errorf("skip-level qn = %q, want top.deep", syms[1].QualifiedName)
	}
}

// TestExtractMarkdown_RegisteredExtensions guards the extension list
// from regression. Adding a new extension = update this test.
func TestExtractMarkdown_RegisteredExtensions(t *testing.T) {
	for _, ext := range []string{".md", ".markdown", ".mdx", ".mdc"} {
		got := DetectLanguage("test" + ext)
		if got != "Markdown" {
			t.Errorf("DetectLanguage(test%s) = %q, want Markdown", ext, got)
		}
	}
}

// TestExtractMarkdown_MdcExtractsLikeMarkdown verifies that .mdc files
// (Cursor rule files — `.cursor/rules/*.mdc`) extract heading symbols
// using the same CommonMark grammar as .md. The .mdc convention is the
// primary motivator for registering the extension.
//
// Note: real Cursor rule files often have YAML frontmatter delimited by
// `---` lines. goldmark interprets `---` between content lines as a
// Setext heading underline (CommonMark grammar), which can produce extra
// Section symbols from frontmatter keys. That's a goldmark/CommonMark
// behaviour, not specific to .mdc — this test focuses on the extension
// registration itself, not frontmatter handling.
func TestExtractMarkdown_MdcExtractsLikeMarkdown(t *testing.T) {
	src := `# Style guide

Body of the rule.

## Naming

Use camelCase.
`
	r := Extract([]byte(src), "Markdown", ".cursor/rules/style.mdc")
	if r == nil {
		t.Fatal("Extract returned nil")
	}
	if len(r.Symbols) != 2 {
		t.Fatalf("got %d symbols, want 2 (Style guide + Naming)", len(r.Symbols))
	}
	got := make(map[string]ExtractedSymbol)
	for _, s := range r.Symbols {
		got[s.QualifiedName] = s
	}
	if _, ok := got["style_guide"]; !ok {
		t.Errorf("missing style_guide symbol; got QNs: %v", mapKeys(got))
	}
	if _, ok := got["style_guide.naming"]; !ok {
		t.Errorf("missing style_guide.naming symbol; got QNs: %v", mapKeys(got))
	}
}

// Headings inside fenced code blocks must NOT become Section symbols.
// CommonMark treats `## ...` inside a triple-backtick block as code
// content, not a heading. Pre-extractor pincher (regex-tier) used to
// over-match; the goldmark parser handles this correctly, but the
// test pins the contract so a future swap of the markdown parser
// can't quietly reintroduce the bug.
func TestExtractMarkdown_HeadingsInCodeBlocksIgnored(t *testing.T) {
	src := []byte("# Top\n\n## Real Heading\n\n```\n## NOT a heading (in code block)\n```\n\nMore prose.\n")
	r := Extract(src, "Markdown", "code-block.md")
	if r == nil {
		t.Fatal("Extract returned nil")
	}
	if len(r.Symbols) != 2 {
		t.Errorf("expected 2 sections (Top + Real Heading), got %d", len(r.Symbols))
	}
	for _, s := range r.Symbols {
		if s.Name == "NOT a heading (in code block)" {
			t.Errorf("heading inside code block must not become a Section symbol: %s", s.QualifiedName)
		}
	}
}

// Setext-style headings (underlined with === or ---) are valid
// CommonMark equivalents to # / ##. Pin them as a regression guard —
// pincher's docs corpus matters most on README/CHANGELOG, both of
// which can carry setext headings depending on tooling.
func TestExtractMarkdown_SetextHeadings(t *testing.T) {
	src := []byte("Title\n=====\n\nIntro prose.\n\nSubsection\n----------\n\nMore.\n")
	r := Extract(src, "Markdown", "setext.md")
	if r == nil {
		t.Fatal("Extract returned nil")
	}
	if len(r.Symbols) != 2 {
		t.Errorf("expected 2 sections from setext headings, got %d", len(r.Symbols))
	}
	byQN := make(map[string]ExtractedSymbol)
	for _, s := range r.Symbols {
		byQN[s.QualifiedName] = s
	}
	if _, ok := byQN["title"]; !ok {
		t.Errorf("missing 'title' (H1-setext); got QNs: %v", mapKeys(byQN))
	}
	if _, ok := byQN["title.subsection"]; !ok {
		t.Errorf("missing 'title.subsection' (H2-setext); got QNs: %v", mapKeys(byQN))
	}
}
