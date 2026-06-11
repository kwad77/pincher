// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// format=toon on search/trace: renders the list-shaped payload as a
// TOON (Token-Oriented Object Notation) tabular block
// (data.results_toon) inside the standard envelope, replacing the
// results/hops array. _meta unchanged. Mirrors format_text_test.go.

// Shape: format=toon replaces results with results_toon; the envelope
// (count/total/has_more/_meta) is unchanged; the block is one tabular
// header declaring the field list plus one bare row per hit.
func TestHandleSearch_FormatTOON_Shape(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	mustUpsertProject(t, store, "p-toon", "/tmp/p-toon", "toonproj")
	srv.sessionID = "p-toon"
	srv.sessionRoot = "/tmp/p-toon"
	seedFormatSearchCorpus(t, store, "p-toon", 5)

	res, err := srv.handleSearch(context.Background(), makeReq(map[string]any{
		"query":  "FmtHitHandler*",
		"format": "toon",
	}))
	if err != nil {
		t.Fatalf("handleSearch: %v", err)
	}
	body := decode(t, res)
	if _, present := body["results"]; present {
		t.Errorf("format=toon must replace results with results_toon; results still present")
	}
	if _, present := body["results_text"]; present {
		t.Errorf("format=toon must not emit results_text")
	}
	toon, ok := body["results_toon"].(string)
	if !ok || toon == "" {
		t.Fatalf("format=toon must return non-empty results_toon; got %T %v", body["results_toon"], body["results_toon"])
	}
	lines := strings.Split(toon, "\n")
	if lines[0] != "results[5]{file,id,kind,line,name,signature}:" {
		t.Errorf("tabular header mismatch; got %q", lines[0])
	}
	count, _ := body["count"].(float64)
	if len(lines)-1 != int(count) {
		t.Errorf("results_toon has %d data rows, count=%v — must match", len(lines)-1, count)
	}
	for i, line := range lines[1:] {
		if !strings.HasPrefix(line, "  ") {
			t.Errorf("row %d: tabular rows must be indented one level: %q", i, line)
		}
	}
	// _meta unchanged: standard fields still stamped.
	meta, _ := body["_meta"].(map[string]any)
	for _, k := range []string{"tokens_used", "latency_ms"} {
		if _, ok := meta[k]; !ok {
			t.Errorf("_meta must be unchanged under format=toon; missing %q", k)
		}
	}
}

// Honest measurement gate: on the same representative 20-hit search
// the text rendering is gated on, the TOON rendering must cost < 0.7x
// the JSON results array in ApproxTokens. The logged ratios (toon vs
// json, toon vs text) feed the changelog.
func TestFormatTOON_Search_TokenRatio(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	mustUpsertProject(t, store, "p-toon-ratio", "/tmp/p-toon-ratio", "toonratio")
	srv.sessionID = "p-toon-ratio"
	srv.sessionRoot = "/tmp/p-toon-ratio"
	seedFormatSearchCorpus(t, store, "p-toon-ratio", 20)

	jsonRes, err := srv.handleSearch(context.Background(), makeReq(map[string]any{
		"query": "FmtHitHandler*",
	}))
	if err != nil {
		t.Fatalf("handleSearch json: %v", err)
	}
	jsonBody := decode(t, jsonRes)
	rows, _ := jsonBody["results"].([]any)
	if len(rows) != 20 {
		t.Fatalf("fixture must yield 20 hits; got %d", len(rows))
	}
	rowsJSON, _ := json.Marshal(rows)
	jsonTokens := db.ApproxTokens(string(rowsJSON))

	toonRes, err := srv.handleSearch(context.Background(), makeReq(map[string]any{
		"query":  "FmtHitHandler*",
		"format": "toon",
	}))
	if err != nil {
		t.Fatalf("handleSearch toon: %v", err)
	}
	toonBody := decode(t, toonRes)
	toon, _ := toonBody["results_toon"].(string)
	if strings.Count(toon, "\n") != 20 { // header + 20 rows
		t.Fatalf("toon rendering must carry 20 data rows; got %d", strings.Count(toon, "\n"))
	}
	toonTokens := db.ApproxTokens(toon)

	textRes, err := srv.handleSearch(context.Background(), makeReq(map[string]any{
		"query":  "FmtHitHandler*",
		"format": "text",
	}))
	if err != nil {
		t.Fatalf("handleSearch text: %v", err)
	}
	textBody := decode(t, textRes)
	text, _ := textBody["results_text"].(string)
	textTokens := db.ApproxTokens(text)

	ratio := float64(toonTokens) / float64(jsonTokens)
	vsText := float64(toonTokens) / float64(textTokens)
	t.Logf("format=toon 20-hit search: %d tokens vs %d JSON tokens — ratio %.2f (vs text: %d tokens, ratio %.2f)",
		toonTokens, jsonTokens, ratio, textTokens, vsText)
	if ratio >= 0.7 {
		t.Errorf("format=toon must cost < 0.7x the JSON results array on the 20-hit fixture; got %.2f (%d vs %d tokens)",
			ratio, toonTokens, jsonTokens)
	}
}

