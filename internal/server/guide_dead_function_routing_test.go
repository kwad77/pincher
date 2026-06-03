// SPDX-License-Identifier: MIT

package server

import "testing"

// #1230: classifyTaskShape missed natural-language "find dead
// functions" / "dead methods" / "dead symbols" phrasings. Pre-fix,
// "find all dead functions in this project" routed to shapeFind and
// guide recommended `search query="dead"` — BM25 of the literal
// phrase, totally unrelated to the dead_code tool that exists for
// exactly this use case. Same family as #768 / #1107 (gap between
// the user's natural phrasing and the classifier's keyword list).

// Positive: dead-noun pairings route to shapeDeadCode.
func TestClassifyTaskShape_DeadCode_DeadNounPairings(t *testing.T) {
	t.Parallel()
	cases := []string{
		"find all dead functions in this project",
		"dead functions",
		"list dead methods",
		"identify dead symbols",
		// Plural-suffix coverage via substring (shape rule uses
		// "dead function" as a substring; "dead functions" matches).
		"any dead functions remaining",
	}
	for _, task := range cases {
		t.Run(task, func(t *testing.T) {
			if got := classifyTaskShape(task); got != shapeDeadCode {
				t.Errorf("classifyTaskShape(%q) = %v, want shapeDeadCode", task, got)
			}
		})
	}
}

// Control: "deadline" / "dead-link" / "dead pool" must NOT match the
// dead-code routing — they use "dead" in unrelated contexts. The
// fix uses "dead function/method/symbol" substring guards, NOT bare
// "dead", so these stay safe.
func TestClassifyTaskShape_Dead_WithoutNoun_DoesNotMisroute(t *testing.T) {
	t.Parallel()
	cases := []string{
		"check deadline tracking logic",
		"find dead links in the docs",
		"what is the dead-letter queue policy",
	}
	for _, task := range cases {
		t.Run(task, func(t *testing.T) {
			if got := classifyTaskShape(task); got == shapeDeadCode {
				t.Errorf("classifyTaskShape(%q) = shapeDeadCode but should not — \"dead\" appears in a non-code-survey context", task)
			}
		})
	}
}
