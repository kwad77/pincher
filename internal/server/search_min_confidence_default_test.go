// SPDX-License-Identifier: MIT

package server

import (
	"encoding/json"
	"strings"
	"testing"
)

// v0.65 description-honesty audit (continuation of v0.64 work):
// the search tool's `min_confidence` per-field schema text claimed
// the default was driven by query shape only — exact-identifier
// vs phrase/wildcard. defaultMinConfidenceFor has had a third
// branch since at least v0.5: corpus="docs" returns 0.0 regardless
// of query shape. Documented now so agents understand why
// multi-word docs queries surface Markdown sections that look
// like "noise" — they're the intentional target.
//
// Table-from-the-start (#1152):
//   - Positive: schema text names the corpus="docs" default branch
//   - Negative: schema text does NOT claim the default is query-
//     shape-only
//   - Control: defaultMinConfidenceFor still returns 0.71 for
//     phrase queries on the code corpus (regression guard for the
//     non-docs path)
//   - Cross-check: the runtime behaviour matches the documented
//     defaults across all three branches (exact / phrase / docs)

func findSearchToolSchema(t *testing.T) map[string]any {
	t.Helper()
	srv, _, _ := newTestServer(t)
	tool := srv.tools["search"]
	if tool == nil {
		t.Fatal("search tool not registered")
	}
	raw, ok := tool.InputSchema.(json.RawMessage)
	if !ok {
		t.Fatalf("InputSchema not json.RawMessage: %T", tool.InputSchema)
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("InputSchema not valid JSON: %v", err)
	}
	return schema
}

func searchMinConfDesc(t *testing.T) string {
	t.Helper()
	schema := findSearchToolSchema(t)
	props, _ := schema["properties"].(map[string]any)
	mc, _ := props["min_confidence"].(map[string]any)
	desc, _ := mc["description"].(string)
	if desc == "" {
		t.Fatal("search.min_confidence description missing")
	}
	return desc
}

// Positive: description names the corpus="docs" default branch.
func TestSearchMinConfDescription_NamesDocsCorpusDefault(t *testing.T) {
	desc := searchMinConfDesc(t)
	mustContain := []string{
		"corpus='docs'", // exact phrasing used in description
		"0.0",           // the default value on the docs branch
		"0.71",          // the default on the phrase/wildcard non-docs branch
	}
	for _, want := range mustContain {
		if !strings.Contains(desc, want) {
			t.Errorf("search.min_confidence description missing %q\nGOT:\n%s", want, desc)
		}
	}
}

// Negative: description must not claim the default is query-shape
// only. Pre-fix the prose pinned only two defaults (exact vs
// phrase) and omitted the corpus branch entirely.
func TestSearchMinConfDescription_DoesNotClaimQueryShapeOnly(t *testing.T) {
	desc := searchMinConfDesc(t)
	// The historically misleading phrasing would have said
	// "either default" or otherwise framed the two query-shape
	// branches as the complete picture. The corpus-aware fix
	// adds a third branch — the description must acknowledge it.
	if strings.Contains(desc, "either default") &&
		!strings.Contains(desc, "corpus") {
		t.Errorf("search.min_confidence description claims 'either default' without mentioning corpus branch — incomplete\nGOT:\n%s", desc)
	}
}

// Control + cross-check: runtime behaviour matches the documented
// defaults across all three branches.
func TestDefaultMinConfidenceFor_RuntimeMatchesDescription(t *testing.T) {
	cases := []struct {
		query  string
		corpus string
		want   float64
		why    string
	}{
		// Exact-identifier query on code corpus: 0.0 (no floor — an
		// identifier can't BM25-match doc-section noise).
		{"Open", "code", 0.0, "exact-identifier on code corpus"},
		{"processOrder", "", 0.0, "exact-identifier with implicit code corpus"},

		// Phrase / multi-word / wildcard on code corpus: 0.71.
		{"open session", "code", 0.71, "multi-word phrase on code corpus"},
		{"auth*", "code", 0.71, "wildcard on code corpus"},

		// Docs corpus regardless of query shape: 0.0.
		{"authentication overview", "docs", 0.0, "phrase on docs corpus"},
		{"Installation", "docs", 0.0, "exact word on docs corpus"},
		{"intro*", "docs", 0.0, "wildcard on docs corpus"},
	}
	for _, c := range cases {
		got := defaultMinConfidenceFor(c.query, c.corpus)
		if got != c.want {
			t.Errorf("%s: defaultMinConfidenceFor(%q, %q) = %v, want %v",
				c.why, c.query, c.corpus, got, c.want)
		}
	}
}
