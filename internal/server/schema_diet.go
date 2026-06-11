// SPDX-License-Identifier: MIT

package server

import (
	"encoding/json"
	"strings"
)

// Schema diet (#2003): two opt-in mechanisms that cut the per-session
// MCP schema overhead measured in the messy-corpus loopbench run
// (PR #2002 — ~46.5k tokens of tool schemas cache-created at session
// start vs ~27k for native arms, re-read every turn; pincher lost an
// otherwise-winning 22-vs-36-turn run on tokens because of it).
//
//  1. Toolset mode (PINCHER_TOOLSET=core|full, or --toolset): `core`
//     registers only the loop-essential MCP surface (coreToolset below).
//     EVERY tool stays registered on s.handlers/s.tools, so the HTTP
//     /v1/<tool> routes, the OpenAPI spec, the tool-contract golden and
//     `batch` sub-query dispatch are unaffected — core mode narrows the
//     tools/list advertisement only. Default is `full` this release;
//     the flip to `core` is a future measured decision (see
//     docs/reference/http-api.md).
//
//  2. Schema style (PINCHER_SCHEMA_STYLE=rich|lean): `lean` applies a
//     deterministic transform at registration time — each tool
//     description is cut to its first sentence and every arg
//     description is cut to its first sentence then capped at
//     leanArgDescMax chars. No hand-written variants; the pedagogy
//     lives on in `guide` and docs/reference/tools.md.
//
// Both knobs are read once at New() (like PINCHER_META_CAPABILITIES);
// they cannot toggle mid-process. The schema-weight gate test
// (schema_weight_test.go) pins the measured totals for all four
// toolset × style combinations.

const (
	toolsetFull = "full"
	toolsetCore = "core"

	schemaStyleRich = "rich"
	schemaStyleLean = "lean"
)

// coreToolset is the loop-essential MCP surface registered under
// PINCHER_TOOLSET=core: the probe set a delivery loop actually cycles
// through (search → symbol/symbols/context → trace → edit → changes →
// verify_change), the envelope dedupe (`batch` — which can still
// dispatch the read-only non-core tools as sub-queries), the ledger
// (`loop`), and `guide` — kept because it routes to everything else
// and can NAME full-mode tools with a restart/HTTP hint (see
// computeGuide).
var coreToolset = map[string]bool{
	"search":        true,
	"symbol":        true,
	"symbols":       true,
	"context":       true,
	"trace":         true,
	"changes":       true,
	"batch":         true,
	"loop":          true,
	"verify_change": true,
	"guide":         true,
}

// parseToolsetEnv reads PINCHER_TOOLSET and returns the effective
// toolset mode. Only the canonical "core" narrows the surface; anything
// else (unset, "full", a typo) keeps the full default — same
// unknown-value safety as parseToolDescriptionsEnv (#1088): a typo'd
// "coree" doesn't silently hide 24 tools.
func parseToolsetEnv(v string) string {
	if strings.ToLower(strings.TrimSpace(v)) == toolsetCore {
		return toolsetCore
	}
	return toolsetFull
}

// parseSchemaStyleEnv reads PINCHER_SCHEMA_STYLE and returns the
// effective style. Only the canonical "lean" opts in; anything else
// keeps the rich default.
func parseSchemaStyleEnv(v string) string {
	if strings.ToLower(strings.TrimSpace(v)) == schemaStyleLean {
		return schemaStyleLean
	}
	return schemaStyleRich
}

// leanArgDescMax caps each arg description under lean style. ~120 chars
// keeps the load-bearing first clause ("Project name or ID. …" →
// "Project name or ID.") while dropping the multi-paragraph pedagogy.
const leanArgDescMax = 120

// leanToolDescription returns the first sentence of a tool description.
func leanToolDescription(s string) string {
	return firstSentence(s)
}

// leanArgDescription returns the first sentence of an arg description,
// hard-capped at leanArgDescMax chars (cut at a word boundary, with a
// "…" marker so a truncation is visibly a truncation).
func leanArgDescription(s string) string {
	out := firstSentence(s)
	if len(out) <= leanArgDescMax {
		return out
	}
	cut := strings.LastIndexByte(out[:leanArgDescMax], ' ')
	if cut <= 0 {
		cut = leanArgDescMax
	}
	return strings.TrimRight(out[:cut], " ,;:—-") + " …"
}

// leanInputSchema walks a JSON Schema document and rewrites every
// "description" string property to its lean form. Non-string
// "description" members (e.g. a property literally named description)
// are recursed into, not rewritten. Returns the input unchanged when it
// isn't valid JSON.
func leanInputSchema(raw json.RawMessage) json.RawMessage {
	var node any
	if err := json.Unmarshal(raw, &node); err != nil {
		return raw
	}
	leanWalk(node)
	out, err := json.Marshal(node)
	if err != nil {
		return raw
	}
	return out
}

func leanWalk(node any) {
	switch v := node.(type) {
	case map[string]any:
		for k, child := range v {
			if k == "description" {
				if s, ok := child.(string); ok {
					v[k] = leanArgDescription(s)
					continue
				}
			}
			leanWalk(child)
		}
	case []any:
		for _, c := range v {
			leanWalk(c)
		}
	}
}

// firstSentence returns s up to and including its first sentence
// terminator. A terminator is '.', '!' or '?' followed by optional
// closing punctuation (markdown bold, backtick, paren, quote) and then
// whitespace or end-of-string. Decimals ("0.95"), file names
// ("tools.md") and env values never qualify because the '.' is followed
// by a non-space; common abbreviations (e.g., i.e., vs., etc., cf.) are
// skipped explicitly. When no terminator is found, s is returned whole.
func firstSentence(s string) string {
	s = strings.TrimSpace(s)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '.' && c != '!' && c != '?' {
			continue
		}
		if c == '.' && isAbbrevBefore(s, i) {
			continue
		}
		j := i + 1
		for j < len(s) && strings.IndexByte("*`)\"'", s[j]) >= 0 {
			j++
		}
		if j >= len(s) || s[j] == ' ' || s[j] == '\n' || s[j] == '\t' {
			return s[:j]
		}
	}
	return s
}

// isAbbrevBefore reports whether the '.' at s[i] terminates a known
// abbreviation rather than a sentence.
func isAbbrevBefore(s string, i int) bool {
	start := i - 4
	if start < 0 {
		start = 0
	}
	prefix := strings.ToLower(s[start:i])
	for _, a := range []string{"e.g", "i.e", "etc", "vs", "cf"} {
		if strings.HasSuffix(prefix, a) {
			return true
		}
	}
	return false
}