// Determinism: same input, byte-identical output — Go map iteration
// order must never leak into the rendering (keys are sorted).
func TestTOONEncode_Deterministic(t *testing.T) {
	t.Parallel()
	build := func() map[string]any {
		return map[string]any{
			"zeta":  1,
			"alpha": "x, y",
			"nested": map[string]any{
				"b": true,
				"a": nil,
				"c": []any{"one", 2, map[string]any{"k": "v"}},
			},
			"rows": []any{
				map[string]any{"id": "a.go::pkg.A#Function", "n": 1},
				map[string]any{"id": "b.go::pkg.B#Function", "n": 2},
			},
		}
	}
	want := toonEncode(build())
	for i := 0; i < 50; i++ {
		if got := toonEncode(build()); got != want {
			t.Fatalf("iteration %d: non-deterministic output:\n%q\nvs\n%q", i, got, want)
		}
	}
}

// Fidelity: every id / file_path / name in the JSON results must
// appear VERBATIM in the TOON rendering — the agent must be able to
// copy IDs exactly into a follow-up symbol/context/trace call.
func TestFormatTOON_Search_Fidelity(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	mustUpsertProject(t, store, "p-toon-fid", "/tmp/p-toon-fid", "toonfid")
	srv.sessionID = "p-toon-fid"
	srv.sessionRoot = "/tmp/p-toon-fid"
	seedFormatSearchCorpus(t, store, "p-toon-fid", 20)

	jsonRes, err := srv.handleSearch(context.Background(), makeReq(map[string]any{
		"query": "FmtHitHandler*",
	}))
	if err != nil {
		t.Fatalf("handleSearch json: %v", err)
	}
	jsonBody := decode(t, jsonRes)
	rows, _ := jsonBody["results"].([]any)
	if len(rows) != 20 {
		t.Fatalf("fixture must yield 20 hits; got %d", len(rows))
	}

	toonRes, err := srv.handleSearch(context.Background(), makeReq(map[string]any{
		"query":  "FmtHitHandler*",
		"format": "toon",
	}))
	if err != nil {
		t.Fatalf("handleSearch toon: %v", err)
	}
	toonBody := decode(t, toonRes)
	toon, _ := toonBody["results_toon"].(string)

	for i, raw := range rows {
		row, _ := raw.(map[string]any)
		for _, field := range []string{"id", "file_path", "name"} {
			v, _ := row[field].(string)
			if v == "" {
				t.Fatalf("row %d: json form missing %s", i, field)
			}
			if !strings.Contains(toon, v) {
				t.Errorf("row %d: %s %q must appear verbatim in the toon rendering", i, field, v)
			}
		}
	}
}

