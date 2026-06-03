// SPDX-License-Identifier: MIT

package server

import (
	"strings"
	"testing"
)

// #942: taskHintFromString split "indexer's" into ["indexer", "s"]
// because the FieldsFunc rune predicate treated `'` as a separator.
// The stray single-letter "s" survived stopword filtering and
// corrupted the hint — agents saw "indexer s worker pool" which
// looks janky and feeds search BM25 a low-signal token. Fix strips
// apostrophes before tokenizing.

func TestTaskHintFromString_ApostropheContractionsNoStrayLetters(t *testing.T) {
	cases := []struct {
		task     string
		notWant  []string
		mustWant string
	}{
		{
			task:     "show me the indexer's worker pool",
			notWant:  []string{" s ", " s$", "^s "},
			mustWant: "indexers", // "indexer's" → "indexers" after apostrophe strip
		},
		{
			task:     "find the parser's tokenizer",
			notWant:  []string{" s ", " s$", "^s "},
			mustWant: "parsers tokenizer",
		},
		{
			task:    "what's wrong with the handler",
			notWant: []string{" s ", " s$", "^s ", " t ", " t$", "^t "},
		},
		// Unicode curly apostrophe (often pasted from rich-text sources).
		{
			task:     "show me the indexer’s worker pool",
			notWant:  []string{" s ", " s$", "^s "},
			mustWant: "indexers",
		},
	}
	for _, c := range cases {
		hint := taskHintFromString(c.task)
		// Use padded form so we can spot " s ", " s$", "^s " precisely.
		padded := " " + hint + " "
		for _, bad := range c.notWant {
			needle := bad
			needle = strings.ReplaceAll(needle, "^", " ")
			needle = strings.ReplaceAll(needle, "$", " ")
			if strings.Contains(padded, needle) {
				t.Errorf("hint for %q contains stray pattern %q; got hint=%q", c.task, bad, hint)
			}
		}
		if c.mustWant != "" && !strings.Contains(hint, c.mustWant) {
			t.Errorf("hint for %q missing expected substring %q; got %q", c.task, c.mustWant, hint)
		}
	}
}

// Non-contraction inputs unchanged: no apostrophes means no behavior
// difference. Pin the regression so an over-aggressive future strip
// doesn't eat real word characters.
func TestTaskHintFromString_NoApostrophe_Unchanged(t *testing.T) {
	cases := map[string]string{
		"refactor the indexer worker pool":             "indexer worker pool",
		"how does the auth flow work":                  "auth flow",
		"find functions that handle http requests":    "http requests",
	}
	for task, want := range cases {
		got := taskHintFromString(task)
		if got != want {
			t.Errorf("taskHintFromString(%q) = %q, want %q", task, got, want)
		}
	}
}
