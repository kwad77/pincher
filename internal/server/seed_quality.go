// SPDX-License-Identifier: MIT

package server

import "strings"

// PR-6 (loop-substrate): seed-quality scoring for context_for_task.
//
// The measured failure this prevents (dogfood A/B, ADR
// DOGFOOD_LOOP_BENCH_2026_06_10): a prose task BM25-matches seeds via
// docstring text, the composite expands a full ~5k-token envelope
// around symbols that share no identifier with the task, and the agent
// pays for a wrong-cluster dump. Name-overlap between the task's
// identifier-shaped tokens and the selected seeds is a cheap, robust
// proxy for "did BM25 anchor on what the caller meant".

// seedQualityStopwords are english function words and generic task
// verbs that carry no identifier signal. Deliberately modest — an
// aggressive list would discard legitimate lowercase identifiers.
var seedQualityStopwords = map[string]bool{
	"the": true, "a": true, "an": true, "and": true, "or": true,
	"of": true, "in": true, "on": true, "to": true, "for": true,
	"with": true, "from": true, "by": true, "at": true, "as": true,
	"is": true, "are": true, "was": true, "be": true, "been": true,
	"it": true, "its": true, "this": true, "that": true, "these": true,
	"those": true, "how": true, "what": true, "where": true, "why": true,
	"when": true, "which": true, "does": true, "do": true, "did": true,
	"not": true, "no": true, "can": true, "could": true, "should": true,
	"would": true, "will": true, "into": true, "over": true,
	"under": true, "about": true, "fix": true, "fixing": true,
	"add": true, "adding": true, "make": true, "making": true,
	"work": true, "works": true, "working": true, "broken": true,
	"missing": true, "issue": true, "issues": true, "problem": true,
	"bug": true, "bugs": true, "code": true, "codebase": true,
	"project": true, "understand": true, "investigate": true,
	"find": true, "look": true, "support": true, "handle": true,
	"handling": true,
}

// identifierTokens splits a free-form task string into lowercase
// matching tokens. `strong` tokens have identifier shape (camelCase
// hump, underscore, or dotted path); `weak` tokens are plain words of
// length >= 4 that aren't stopwords. Edge punctuation is stripped;
// interior '.'/'_' survive so dotted paths stay intact.
func identifierTokens(task string) (strong, weak []string) {
	for _, raw := range strings.Fields(task) {
		tok := strings.Trim(raw, "\"'`(),:;!?[]{}")
		if tok == "" {
			continue
		}
		if hasIdentifierShape(tok) {
			strong = append(strong, strings.ToLower(tok))
			continue
		}
		lt := strings.ToLower(tok)
		if seedQualityStopwords[lt] {
			continue
		}
		// TitleCase tokens of any length are usually symbol names
		// ("Mid", "Run", "New") — sentence-start capitals on prose
		// words are absorbed by the stopword check above.
		titleCase := tok[0] >= 'A' && tok[0] <= 'Z'
		if len(lt) >= 4 || (titleCase && len(lt) >= 2) {
			weak = append(weak, lt)
		}
	}
	return strong, weak
}

// hasIdentifierShape reports whether a token looks like a code
// identifier rather than a prose word: an underscore or interior dot,
// or a camelCase hump (uppercase after the first rune alongside at
// least one lowercase letter — so ALLCAPS prose like "TODO" doesn't
// count).
func hasIdentifierShape(tok string) bool {
	if len(tok) > 2 && (strings.Contains(tok, "_") || strings.Contains(strings.Trim(tok, "."), ".")) {
		return true
	}
	hasUpperAfterFirst := false
	hasLower := false
	for i, r := range tok {
		if i > 0 && r >= 'A' && r <= 'Z' {
			hasUpperAfterFirst = true
		}
		if r >= 'a' && r <= 'z' {
			hasLower = true
		}
	}
	return hasUpperAfterFirst && hasLower
}

// seedQuality is the per-call confidence stamp for task-driven seed
// resolution, surfaced as _meta.seed_quality on every task-mode
// context_for_task response.
type seedQuality struct {
	Level            string  `json:"level"` // high | medium | low
	ExactNameMatch   bool    `json:"exact_name_match"`
	NameMatchRatio   float64 `json:"name_match_ratio"`
	IdentifierTokens int     `json:"identifier_tokens"`
	SeededVia        string  `json:"seeded_via"` // and | identifier_or | or_fallback
}

// computeSeedQuality scores how well the selected seeds' names overlap
// the task's tokens. Level semantics: `high` — some task token IS a
// seed's name (the caller named their target); `medium` — at least
// half the seeds share a token with the task; `low` — the seeds were
// matched on text the caller can't see (docstrings, signatures) and
// the cluster is likely wrong.
func computeSeedQuality(task, seededVia string, names, qualifiedNames []string) seedQuality {
	strong, weak := identifierTokens(task)
	all := make([]string, 0, len(strong)+len(weak))
	all = append(all, strong...)
	all = append(all, weak...)
	sq := seedQuality{Level: "low", SeededVia: seededVia, IdentifierTokens: len(strong)}
	if len(names) == 0 {
		return sq
	}
	matched := 0
	for i, n := range names {
		nl := strings.ToLower(n)
		ql := ""
		if i < len(qualifiedNames) {
			ql = strings.ToLower(qualifiedNames[i])
		}
		hit := false
		for _, tok := range all {
			if tok == nl {
				sq.ExactNameMatch = true
			}
			if strings.Contains(nl, tok) || (ql != "" && strings.Contains(ql, tok)) {
				hit = true
			}
		}
		if hit {
			matched++
		}
	}
	sq.NameMatchRatio = float64(matched) / float64(len(names))
	switch {
	case sq.ExactNameMatch:
		sq.Level = "high"
	case matched*2 >= len(names):
		sq.Level = "medium"
	}
	return sq
}