// Trace shape: format=toon replaces hops with results_toon —
// hops[N]{depth,id,risk}: plus one bare row per hop.
func TestHandleTrace_FormatTOON_Shape(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	mustUpsertProject(t, store, "p-toon-tr", "/tmp/p-toon-tr", "toontr")
	srv.sessionID = "p-toon-tr"
	srv.sessionRoot = "/tmp/p-toon-tr"

	mustUpsertSymbols(t, store, []db.Symbol{
		{ID: "a.go::pkg.A#Function", ProjectID: "p-toon-tr",
			FilePath: "a.go", Name: "A", QualifiedName: "pkg.A",
			Kind: "Function", Language: "Go", ExtractionConfidence: 1.0},
		{ID: "b.go::pkg.B#Function", ProjectID: "p-toon-tr",
			FilePath: "b.go", Name: "B", QualifiedName: "pkg.B",
			Kind: "Function", Language: "Go", ExtractionConfidence: 1.0},
	})
	mustUpsertEdges(t, store, "p-toon-tr", []db.Edge{{
		FromID: "a.go::pkg.A#Function",
		ToID:   "b.go::pkg.B#Function", Kind: "CALLS", Confidence: 1.0,
	}})

	res, err := srv.handleTrace(context.Background(), makeReq(map[string]any{
		"name":      "A",
		"direction": "outbound",
		"format":    "toon",
	}))
	if err != nil {
		t.Fatalf("handleTrace: %v", err)
	}
	body := decode(t, res)
	if _, present := body["hops"]; present {
		t.Errorf("format=toon must replace hops with results_toon; hops still present")
	}
	toon, ok := body["results_toon"].(string)
	if !ok {
		t.Fatalf("format=toon must return results_toon; got %T", body["results_toon"])
	}
	lines := strings.Split(toon, "\n")
	if lines[0] != "hops[1]{depth,id,risk}:" {
		t.Errorf("tabular header mismatch; got %q", lines[0])
	}
	if len(lines) != 2 {
		t.Fatalf("want 1 hop row; got %d lines", len(lines)-1)
	}
	if strings.TrimPrefix(lines[1], "  ") != "1,b.go::pkg.B#Function,CRITICAL" {
		t.Errorf("hop row mismatch; got %q", lines[1])
	}
	// total + _meta unchanged.
	if total, _ := body["total"].(float64); int(total) != 1 {
		t.Errorf("total must stay 1 under format=toon; got %v", body["total"])
	}
	if _, ok := body["_meta"].(map[string]any); !ok {
		t.Errorf("_meta must survive format=toon")
	}
}

// --- round-trip-ish sanity ---------------------------------------------
//
// A tiny TOON *tabular* reader, TEST ONLY (pincher itself never parses
// TOON). Proves the tabular rows align with their declared fields on a
// fixture with quoting edge cases (comma inside a signature, empty
// string field, leading space, embedded quote, numeric-looking string).

// parseTOONTabular parses a `key[N]{f1,f2,...}:` block into its field
// list and decoded row cells.
func parseTOONTabular(t *testing.T, s string) (fields []string, rows [][]string) {
	t.Helper()
	lines := strings.Split(s, "\n")
	header := lines[0]
	openIdx := strings.Index(header, "{")
	closeIdx := strings.Index(header, "}")
	if openIdx < 0 || closeIdx < openIdx || !strings.HasSuffix(header, ":") {
		t.Fatalf("not a tabular header: %q", header)
	}
	fields = strings.Split(header[openIdx+1:closeIdx], ",")
	for _, line := range lines[1:] {
		rows = append(rows, splitTOONRow(t, strings.TrimPrefix(line, "  ")))
	}
	return fields, rows
}

