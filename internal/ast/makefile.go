// SPDX-License-Identifier: MIT

package ast

import (
	"bytes"
	"regexp"
	"strings"
)

// extractMakefile is a regex-tier extractor (confidence 0.85) for the
// symbol-extraction-relevant subset of Makefile syntax:
//
//   - Rule targets at column 0 → Function symbols (one per target)
//   - .PHONY: targets... → marks listed targets is_exported=true
//   - VARIABLE = value / VARIABLE := value → Setting symbols
//
// Skipped (not symbol-shaped, or out of scope for v1):
//   - Pattern rules (`%.o: %.c`) — no concrete name to extract
//   - Inline shell recipes (the lines after a target, indented with TAB)
//   - `define`/`endef` blocks (used for multi-line macros; rare in
//     practice and would need a stateful parser)
//   - `include` / `-include` directives (could become IMPORTS edges
//     in a follow-up; not needed for the rule-name-search use case)
//   - `ifeq`/`else`/`endif` conditionals (the regex pass through; this
//     means a target inside a conditional will still be extracted, which
//     is the right behaviour for "show me all targets")
//
// Real Makefile parsers would handle tab-vs-space lexing and recursive
// `$(VAR)` expansion. The regex tier is fine here because (a) we only
// emit symbols at the rule-target / variable-definition granularity,
// (b) the malformed-syntax cases are tolerated by the line-based regex
// without panicking. Closes #103.

// makeRuleRE matches a rule target at column 0:
//
//	target: dep1 dep2
//	target:: dep1 dep2          (double-colon rule)
//	target : dep1               (allow whitespace before the colon)
//	dir/sub-target: deps        (path-shaped target name)
//
// Captures: 1 = target name. The pattern explicitly excludes:
//   - Targets containing `%` (pattern rules — no concrete name)
//   - Targets containing `$` (variable-expanded names — can't resolve)
//   - Lines starting with `.` followed by a space (special directives
//     like `.PHONY:`, `.PRECIOUS:` — handled separately or skipped)
//
// The `^[A-Za-z0-9_]` prefix gate is the key constraint that distinguishes
// rule lines from variable assignments (which start with the same shape
// but are followed by `=`/`:=` not `:` followed by space/end-of-line).
// #1344 v0.71: tightened the two `\s*` clauses to `[ \t]*` so they do
// NOT consume newlines. Pre-fix, `deps:\n\techo deps` was matched as
// a single logical rule with prereqs="echo deps" — the recipe line was
// folded into the prerequisites. Pre-fix this was invisible because
// only Function symbols were emitted (the recipe-fold didn't change
// the captured name); post-fix's CALLS pass surfaced it as a spurious
// `deps → deps` self-edge. The new bounded character class keeps
// horizontal-whitespace tolerance (which IS legal in Makefiles for
// `target : deps`) while refusing the next-line spillover.
var makeRuleRE = regexp.MustCompile(
	`(?m)^(?P<name>[A-Za-z0-9_][A-Za-z0-9_./-]*)[ \t]*::?[ \t]*([^=\n].*)?$`)

// makePhonyRE matches a .PHONY declaration. Captures: 1 = space-separated
// list of phony target names that follow.
var makePhonyRE = regexp.MustCompile(
	`(?m)^\.PHONY\s*:\s*(?P<targets>.+?)\s*$`)

// makeVarRE matches a top-level (column-0) variable assignment:
//
//	NAME = value          (recursive — value re-expanded on each use)
//	NAME := value         (immediate — value expanded once at definition)
//	NAME ::= value        (POSIX equivalent of :=)
//	NAME ?= value         (conditional — only if not already set)
//	NAME += value         (append)
//
// Captures: 1 = name. Indented lines (which are recipe content, not
// assignments) are excluded by the `^[A-Z_]` column-0 anchor.
var makeVarRE = regexp.MustCompile(
	`(?m)^(?P<name>[A-Za-z_][A-Za-z0-9_]*)\s*(?P<op>:?:?=|\?=|\+=)\s*(?P<value>.*)$`)

