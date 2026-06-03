// SPDX-License-Identifier: MIT

package server

import (
	"strings"
	"testing"
)

// #1682: comparison / filler words ("like", "such", "similar", ...)
// must not survive into the guide hint. A task that names a reference
// example — "add X backoff to the resolve pass LIKE the watcher" —
// used to extract "...resolve pass like" as the longest non-stopword
// run, dragging the noise token "like" into the templated search
// query where it only matched via the AND→OR fallthrough.

func TestTaskHintFromString_DropsComparisonFillers_1682(t *testing.T) {
	t.Parallel()
	// The #1682 contract is narrow: filler tokens must not SURVIVE into
	// the hint. Which non-filler run wins the longest-run tiebreak is a
	// separate heuristic this test deliberately doesn't pin (over-
	// specifying it bites when a task has two equally-good subjects).
	cases := []struct {
		task   string
		banned []string // tokens that must NOT appear in the hint
	}{
		{"add memory-pressure backoff to the indexer's resolve pass like the watcher has", []string{"like"}},
		{"make the cache eviction work similar to the LRU helper", []string{"similar"}},
		{"implement retry logic such as the backoff in the http client", []string{"such"}},
		{"the parser should be faster than the old tokenizer", []string{"than"}},
		{"the result is too big and also slow", []string{"too", "also"}},
	}
	for _, c := range cases {
		hint := taskHintFromString(c.task)
		toks := strings.Fields(strings.ToLower(hint))
		for _, b := range c.banned {
			for _, tok := range toks {
				if tok == b {
					t.Errorf("task %q: hint %q still contains filler token %q", c.task, hint, b)
				}
			}
		}
	}
}

// Control: a filler word that is ALSO a legitimate substring of a real
// identifier must still survive when it's part of a compound token.
// "likelihood" contains "like" but tokenises as one word — the
// stopword filter is exact-token, so "likelihood" is kept.
func TestTaskHintFromString_FillerSubstringInIdentifierKept_1682(t *testing.T) {
	t.Parallel()
	hint := taskHintFromString("compute the likelihood score")
	if !strings.Contains(strings.ToLower(hint), "likelihood") {
		t.Errorf("hint %q dropped 'likelihood' — exact-token stopword filter must not match substrings", hint)
	}
}
