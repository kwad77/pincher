// SPDX-License-Identifier: MIT

package server

import (
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// Tests for #247 #2 shape-aware next_steps. The suggestNextStepsForResults
// wrapper layers two tailoring rules on top of the per-kind
// suggestNextSteps:
//   - Many results across many files → prepend `architecture`.
//   - Single high-confidence result → trim secondary suggestions.
// All other shapes pass through to the per-kind defaults.

// Many results spread across many files: agent should orient before
// drilling. Architecture suggestion gets prepended.
func TestSuggestNextStepsForResults_ManyFilesPrependsArchitecture(t *testing.T) {
	t.Parallel()
	results := make([]db.SearchResult, 12)
	for i := 0; i < 12; i++ {
		// Each result lives in a different file → 12 distinct files,
		// well above the >5 threshold.
		results[i] = db.SearchResult{
			Symbol: db.Symbol{
				ID:                   "p::pkg.Foo#Function",
				Name:                 "Foo",
				Kind:                 "Function",
				FilePath:             string(rune('a'+i)) + ".go",
				ExtractionConfidence: 1.0,
			},
		}
	}
	steps := suggestNextStepsForResults(results)
	if len(steps) == 0 {
		t.Fatal("expected at least one suggestion")
	}
	if steps[0]["tool"] != "architecture" {
		t.Errorf("first suggestion tool = %q, want \"architecture\" (many-files orientation)", steps[0]["tool"])
	}
	if !contains(steps[0]["why"], "12 files") {
		t.Errorf("architecture why must report file count for context; got %q", steps[0]["why"])
	}
}

// Many results but few files (e.g. 12 results all in 2 files): NOT a
// many-files case. Should pass through to per-kind defaults without
// prepending architecture.
func TestSuggestNextStepsForResults_ManyResultsFewFilesNoArchitecture(t *testing.T) {
	t.Parallel()
	results := make([]db.SearchResult, 12)
	for i := 0; i < 12; i++ {
		results[i] = db.SearchResult{
			Symbol: db.Symbol{
				ID:                   "p::pkg.Foo#Function",
				Name:                 "Foo",
				Kind:                 "Function",
				FilePath:             []string{"a.go", "b.go"}[i%2],
				ExtractionConfidence: 1.0,
			},
		}
	}
	steps := suggestNextStepsForResults(results)
	for _, s := range steps {
		if s["tool"] == "architecture" {
			t.Errorf("architecture suggestion fired with only 2 files; should require >5:\n%v", steps)
		}
	}
}

// Single high-confidence Function result: Functions normally get TWO
// suggestions (context + trace). Single-hit-high-conf trims to the
// most useful one (context — returns symbol + imports).
func TestSuggestNextStepsForResults_SingleHighConfTrimsSecondary(t *testing.T) {
	t.Parallel()
	results := []db.SearchResult{{
		Symbol: db.Symbol{
			ID: "p::pkg.UniqueFunc#Function", Name: "UniqueFunc",
			Kind: "Function", FilePath: "x.go",
			ExtractionConfidence: 1.0,
		},
	}}
	steps := suggestNextStepsForResults(results)
	if len(steps) != 1 {
		t.Errorf("single high-conf hit should trim to one suggestion; got %d:\n%v", len(steps), steps)
	}
	if steps[0]["tool"] != "context" {
		t.Errorf("kept suggestion = %q, want \"context\" (the most informative single-step move)", steps[0]["tool"])
	}
}

// Single result but BELOW the high-conf threshold: keep both
// suggestions. The agent may need both context (read it) and trace
// (verify it's the right symbol given the low confidence).
func TestSuggestNextStepsForResults_SingleLowConfKeepsBoth(t *testing.T) {
	t.Parallel()
	results := []db.SearchResult{{
		Symbol: db.Symbol{
			ID: "p::pkg.MaybeFunc#Function", Name: "MaybeFunc",
			Kind: "Function", FilePath: "x.go",
			ExtractionConfidence: 0.75,
		},
	}}
	steps := suggestNextStepsForResults(results)
	if len(steps) != 2 {
		t.Errorf("low-conf single result should keep both suggestions; got %d", len(steps))
	}
}

// Multiple results in a few files: pass-through to per-kind defaults.
// The shape doesn't trigger architecture (too few files) or single-
// hit trimming (too many results).
func TestSuggestNextStepsForResults_PassThroughForOrdinaryShape(t *testing.T) {
	t.Parallel()
	results := []db.SearchResult{
		{Symbol: db.Symbol{ID: "p::pkg.A#Function", Name: "A", Kind: "Function", FilePath: "a.go", ExtractionConfidence: 1.0}},
		{Symbol: db.Symbol{ID: "p::pkg.B#Function", Name: "B", Kind: "Function", FilePath: "b.go", ExtractionConfidence: 1.0}},
		{Symbol: db.Symbol{ID: "p::pkg.C#Function", Name: "C", Kind: "Function", FilePath: "c.go", ExtractionConfidence: 1.0}},
	}
	steps := suggestNextStepsForResults(results)
	if len(steps) != 2 {
		t.Errorf("ordinary shape should pass through to per-kind suggestions (2 for Function); got %d", len(steps))
	}
	for _, s := range steps {
		if s["tool"] == "architecture" {
			t.Errorf("architecture should not fire for ordinary shape:\n%v", steps)
		}
	}
}

// Zero results: nil suggestions. Defensive — handleSearch's empty-
// result branch is what actually fires; this is just the helper's
// boundary behavior.
func TestSuggestNextStepsForResults_EmptyReturnsNil(t *testing.T) {
	t.Parallel()
	steps := suggestNextStepsForResults(nil)
	if steps != nil {
		t.Errorf("empty input should return nil; got %v", steps)
	}
}

// Section/Document kinds get a single suggestion from the per-kind
// path. Single-hit trim is a no-op for them (only 1 step already).
func TestSuggestNextStepsForResults_SectionSingleHitNoChange(t *testing.T) {
	t.Parallel()
	results := []db.SearchResult{{
		Symbol: db.Symbol{
			ID: "p::docs/x.md::H1#Section", Name: "H1",
			Kind: "Section", FilePath: "docs/x.md",
			ExtractionConfidence: 1.0,
		},
	}}
	steps := suggestNextStepsForResults(results)
	if len(steps) != 1 {
		t.Errorf("Section single-hit should keep its single per-kind suggestion; got %d", len(steps))
	}
	if steps[0]["tool"] != "symbol" {
		t.Errorf("Section per-kind suggestion = %q, want \"symbol\"", steps[0]["tool"])
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
