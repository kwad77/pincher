package server

import (
	"strings"
	"unicode/utf8"

	"github.com/kwad77/pincher/internal/db"
)

// maxTokensArg parses the per-call response budget (loop-substrate
// PR-5). The contract: 0, omitted, or negative = unlimited — the exact
// legacy envelope shape. Budgets are measured with db.ApproxTokens so
// they line up with the _meta.tokens_used the caller already reads.
func maxTokensArg(args map[string]any) int {
	mt := intArg(args, "max_tokens", 0)
	if mt < 0 {
		return 0
	}
	return mt
}

// truncateSourceToTokens cuts source at a line boundary so that
// db.ApproxTokens(result) <= budgetTokens. Deterministic for a given
// (source, budget) pair. Returns the possibly-cut source, the number
// of source lines dropped, and whether a cut happened.
//
// When even the first line exceeds the budget (minified/single-line
// bodies), falls back to a hard byte cut trimmed to a valid UTF-8
// boundary — an empty result would be strictly less useful than a
// partial first line.
func truncateSourceToTokens(source string, budgetTokens int) (string, int, bool) {
	if budgetTokens <= 0 || db.ApproxTokens(source) <= budgetTokens {
		return source, 0, false
	}
	lines := strings.SplitAfter(source, "\n")
	budgetBytes := budgetTokens * 4
	kept := 0
	total := 0
	for _, ln := range lines {
		if total+len(ln) > budgetBytes {
			break
		}
		total += len(ln)
		kept++
	}
	if kept == 0 {
		// Single line bigger than the whole budget: hard byte cut.
		cut := budgetBytes - 3
		if cut < 0 {
			cut = 0
		}
		if cut > len(source) {
			cut = len(source)
		}
		out := source[:cut]
		for len(out) > 0 && !utf8.ValidString(out) {
			out = out[:len(out)-1]
		}
		// ApproxTokens may run in exact-BPE mode (env opt-in); shrink
		// until the measured count fits.
		for len(out) > 0 && db.ApproxTokens(out) > budgetTokens {
			out = out[:len(out)*9/10]
			for len(out) > 0 && !utf8.ValidString(out) {
				out = out[:len(out)-1]
			}
		}
		return out, len(lines), true
	}
	out := strings.Join(lines[:kept], "")
	// Same exact-BPE-mode guard on the line-boundary path.
	for kept > 0 && db.ApproxTokens(out) > budgetTokens {
		kept--
		out = strings.Join(lines[:kept], "")
	}
	return out, len(lines) - kept, true
}
