// SPDX-License-Identifier: MIT

package server

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/kwad77/pincher/internal/db"
	"github.com/kwad77/pincher/internal/index"
)

// TOON (Token-Oriented Object Notation) encoder — ENCODER ONLY.
// Pincher never parses TOON; it only emits it as an opt-in row
// rendering (format="toon" on search/trace, carried in results_toon).
//
// Implemented subset of the TOON format:
//
//   - Nesting is indentation-based (2 spaces per level), no braces.
//   - map[string]any: one `key: value` line per primitive entry;
//     nested maps/arrays render as `key:` / `key[N]...:` headers with
//     an indented block beneath. Keys are emitted in sorted order so
//     output is deterministic (Go map iteration order is randomized).
//   - Uniform object arrays — every element a map with the identical
//     key set and only primitive values — use TOON's tabular form:
//     the field list is declared once in the header,
//     `key[N]{f1,f2,...}:`, followed by one bare comma-delimited row
//     per element. This is where the token savings live: no per-row
//     key repetition, no brace/quote chrome.
//   - Non-uniform arrays fall back to list form: `key[N]:` then one
//     `- element` line per entry (maps/arrays inside a list nest
//     under the dash).
//   - Strings are quoted only when necessary: empty, contains the
//     field delimiter (comma), a newline/tab/CR, a double quote,
//     leading/trailing whitespace, or would read as a TOON literal
//     (true/false/null or a number). Quoted strings use JSON-style
//     backslash escapes.
//   - Primitives: bool → true/false, nil → null, integers bare,
//     floats via strconv 'g' formatting (shortest round-trip form).
//
// Not implemented (pincher never emits these shapes): key folding,
// alternate delimiters, length-marker omission, top-level scalars.