// splitTOONRow splits one comma-delimited tabular row, honoring quoted
// cells with backslash escapes.
func splitTOONRow(t *testing.T, line string) []string {
	t.Helper()
	var cells []string
	var cur strings.Builder
	inQuote := false
	escaped := false
	for _, r := range line {
		switch {
		case escaped:
			switch r {
			case 'n':
				cur.WriteByte('\n')
			case 'r':
				cur.WriteByte('\r')
			case 't':
				cur.WriteByte('\t')
			default:
				cur.WriteRune(r) // \" and \\
			}
			escaped = false
		case inQuote && r == '\\':
			escaped = true
		case r == '"':
			inQuote = !inQuote
		case r == ',' && !inQuote:
			cells = append(cells, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	if inQuote || escaped {
		t.Fatalf("unterminated quote/escape in row %q", line)
	}
	cells = append(cells, cur.String())
	return cells
}

func TestTOON_TabularRoundTrip(t *testing.T) {
	t.Parallel()
	in := []any{
		map[string]any{
			"id":        "internal/server/a.go::server.A#Function",
			"signature": "func A(ctx context.Context, n int) error", // comma → quoted
			"doc":       "",                                         // empty string → quoted
			"note":      " leading space",                           // → quoted
			"label":     `say "hi"`,                                 // quote → quoted+escaped
			"version":   "123",                                      // numeric-looking string → quoted
			"line":      42,
		},
		map[string]any{
			"id":        "internal/server/b.go::server.B#Function",
			"signature": "func B() error",
			"doc":       "plain",
			"note":      "ok",
			"label":     "plain label",
			"version":   "v2",
			"line":      7,
		},
	}
	toon := toonEncode(map[string]any{"rows": in})

	fields, rows := parseTOONTabular(t, toon)
	wantFields := []string{"doc", "id", "label", "line", "note", "signature", "version"}
	if strings.Join(fields, "|") != strings.Join(wantFields, "|") {
		t.Fatalf("field list mismatch: got %v want %v", fields, wantFields)
	}
	if len(rows) != len(in) {
		t.Fatalf("row count mismatch: got %d want %d", len(rows), len(in))
	}
	for i, row := range rows {
		if len(row) != len(fields) {
			t.Fatalf("row %d: %d cells for %d declared fields: %v", i, len(row), len(fields), row)
		}
		orig := in[i].(map[string]any)
		for j, f := range fields {
			var want string
			switch v := orig[f].(type) {
			case string:
				want = v
			case int:
				want = strconv.Itoa(v)
			default:
				t.Fatalf("unexpected fixture type %T", v)
			}
			if row[j] != want {
				t.Errorf("row %d field %q: decoded %q, want %q", i, f, row[j], want)
			}
		}
	}
}

// Encoder subset coverage: nested maps, non-uniform arrays (list
// form), empty arrays, null, floats — the shapes the generic encoder
// must render deterministically even though search/trace only use the
// tabular path.
func TestTOONEncode_SubsetShapes(t *testing.T) {
	t.Parallel()
	got := toonEncode(map[string]any{
		"name":  "pincher",
		"ratio": 0.5,
		"whole": 3.0,
		"none":  nil,
		"empty": []any{},
		"mixed": []any{"a", 1, map[string]any{"k": "v"}},
		"obj":   map[string]any{"inner": "x", "n": 2},
	})
	want := strings.Join([]string{
		"empty[0]:",
		"mixed[3]:",
		"  - a",
		"  - 1",
		"  -",
		"    k: v",
		"name: pincher",
		"none: null",
		"obj:",
		"  inner: x",
		"  n: 2",
		"ratio: 0.5",
		"whole: 3",
	}, "\n")
	if got != want {
		t.Errorf("subset rendering mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}

	// parseFormatArg accepts toon; typo still falls back to json with a
	// warning naming the full accepted set.
	if f, warn := parseFormatArg(map[string]any{"format": "toon"}); f != rowFormatTOON || warn != "" {
		t.Errorf("parseFormatArg(toon) = (%v, %q)", f, warn)
	}
	if f, warn := parseFormatArg(map[string]any{"format": "tooon"}); f != rowFormatJSON || !strings.Contains(warn, `"toon"`) {
		t.Errorf("parseFormatArg(tooon) = (%v, %q) — must fall back to json and name toon in the warning", f, warn)
	}
}
