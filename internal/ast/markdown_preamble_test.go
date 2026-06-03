// SPDX-License-Identifier: MIT

package ast

import (
	"testing"
)

// #1097: synthetic preamble Section. Markdown extractor used to skip
// any content before the first heading (banner, badges, title,
// tagline, navigation in READMEs; intro paragraph in design docs).
// On pincher's own README that's lines 1-20 — invisible to search
// corpus=docs. Self-dogfood gap fix: emit a synthetic "preamble"
// Section covering the pre-heading bytes.

func runMarkdownExtract(t *testing.T, source string) *FileResult {
	t.Helper()
	ext := &markdownExtractor{}
	return ext.Extract([]byte(source), "Markdown", "test.md", ExtractOptions{})
}

// Positive: README-shape file (banner + badges + tagline + nav, then
// first heading) emits a preamble Section covering lines 1 through
// the first heading.
func TestMarkdownExtract_Preamble_ReadmeShape(t *testing.T) {
	t.Parallel()
	src := `<div align="center"><img src="banner.png"/></div>

[![CI](badge.svg)](url)
[![Go 1.25](badge.svg)](url)

**Codebase intelligence server for LLM agents.**
Single binary · No cloud dependencies · Any LLM · MCP stdio or HTTP REST

[What it does](#what-it-does) · [Install](#install)

## What it does

Pincher is the thing.
`
	result := runMarkdownExtract(t, src)

	var preamble *ExtractedSymbol
	for i, sym := range result.Symbols {
		if sym.QualifiedName == "preamble" {
			preamble = &result.Symbols[i]
			break
		}
	}
	if preamble == nil {
		t.Fatalf("expected preamble symbol; got %d symbols, names: %v", len(result.Symbols), symbolNames(result.Symbols))
	}
	if preamble.Kind != "Section" {
		t.Errorf("preamble Kind = %q; want Section", preamble.Kind)
	}
	if preamble.StartByte != 0 {
		t.Errorf("preamble StartByte = %d; want 0", preamble.StartByte)
	}
	// EndByte should land before "## What it does" — find that offset
	// in source.
	wantEnd := indexOfString(src, "## What it does")
	if wantEnd <= 0 {
		t.Fatalf("test source missing '## What it does' marker")
	}
	if preamble.EndByte > wantEnd {
		t.Errorf("preamble EndByte = %d; want <= %d (before the H2)", preamble.EndByte, wantEnd)
	}
	if preamble.EndByte == 0 {
		t.Errorf("preamble EndByte = 0; preamble has no content range")
	}
}

// Positive: file with NO headings at all gets one preamble Section
// covering the whole file. Used to disappear entirely from the
// docs corpus pre-#1097.
func TestMarkdownExtract_Preamble_NoHeadingsAtAll(t *testing.T) {
	t.Parallel()
	src := `Just some plain text content with no headings whatsoever.
It still needs to be searchable.
`
	result := runMarkdownExtract(t, src)
	if len(result.Symbols) != 1 {
		t.Fatalf("want exactly 1 symbol (the preamble); got %d: %v", len(result.Symbols), symbolNames(result.Symbols))
	}
	sym := result.Symbols[0]
	if sym.QualifiedName != "preamble" {
		t.Errorf("symbol QN = %q; want preamble", sym.QualifiedName)
	}
	if sym.StartByte != 0 {
		t.Errorf("StartByte = %d; want 0", sym.StartByte)
	}
}

// Negative: file that starts with a heading on line 1 has NO
// preamble — there's no pre-heading content to extract.
func TestMarkdownExtract_Preamble_HeadingOnLine1HasNoPreamble(t *testing.T) {
	t.Parallel()
	src := `# Title On Line One

Some content after.
`
	result := runMarkdownExtract(t, src)
	for _, sym := range result.Symbols {
		if sym.QualifiedName == "preamble" {
			t.Errorf("file starting with heading should have no preamble symbol; got one with EndByte=%d", sym.EndByte)
		}
	}
}

// Control: whitespace-only preamble (blank lines before first
// heading) doesn't emit a useless empty Section.
func TestMarkdownExtract_Preamble_WhitespaceOnlyDoesNotEmit(t *testing.T) {
	t.Parallel()
	src := `



## Heading after blank lines

content
`
	result := runMarkdownExtract(t, src)
	for _, sym := range result.Symbols {
		if sym.QualifiedName == "preamble" {
			t.Errorf("whitespace-only preamble should not emit a Section; got one")
		}
	}
}

// Cross-check: preamble coexists with the regular heading sections —
// the readme-shape test above implicitly exercises this, but pin it
// explicitly so a refactor that accidentally drops the headings
// (early-return on preamble path) breaks loudly.
func TestMarkdownExtract_Preamble_CoexistsWithHeadingSections(t *testing.T) {
	t.Parallel()
	src := `**preamble paragraph**

## First Heading

content

## Second Heading

more content
`
	result := runMarkdownExtract(t, src)
	gotQNs := map[string]bool{}
	for _, sym := range result.Symbols {
		gotQNs[sym.QualifiedName] = true
	}
	for _, want := range []string{"preamble", "first_heading", "second_heading"} {
		if !gotQNs[want] {
			t.Errorf("expected QN %q in result; got %v", want, gotQNs)
		}
	}
}

func symbolNames(syms []ExtractedSymbol) []string {
	out := make([]string, 0, len(syms))
	for _, s := range syms {
		out = append(out, s.QualifiedName)
	}
	return out
}

func indexOfString(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