// toonEncode renders v (map[string]any / []any / primitives, the
// shapes json.Unmarshal produces) as TOON text. Top-level v must be a
// map; keys render at indent 0. Deterministic: same input, same bytes.
func toonEncode(v map[string]any) string {
	var b strings.Builder
	keys := sortedKeys(v)
	for _, k := range keys {
		writeTOONEntry(&b, k, v[k], 0)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// writeTOONEntry writes one `key: ...` entry (plus any nested block)
// at the given indent level, ending with a newline.
func writeTOONEntry(b *strings.Builder, key string, v any, indent int) {
	pad := strings.Repeat("  ", indent)
	switch val := v.(type) {
	case map[string]any:
		fmt.Fprintf(b, "%s%s:\n", pad, toonString(key))
		for _, k := range sortedKeys(val) {
			writeTOONEntry(b, k, val[k], indent+1)
		}
	case []any:
		writeTOONArray(b, key, val, indent)
	default:
		fmt.Fprintf(b, "%s%s: %s\n", pad, toonString(key), toonScalar(v))
	}
}

// writeTOONArray writes an array entry: tabular form when the array is
// uniform (identical-key maps, primitive values), list form otherwise.
func writeTOONArray(b *strings.Builder, key string, arr []any, indent int) {
	pad := strings.Repeat("  ", indent)
	rowPad := strings.Repeat("  ", indent+1)
	if fields, ok := uniformTOONFields(arr); ok {
		// Tabular form: field list declared once, bare rows after.
		quoted := make([]string, len(fields))
		for i, f := range fields {
			quoted[i] = toonString(f)
		}
		fmt.Fprintf(b, "%s%s[%d]{%s}:\n", pad, toonString(key), len(arr), strings.Join(quoted, ","))
		for _, el := range arr {
			row := el.(map[string]any)
			cells := make([]string, len(fields))
			for i, f := range fields {
				cells[i] = toonScalar(row[f])
			}
			fmt.Fprintf(b, "%s%s\n", rowPad, strings.Join(cells, ","))
		}
		return
	}
	// List form.
	fmt.Fprintf(b, "%s%s[%d]:\n", pad, toonString(key), len(arr))
	for _, el := range arr {
		switch val := el.(type) {
		case map[string]any:
			fmt.Fprintf(b, "%s-\n", rowPad)
			for _, k := range sortedKeys(val) {
				writeTOONEntry(b, k, val[k], indent+2)
			}
		case []any:
			writeTOONArray(b, "-", val, indent+1)
		default:
			fmt.Fprintf(b, "%s- %s\n", rowPad, toonScalar(el))
		}
	}
}

// uniformTOONFields reports whether arr qualifies for TOON's tabular
// form — non-empty, every element a map with the identical key set,
// every value primitive — and returns the sorted field list.
func uniformTOONFields(arr []any) ([]string, bool) {
	if len(arr) == 0 {
		return nil, false
	}
	var fields []string
	for i, el := range arr {
		m, ok := el.(map[string]any)
		if !ok {
			return nil, false
		}
		keys := sortedKeys(m)
		for _, k := range keys {
			if !isTOONPrimitive(m[k]) {
				return nil, false
			}
		}
		if i == 0 {
			fields = keys
			continue
		}
		if len(keys) != len(fields) {
			return nil, false
		}
		for j := range keys {
			if keys[j] != fields[j] {
				return nil, false
			}
		}
	}
	return fields, true
}

func isTOONPrimitive(v any) bool {
	switch v.(type) {
	case nil, string, bool, int, int32, int64, uint, uint32, uint64, float32, float64:
		return true
	default:
		return false
	}
}

// toonScalar renders a primitive value as a TOON cell/value.
func toonScalar(v any) string {
	switch val := v.(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(val)
	case string:
		return toonString(val)
	case int:
		return strconv.Itoa(val)
	case int32:
		return strconv.FormatInt(int64(val), 10)
	case int64:
		return strconv.FormatInt(val, 10)
	case uint:
		return strconv.FormatUint(uint64(val), 10)
	case uint32:
		return strconv.FormatUint(uint64(val), 10)
	case uint64:
		return strconv.FormatUint(val, 10)
	case float32:
		return formatTOONFloat(float64(val))
	case float64:
		return formatTOONFloat(val)
	default:
		// Unsupported scalar kind: stringify via fmt and quote-protect.
		return toonString(fmt.Sprintf("%v", v))
	}
}

// formatTOONFloat renders whole floats without a trailing ".0"-style
// exponent surprise and everything else in shortest 'g' form —
// deterministic for a given bit pattern.
func formatTOONFloat(f float64) string {
	if f == float64(int64(f)) && f < 1e15 && f > -1e15 {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// toonString quotes s only when TOON requires it: empty string, the
// comma field delimiter, newline/CR/tab, a double quote, leading or
// trailing whitespace, a leading "- " (list-marker ambiguity), a
// trailing colon, or a string that would read as a literal
// (true/false/null/number). Quoted form uses JSON-style escapes.
func toonString(s string) string {
	if !toonNeedsQuoting(s) {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func toonNeedsQuoting(s string) bool {
	if s == "" {
		return true
	}
	if strings.ContainsAny(s, ",\n\r\t\"\\") {
		return true
	}
	if strings.TrimSpace(s) != s {
		return true
	}
	if strings.HasPrefix(s, "- ") || s == "-" || strings.HasSuffix(s, ":") {
		return true
	}
	switch s {
	case "true", "false", "null":
		return true
	}
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		return true
	}
	return false
}

// renderSearchResultsTOON renders search hits as a TOON tabular block
// under the `results` key — the field list declared once, then one
// bare comma-delimited row per hit. Same information results_text
// carries (id / kind / file / line / signature-or-name) plus the bare
// `name`, at a fraction of the JSON token cost (pinned by
// TestFormatTOON_Search_TokenRatio). ids/file paths render verbatim
// (they contain no commas) so the agent can copy them exactly into a
// follow-up symbol/context/trace call.
func renderSearchResultsTOON(results []db.SearchResult) string {
	rows := make([]any, 0, len(results))
	for _, r := range results {
		sig := r.Symbol.Signature
		if sig == "" {
			sig = r.Symbol.Name
		}
		rows = append(rows, map[string]any{
			"id":        r.Symbol.ID,
			"kind":      r.Symbol.Kind,
			"file":      r.Symbol.FilePath,
			"line":      r.Symbol.StartLine,
			"name":      r.Symbol.Name,
			"signature": sig,
		})
	}
	return toonEncode(map[string]any{"results": rows})
}

// renderTraceHopsTOON renders trace hops as a TOON tabular block under
// the `hops` key — one row per hop with depth / id / risk. The id
// embeds file path + qualified name + kind, so each row carries enough
// to drive a follow-up context/symbol call. Risk renders "-" when risk
// labelling was disabled (risk=false), mirroring results_text.
func renderTraceHopsTOON(hops []index.Hop) string {
	rows := make([]any, 0, len(hops))
	for _, h := range hops {
		risk := h.Risk
		if risk == "" {
			risk = "-"
		}
		rows = append(rows, map[string]any{
			"depth": h.Depth,
			"risk":  risk,
			"id":    h.Symbol.ID,
		})
	}
	return toonEncode(map[string]any{"hops": rows})
}
