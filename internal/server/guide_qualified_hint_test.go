// SPDX-License-Identifier: MIT

package server

import "testing"

// #290: when a task spells out a qualified identifier (`os.Stat`,
// `pkg/sub`, `Class::method`), it's almost always the intended
// subject. The hint extractor must surface that token verbatim
// instead of picking a generic noun like "indexer" or "codebase".
func TestTaskHintFromString_PrefersQualifiedIdentifier(t *testing.T) {
	t.Parallel()
	cases := []struct {
		task string
		want string
	}{
		// The dogfood-found cases that prompted #290.
		{"find every place we call os.Stat in indexer.go", "os.Stat"},
		{"find every place in the codebase where we call os.Stat and might race a deletion", "os.Stat"},

		// Other qualifier shapes.
		{"refactor pkg/sub/util", "pkg/sub/util"},
		{"trace inbound on Class::method", "Class::method"},
		{"how does fmt.Errorf work", "fmt.Errorf"},

		// Multiple qualifiers — longest wins.
		{"compare os.Stat vs filepath.EvalSymlinks", "filepath.EvalSymlinks"},

		// No qualifier — fall back to existing run logic.
		{"how does flushBuffers work", "flushBuffers"},

		// Generic-noun task: the qualifier wins, not "codebase".
		{"audit the codebase for os.Stat usage", "os.Stat"},
	}
	for _, c := range cases {
		t.Run(c.task, func(t *testing.T) {
			got := taskHintFromString(c.task)
			if got != c.want {
				t.Errorf("taskHintFromString(%q) = %q, want %q", c.task, got, c.want)
			}
		})
	}
}

// qualifiedIdentifierHint is the new helper the extractor short-
// circuits through. Pin its behaviour separately so the helper can
// evolve without surprising the caller.
func TestQualifiedIdentifierHint(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		// Dotted Go identifier.
		{"call os.Stat", "os.Stat"},
		{"fmt.Errorf is everywhere", "fmt.Errorf"},

		// Slash path.
		{"in pkg/sub/foo", "pkg/sub/foo"},

		// Double colon.
		{"the Class::method pattern", "Class::method"},

		// Filename — counts because `.` is between alphas. We accept this
		// (filenames are useful search queries too).
		{"in indexer.go", "indexer.go"},

		// Trailing punctuation trimmed.
		{"call os.Stat,", "os.Stat"},
		{"see os.Stat.", "os.Stat"},

		// Bare nouns — no qualifier present.
		{"flushBuffers", ""},
		{"how does it work", ""},

		// Stops at whitespace — `foo bar` isn't a qualifier.
		{"foo bar", ""},

		// Trailing-only special char doesn't qualify.
		{"foo. bar", ""},
		{"foo: bar", ""},

		// Empty.
		{"", ""},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got := qualifiedIdentifierHint(c.in)
			if got != c.want {
				t.Errorf("qualifiedIdentifierHint(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// classifyTaskShape now falls back to `find` when the task carries a
// qualified identifier and matches no explicit shape keyword (#290).
// Better than `unknown` which routes to a generic recommendation.
func TestClassifyTaskShape_QualifiedIdentifierFallsBackToFind(t *testing.T) {
	t.Parallel()
	cases := []struct {
		task string
		want guideShape
	}{
		// The original repro: shape was `unknown`, now `find`.
		{"every place we call os.Stat", shapeFind},

		// `find` keyword still wins explicitly.
		{"find every place we call os.Stat", shapeFind},

		// Trace keyword still beats the qualifier-fallback.
		{"who calls os.Stat", shapeTraceIn},

		// No qualifier and no keyword: still unknown.
		{"general musings about the codebase", shapeUnknown},

		// `understand` keyword wins over qualifier-fallback.
		{"explain os.Stat", shapeUnderstand},
	}
	for _, c := range cases {
		t.Run(c.task, func(t *testing.T) {
			got := classifyTaskShape(c.task)
			if got != c.want {
				t.Errorf("classifyTaskShape(%q) = %q, want %q", c.task, got, c.want)
			}
		})
	}
}
