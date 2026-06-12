// SPDX-License-Identifier: MIT

package server

import (
	"encoding/json"
	"os"
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
//     tools/list advertisement only. Default is `full` since #2054: the
//     core default (shipped v1.6) omitted the bootstrap/diagnose-
//     essential tools (index/init/health/architecture/adr/
//     context_for_task/plan_change/investigate_failure/doctor/
//     why_empty), so a fresh MCP client could neither index nor
//     diagnose a project. The token win was always dominated by the
//     SCHEMA-STYLE lever, not the toolset lever: full/rich tools/list
//     ≈ 19k approx tokens, full/lean ≈ 7.1k (the lean transform alone
//     captures ~12k), core/lean ≈ 3.3k — narrowing to core only saves
//     a further ~3.7k, not worth breaking onboarding. The shipped
//     default is now full+lean: every tool advertised, most of the
//     saving retained. PINCHER_TOOLSET=core re-narrows for token-tight
//     setups (see docs/reference/http-api.md).
//
//  2. Schema style (PINCHER_SCHEMA_STYLE=rich|lean): `lean` applies a
//     deterministic transform at registration time — each tool
//     description is cut to its first sentence (plus the one-sentence
//     graph-authority note for the tools in leanAuthorityNote) and
//     every arg description is cut to its first sentence then capped
//     at leanArgDescMax chars. No other hand-written variants; the
//     pedagogy lives on in `guide` and docs/reference/tools.md.
//     Default is `lean` since v1.6 (same #2005 measurement);
//     PINCHER_SCHEMA_STYLE=rich restores the full descriptions.
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

// coreToolset is the loop-essential MCP surface advertised under the
// opt-in PINCHER_TOOLSET=core (the default is `full` since #2054): the
// probe set a delivery loop actually cycles
// through (search → symbol/symbols/context → trace → edit → changes →
// verify_change), the envelope dedupe (`batch` — which can still
// dispatch the read-only non-core tools as sub-queries), the ledger
// (`loop`), the decision store (`adr` — added in #2020: `batch`
// sub-queries dispatch read-only tools only, so an MCP-only client on
// the core default had NO path to read or write ADRs, and the loop
// methodology treats the ADR store as first-class memory), and
// `guide` — kept because it routes to everything else and can NAME
// full-mode tools with a restart/HTTP hint (see computeGuide).
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
	"adr":           true,
}

// ToolAdvertised reports whether the named tool appears on the MCP
// tools/list advertisement under the toolset resolved from this
// process's $PINCHER_TOOLSET (#2011): full (the default since #2054)
// advertises every tool, core advertises only coreToolset. Used by the
// hook-check CLI to keep PreToolUse advisory recommendations
// callable — a session running under the opt-in core toolset never saw
// the non-core tools on tools/list, so a tools/call against one returns
// -32602. The resolution is against the CALLING process's
// environment; for the hook subprocess that is a best-effort proxy
// for the server's env (see cmd/pinch/hook_check.go decideGlobHook
// for why the mismatch case stays safe).
func ToolAdvertised(name string) bool {
	if parseToolsetEnv(os.Getenv("PINCHER_TOOLSET")) == toolsetFull {
		return true
	}
	return coreToolset[name]
}

// parseToolsetEnv reads PINCHER_TOOLSET and returns the effective
// toolset mode. The default is `full` since #2054: only the explicit
// canonical "core" narrows the advertisement; anything else (unset,
// "full", a typo) gets the full surface. This is the inverse of the
// pre-#2054 rule — the core default was measured to omit the
// bootstrap/diagnose-essential tools (index/init/health/architecture/
// adr/…), so a fresh MCP client literally could not onboard or
// diagnose a project. The schema-style lever (parseSchemaStyleEnv,
// still lean by default) keeps the dominant token saving: full+lean
// is ~7.1k tools/list tokens vs ~19k full/rich (the lean transform
// captures ~12k), while narrowing to core+lean only shaves a further
// ~3.7k — not worth breaking onboarding for. `core` stays selectable
// for token-tight setups. Unknown values land on the default (full),
// never a third state — same single-switch discipline as
// parseSchemaStyleEnv.
func parseToolsetEnv(v string) string {
	if strings.ToLower(strings.TrimSpace(v)) == toolsetCore {
		return toolsetCore
	}
	return toolsetFull
}

// parseSchemaStyleEnv reads PINCHER_SCHEMA_STYLE and returns the
// effective style. Only the canonical "rich" restores the full
// descriptions; anything else (unset, "lean", a typo) keeps the lean
// default.
func parseSchemaStyleEnv(v string) string {
	if strings.ToLower(strings.TrimSpace(v)) == schemaStyleRich {
		return schemaStyleRich
	}
	return schemaStyleLean
}

// leanArgDescMax caps each arg description under lean style. ~120 chars
// keeps the load-bearing first clause ("Project name or ID. …" →
// "Project name or ID.") while dropping the multi-paragraph pedagogy.
const leanArgDescMax = 120

// leanAuthorityNote carries the one-sentence graph-authority statement
// appended to the lean description of each graph-answer tool. Lean cuts
// the rich description to its first sentence, which drops the authority
// clause the rich text carries at the end — but that clause is load-
// bearing, not pedagogy: without it, agents measurably re-verify graph
// answers with grep/Read (n=3 trust-tax benchmark: stating authority
// halved turns at equal 48/48 accuracy). Kept to one tight sentence per
// tool so the core+lean surface stays under the schema-weight budget
// (TestSchemaWeight_CoreLean_UnderBudget).
// The router tools (router-loop B5) carry a usage note of the same
// class: lean drops the never-block/ownership clauses from the rich
// text, but those are load-bearing for correct loop behavior (consult
// timing; pincher never writes the registry), so each keeps one tight
// sentence within the schema-weight budget.
var leanAuthorityNote = map[string]string{
	"trace":         "Graph-derived; authoritative for caller/callee/count — no text-search re-verification needed absent warnings.",
	"changes":       "Graph-derived; authoritative for blast-radius — no text-search re-verification needed absent warnings.",
	"verify_change": "Graph-derived; authoritative for blast-radius/orphan checks — no re-verification needed absent warnings.",
	"models":        "Read-only render of router state; the router owns the registry — pincher never writes workers.yaml.",
	"route":         "Consult before spawning Make-stage work; never blocks — on router error proceed at the originating model and report the miss.",
}

// leanToolDescription returns the first sentence of a tool description,
// plus the graph-authority note for the tools that carry one.
func leanToolDescription(name, s string) string {
	out := firstSentence(s)
	if note, ok := leanAuthorityNote[name]; ok {
		out += " " + note
	}
	return out
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
