// SPDX-License-Identifier: MIT

package server

import (
	"fmt"
	"os"
	"strings"

	"github.com/kwad77/pincher/internal/db"
	"github.com/kwad77/pincher/internal/index"
)

// Envelope-compression helpers (session-delta _meta, format=text
// renderers). The currency here is agent-context tokens: every
// function in this file exists to carry the same information the
// consumer needs in fewer tokens.

// parseMetaDeltaEnv reads PINCHER_META_DELTA and returns whether
// session-delta _meta emission is enabled. Default true: the first
// response of a session carries the full per-server-constant fields
// (capabilities); subsequent responses omit them and stamp
// `_meta.meta_delta: true` so consumers can tell intentional omission
// from accident. Set 0/off/false/none/no to restore the legacy
// every-call emission (graceful-degradation kill-switch).
//
// Unknown values default to on — same failure-as-pedagogy shape as
// parseCapabilitiesEnv: a typo'd opt-out keeps the new behavior and
// the operator notices because their per-call payloads stay small.
func parseMetaDeltaEnv(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "off", "false", "0", "none", "no":
		return false
	default:
		return true
	}
}

// capsFingerprint joins a capabilities slice into a comparable string
// for the session-delta dedupe. The slice is computed once at New()
// time and only ever replaced wholesale (SetMCPHTTPPath recomputes
// it), so fingerprint inequality is exactly "the advertisement
// changed and must be re-emitted". PINCHER_META_CAPABILITIES is read
// once at server start and cannot toggle mid-process, so the opt-out
// never needs a re-emit path of its own.
func capsFingerprint(caps []string) string {
	return strings.Join(caps, "\x1f")
}

// parseFormatArg validates the universal list-shape `format` arg
// ("json" default | "text"). Returns (textMode, warning). Unknown
// values fall back to json with a warning naming the accepted set —
// never silently change the response shape on a typo.
func parseFormatArg(args map[string]any) (bool, string) {
	raw, _ := args["format"].(string)
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "json":
		return false, ""
	case "text":
		return true, ""
	default:
		return false, fmt.Sprintf("format=%q is not a known format — valid: \"json\" (default), \"text\". Returning json.", raw)
	}
}

// renderSearchResultsText renders search hits as a dense TSV block:
// one header row, then one line per hit —
// id<TAB>kind<TAB>file:line<TAB>signature-or-name. Same information
// an agent needs to drive a follow-up symbol/context/trace call, at a
// fraction of the JSON token cost (no per-row key repetition, no
// brace/quote chrome). Measured on a representative 20-hit search:
// well under 0.7× the JSON results array (pinned by
// TestFormatText_Search_TokenRatio).
func renderSearchResultsText(results []db.SearchResult) string {
	var b strings.Builder
	b.WriteString("id\tkind\tfile:line\tsignature")
	for _, r := range results {
		sig := r.Symbol.Signature
		if sig == "" {
			sig = r.Symbol.Name
		}
		fmt.Fprintf(&b, "\n%s\t%s\t%s:%d\t%s",
			r.Symbol.ID, r.Symbol.Kind, r.Symbol.FilePath, r.Symbol.StartLine, sig)
	}
	return b.String()
}

// renderTraceHopsText renders trace hops as a dense TSV block: one
// header row, then one line per hop — depth<TAB>risk<TAB>id. The id
// embeds file_path + qualified name + kind, so the line carries
// everything needed for a follow-up context/symbol call. Risk renders
// "-" when risk labelling was disabled (risk=false).
func renderTraceHopsText(hops []index.Hop) string {
	var b strings.Builder
	b.WriteString("depth\trisk\tid")
	for _, h := range hops {
		risk := h.Risk
		if risk == "" {
			risk = "-"
		}
		fmt.Fprintf(&b, "\n%d\t%s\t%s", h.Depth, risk, h.Symbol.ID)
	}
	return b.String()
}

// metaDeltaEnabledFromEnv is the New()-time read. Split out so tests
// can assert the parse table without spinning up a server.
func metaDeltaEnabledFromEnv() bool {
	return parseMetaDeltaEnv(os.Getenv("PINCHER_META_DELTA"))
}
