// SPDX-License-Identifier: MIT

package server

import "testing"

// #1107: classifyTaskShape missed the idiomatic English phrasing
// "no one calls" / "nobody calls". Pre-fix, "find functions in this
// repo that no one calls" routed to shapeUnknown and guide produced
// a search+context flow with a single-word discriminator ("one") from
// the task — totally unrelated to the dead-code intent. Same family
// as #768 (the gap between "no" and "callers").

func TestClassifyTaskShape_DeadCode_NoOneIdiom(t *testing.T) {
	t.Parallel()
	cases := []string{
		"find functions in this repo that no one calls",
		"functions no one calls",
		"which methods nobody calls",
		"list helpers that nobody called",
	}
	for _, task := range cases {
		t.Run(task, func(t *testing.T) {
			if got := classifyTaskShape(task); got != shapeDeadCode {
				t.Errorf("classifyTaskShape(%q) = %v, want shapeDeadCode", task, got)
			}
		})
	}
}

// Control: "no one" appearing for a different intent (e.g. asking
// about a singular item) must NOT route to dead_code. Guarded by the
// trailing "call" — the dead-code idiom is specifically "no one
// calls/called/calling", not bare "no one".
func TestClassifyTaskShape_NoOne_WithoutCallVerb_DoesNotMatch(t *testing.T) {
	t.Parallel()
	// "no one mentioned this" is not a dead-code task — it should
	// fall through to shapeUnknown (default search-shaped flow).
	if got := classifyTaskShape("no one mentioned this in the docs"); got == shapeDeadCode {
		t.Errorf("bare 'no one' without 'call' verb should NOT route to shapeDeadCode; got %v", got)
	}
}