// extractMakefile parses a Makefile-shaped source and emits Function
// symbols for rule targets + Setting symbols for top-level variable
// definitions. Targets named in a `.PHONY:` line get is_exported=true.
//
// Byte-range invariant: each symbol's [StartByte, EndByte) covers the
// definition line only. Multi-line recipe bodies are intentionally NOT
// included — the symbol's source view should be the rule signature, not
// the shell commands beneath it (those are out of scope for code
// intelligence; a future iteration could include them as part of the
// rule's "body" similar to how Go function bodies are captured).
func extractMakefile(source []byte, relPath string) *FileResult {
	out := &FileResult{}

	// Pre-scan .PHONY lines to collect the set of exported rule names.
	phonySet := map[string]bool{}
	for _, m := range makePhonyRE.FindAllSubmatch(source, -1) {
		// m[1] is the targets list. Split on whitespace (tabs, spaces).
		for _, name := range strings.Fields(string(m[1])) {
			phonySet[name] = true
		}
	}

	// Pass 1: rule targets. Collect deps alongside so the post-pass
	// can emit CALLS edges between intra-file rules (#1344 v0.71).
	type rulePrereqs struct {
		name      string
		prereqStr string
	}
	var collectedRules []rulePrereqs
	for _, m := range makeRuleRE.FindAllSubmatchIndex(source, -1) {
		nameStart, nameEnd := m[2], m[3]
		name := string(source[nameStart:nameEnd])
		// Filter out lines that look like rules but are actually
		// directives or other non-rule shapes the regex captured.
		if name == "" {
			continue
		}
		if strings.HasPrefix(name, ".") {
			continue // .PHONY, .PRECIOUS, .SUFFIXES — directives, not rules
		}
		if strings.ContainsAny(name, "%$") {
			continue // pattern rule or variable-expanded name
		}
		// Variable assignments can't be confused with rules at this point
		// because makeRuleRE requires `:`, not `=`. But a line like
		//   FOO := bar:baz
		// would slip through if we matched too eagerly. Defensive guard:
		// the line up to the colon must be the target name itself.
		lineStart := bytes.LastIndexByte(source[:nameStart], '\n') + 1
		lineEnd := bytes.IndexByte(source[nameStart:], '\n')
		if lineEnd < 0 {
			lineEnd = len(source) - nameStart
		}
		fullLine := source[lineStart : nameStart+lineEnd]
		// If `=` appears before the colon, this is a variable assignment
		// the rule regex captured by accident — skip.
		if bytesLessIdx(fullLine, '=', ':') {
			continue
		}

		startByte := lineStart
		endByte := nameStart + lineEnd
		startLine := bytes.Count(source[:startByte], []byte("\n")) + 1
		endLine := bytes.Count(source[:endByte], []byte("\n")) + 1
		out.Symbols = append(out.Symbols, ExtractedSymbol{
			Name:          name,
			QualifiedName: name,
			Kind:          "Function",
			StartByte:     startByte,
			EndByte:       endByte,
			StartLine:     startLine,
			EndLine:       endLine,
			IsExported:    phonySet[name],
		})

		// Capture the prerequisites half of the rule for the post-pass
		// CALLS-edge emission. The regex's optional group 2 captures
		// everything after the colon up to end-of-line; m[4],m[5] are
		// its start/end byte offsets (or -1,-1 when absent for a no-
		// dep rule). #1344 v0.71.
		depStr := ""
		if m[4] >= 0 && m[5] > m[4] {
			depStr = string(source[m[4]:m[5]])
		}
		collectedRules = append(collectedRules, rulePrereqs{name: name, prereqStr: depStr})
	}

	// CALLS edges: rule-to-rule prerequisites. Built after the rule
	// pass so we know which prerequisite names resolve to in-file rule
	// symbols (the others are external files / pattern stems / variable
	// references and intentionally drop). #1344 v0.71.
	ruleNames := make(map[string]struct{}, len(collectedRules))
	for _, r := range collectedRules {
		ruleNames[r.name] = struct{}{}
	}
	for _, r := range collectedRules {
		if r.prereqStr == "" {
			continue
		}
		seen := make(map[string]struct{})
		for _, prereq := range strings.Fields(r.prereqStr) {
			// Skip variable-reference prerequisites: $(BUILD_DEPS),
			// ${X}, $X. The resolver has no way to bind these to a
			// concrete rule, and emitting an unresolved-shape edge
			// would surface as #1340-class drop with no signal.
			if strings.ContainsAny(prereq, "$%") {
				continue
			}
			if _, isRule := ruleNames[prereq]; !isRule {
				continue
			}
			if _, dup := seen[prereq]; dup {
				continue
			}
			seen[prereq] = struct{}{}
			out.Edges = append(out.Edges, ExtractedEdge{
				FromQN:     r.name,
				ToName:     prereq,
				Kind:       "CALLS",
				Confidence: 1.0,
			})
		}
	}

	// Pass 2: variable definitions.
	for _, m := range makeVarRE.FindAllSubmatchIndex(source, -1) {
		nameStart, nameEnd := m[2], m[3]
		name := string(source[nameStart:nameEnd])
		// Skip if this matched the value half of a rule (recipe lines
		// can contain VAR=value but they're tab-indented). The regex's
		// `^` anchor + Go's `(?m)` mode already constrains to start of
		// line, but recipes start with TAB so the `[A-Za-z_]` prefix
		// would not match. Belt-and-suspenders: refuse anything where
		// the line prefix is whitespace.
		lineStart := bytes.LastIndexByte(source[:nameStart], '\n') + 1
		if lineStart < nameStart && (source[lineStart] == '\t' || source[lineStart] == ' ') {
			continue
		}
		lineEnd := bytes.IndexByte(source[nameStart:], '\n')
		if lineEnd < 0 {
			lineEnd = len(source) - nameStart
		}
		startByte := lineStart
		endByte := nameStart + lineEnd
		startLine := bytes.Count(source[:startByte], []byte("\n")) + 1
		endLine := bytes.Count(source[:endByte], []byte("\n")) + 1
		out.Symbols = append(out.Symbols, ExtractedSymbol{
			Name:          name,
			QualifiedName: name,
			Kind:          "Setting",
			StartByte:     startByte,
			EndByte:       endByte,
			StartLine:     startLine,
			EndLine:       endLine,
			IsExported:    true, // Makefile variables are top-level by convention
		})
	}

	return out
}

// bytesLessIdx reports whether the first byte `a` in s appears before
// the first byte `b` in s. Returns false if `a` doesn't appear, true
// only when both are present and a's index < b's index. Used to
// discriminate variable assignments from rule definitions when both
// `:` and `=` appear on the same line (rule deps may legally contain
// `=` in variable references).
func bytesLessIdx(s []byte, a, b byte) bool {
	ai := bytes.IndexByte(s, a)
	bi := bytes.IndexByte(s, b)
	if ai < 0 {
		return false
	}
	if bi < 0 {
		return true
	}
	return ai < bi
}
