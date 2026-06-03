// SPDX-License-Identifier: MIT

package server

import (
	"strings"
	"testing"
)

// #397: guide returned shallow generic next-tools for tasks that
// reference well-known pincher concepts. Three regressions covered:
//
//  1. "why does X" wasn't classified as shapeUnderstand — it fell through
//     to shapeUnknown and the generic architecture+search recommendation
//     instead of the deeper context-read flow.
//
//  2. Acronym hint extraction: for "add support for INI file parsing"
//     the tied-length runs `[INI]` and `[parsing]` resolved to "parsing"
//     by total-character length, losing the "INI" specificity. The
//     acronym is the discriminator; "parsing" is scope.
//
//  3. Domain-concept dictionary: tasks mentioning "MCP tool" / "schema
//     migration" / "extractor" / etc. now prepend a concept-aware
//     starter pointing at the actual file/symbol (e.g. registerTools
//     in internal/server/server.go) rather than a generic search.

func TestClassifyTaskShape_WhyDoesIsUnderstand(t *testing.T) {
	t.Parallel()
	cases := []struct {
		task string
		want guideShape
	}{
		// Pure "why" tasks — no overlapping shape keywords (no "test",
		// no "callers", no "fix"). These fell through to shapeUnknown
		// before #397.
		{"why does indexing skip symlinked directories", shapeUnderstand},
		{"why is the supervisor flapping on Ubuntu", shapeUnderstand},
		{"why are responses delayed past the probe deadline", shapeUnderstand},
		{"why do projects sometimes lock", shapeUnderstand},
	}
	for _, c := range cases {
		if got := classifyTaskShape(c.task); got != c.want {
			t.Errorf("classifyTaskShape(%q) = %v, want %v", c.task, got, c.want)
		}
	}
}

func TestTaskHintFromString_AcronymTieBreak(t *testing.T) {
	t.Parallel()
	cases := []struct {
		task string
		want string
	}{
		// Tied 1-token runs [INI] vs [parsing]: acronym wins under
		// the new tie-break (was "parsing" pre-#397 because total-char
		// length was the only discriminator).
		{"add support for INI file parsing", "INI"},
	}
	for _, c := range cases {
		if got := taskHintFromString(c.task); got != c.want {
			t.Errorf("taskHintFromString(%q) = %q, want %q", c.task, got, c.want)
		}
	}
}

func TestDomainConceptHint_RecommendationsByPattern(t *testing.T) {
	t.Parallel()
	cases := []struct {
		task         string
		wantContains string // substring expected in the prepended why
	}{
		{"add a new MCP tool that returns symbols by SHA", "registerTools"},
		{"how does schema migration work", "schemaMigrations"},
		{"add a new language extractor for Zig", "registry.go"},
		{"explain runGitDiff and changed_files semantics", "runGitDiff"},
		{"what happens during supervisor respawn", "supervisor.go"},
		{"how does pinchQL pushdown WHERE work", "Execute()"},
		{"trace BFS depth ranking", "traceViaCTE"},
	}
	for _, c := range cases {
		got := domainConceptHint(c.task)
		if got == nil {
			t.Errorf("domainConceptHint(%q) = nil, want concept-aware hint containing %q", c.task, c.wantContains)
			continue
		}
		why := (*got)["why"]
		if !strings.Contains(why, c.wantContains) {
			t.Errorf("domainConceptHint(%q).why = %q, want to contain %q", c.task, why, c.wantContains)
		}
	}
}

func TestDomainConceptHint_NoMatchReturnsNil(t *testing.T) {
	t.Parallel()
	cases := []string{
		"fix the login timeout bug",
		"refactor the auth middleware",
		"",
	}
	for _, task := range cases {
		if got := domainConceptHint(task); got != nil {
			t.Errorf("domainConceptHint(%q) = %v, want nil (no concept matched)", task, *got)
		}
	}
}
