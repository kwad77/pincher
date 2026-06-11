// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// format=text on search/trace: renders the list-shaped payload as a
// dense TSV block (data.results_text) inside the standard envelope,
// replacing the results/hops array. _meta unchanged.

func seedFormatSearchCorpus(t *testing.T, store *db.Store, projectID string, n int) {
	t.Helper()
	syms := make([]db.Symbol, 0, n)
	for i := 0; i < n; i++ {
		syms = append(syms, db.Symbol{
			ID:                   fmt.Sprintf("internal/server/handlers_%02d.go::server.*Server.FmtHitHandler%02d#Method", i, i),
			ProjectID:            projectID,
			FilePath:             fmt.Sprintf("internal/server/handlers_%02d.go", i),
			Name:                 fmt.Sprintf("FmtHitHandler%02d", i),
			QualifiedName:        fmt.Sprintf("server.*Server.FmtHitHandler%02d", i),
			Kind:                 "Method",
			Language:             "Go",
			StartLine:            10 + i,
			EndLine:              42 + i,
			StartByte:            100 * i,
			EndByte:              100*i + 90,
			Signature:            fmt.Sprintf("func (s *Server) FmtHitHandler%02d(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error)", i),
			ExtractionConfidence: 1.0,
		})
	}
	mustUpsertSymbols(t, store, syms)
}

// Shape: format=text replaces results with results_text; the envelope
// (count/total/has_more/_meta) is unchanged; the block is one header
// row plus one TSV line per hit.
func TestHandleSearch_FormatText_Shape(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	mustUpsertProject(t, store, "p-fmt", "/tmp/p-fmt", "fmtproj")
	srv.sessionID = "p-fmt"
	srv.sessionRoot = "/tmp/p-fmt"
	seedFormatSearchCorpus(t, store, "p-fmt", 5)

	res, err := srv.handleSearch(context.Background(), makeReq(map[string]any{
		"query":  "FmtHitHandler*",
		"format": "text",
	}))
	if err != nil {
		t.Fatalf("handleSearch: %v", err)
	}
	body := decode(t, res)
	if _, present := body["results"]; present {
		t.Errorf("format=text must replace results with results_text; results still present")
	}
	text, ok := body["results_text"].(string)
	if !ok || text == "" {
		t.Fatalf("format=text must return non-empty results_text; got %T %v", body["results_text"], body["results_text"])
	}
	lines := strings.Split(text, "\n")
	if lines[0] != "id\tkind\tfile:line\tsignature" {
		t.Errorf("header row mismatch; got %q", lines[0])
	}
	count, _ := body["count"].(float64)
	if len(lines)-1 != int(count) {
		t.Errorf("results_text has %d data lines, count=%v — must match", len(lines)-1, count)
	}
	// Every data line: 4 TSV columns; col 3 is file:line; col 1 a
	// usable symbol id for a follow-up symbol/context call.
	for i, line := range lines[1:] {
		cols := strings.Split(line, "\t")
		if len(cols) != 4 {
			t.Fatalf("line %d: want 4 TSV columns, got %d: %q", i, len(cols), line)
		}
		if !strings.Contains(cols[0], "::") || !strings.Contains(cols[0], "#") {
			t.Errorf("line %d: col 1 is not a symbol id: %q", i, cols[0])
		}
		if !strings.Contains(cols[2], ":") {
			t.Errorf("line %d: col 3 is not file:line: %q", i, cols[2])
		}
	}
	// _meta unchanged: standard fields still stamped.
	meta, _ := body["_meta"].(map[string]any)
	for _, k := range []string{"tokens_used", "latency_ms"} {
		if _, ok := meta[k]; !ok {
			t.Errorf("_meta must be unchanged under format=text; missing %q", k)
		}
	}
}

// Honest measurement gate: on a representative 20-hit search, the
// text rendering must cost < 0.7x the JSON results array in
// ApproxTokens. The logged ratio feeds the changelog.
func TestFormatText_Search_TokenRatio(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	mustUpsertProject(t, store, "p-fmt-ratio", "/tmp/p-fmt-ratio", "fmtratio")
	srv.sessionID = "p-fmt-ratio"
	srv.sessionRoot = "/tmp/p-fmt-ratio"
	seedFormatSearchCorpus(t, store, "p-fmt-ratio", 20)

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

	textRes, err := srv.handleSearch(context.Background(), makeReq(map[string]any{
		"query":  "FmtHitHandler*",
		"format": "text",
	}))
	if err != nil {
		t.Fatalf("handleSearch text: %v", err)
	}
	textBody := decode(t, textRes)
	text, _ := textBody["results_text"].(string)
	if strings.Count(text, "\n") != 20 { // header + 20 lines
		t.Fatalf("text rendering must carry 20 data lines; got %d", strings.Count(text, "\n"))
	}
	textTokens := db.ApproxTokens(text)

	ratio := float64(textTokens) / float64(jsonTokens)
	t.Logf("format=text 20-hit search: %d tokens vs %d JSON tokens — ratio %.2f", textTokens, jsonTokens, ratio)
	if ratio >= 0.7 {
		t.Errorf("format=text must cost < 0.7x the JSON results array on the 20-hit fixture; got %.2f (%d vs %d tokens)",
			ratio, textTokens, jsonTokens)
	}
}

// Trace shape: format=text replaces hops with results_text —
// depth<TAB>risk<TAB>id, one line per hop.
func TestHandleTrace_FormatText_Shape(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	mustUpsertProject(t, store, "p-fmt-tr", "/tmp/p-fmt-tr", "fmttr")
	srv.sessionID = "p-fmt-tr"
	srv.sessionRoot = "/tmp/p-fmt-tr"

	mustUpsertSymbols(t, store, []db.Symbol{
		{ID: "a.go::pkg.A#Function", ProjectID: "p-fmt-tr",
			FilePath: "a.go", Name: "A", QualifiedName: "pkg.A",
			Kind: "Function", Language: "Go", ExtractionConfidence: 1.0},
		{ID: "b.go::pkg.B#Function", ProjectID: "p-fmt-tr",
			FilePath: "b.go", Name: "B", QualifiedName: "pkg.B",
			Kind: "Function", Language: "Go", ExtractionConfidence: 1.0},
	})
	mustUpsertEdges(t, store, "p-fmt-tr", []db.Edge{{
		FromID: "a.go::pkg.A#Function",
		ToID:   "b.go::pkg.B#Function", Kind: "CALLS", Confidence: 1.0,
	}})

	res, err := srv.handleTrace(context.Background(), makeReq(map[string]any{
		"name":      "A",
		"direction": "outbound",
		"format":    "text",
	}))
	if err != nil {
		t.Fatalf("handleTrace: %v", err)
	}
	body := decode(t, res)
	if _, present := body["hops"]; present {
		t.Errorf("format=text must replace hops with results_text; hops still present")
	}
	text, ok := body["results_text"].(string)
	if !ok {
		t.Fatalf("format=text must return results_text; got %T", body["results_text"])
	}
	lines := strings.Split(text, "\n")
	if lines[0] != "depth\trisk\tid" {
		t.Errorf("header row mismatch; got %q", lines[0])
	}
	if len(lines) != 2 {
		t.Fatalf("want 1 hop line; got %d lines", len(lines)-1)
	}
	cols := strings.Split(lines[1], "\t")
	if len(cols) != 3 {
		t.Fatalf("hop line must have 3 TSV columns; got %q", lines[1])
	}
	if cols[0] != "1" {
		t.Errorf("depth column: want 1, got %q", cols[0])
	}
	if cols[2] != "b.go::pkg.B#Function" {
		t.Errorf("id column: want callee id, got %q", cols[2])
	}
	// total + _meta unchanged.
	if total, _ := body["total"].(float64); int(total) != 1 {
		t.Errorf("total must stay 1 under format=text; got %v", body["total"])
	}
	if _, ok := body["_meta"].(map[string]any); !ok {
		t.Errorf("_meta must survive format=text")
	}
}

// Pedagogy: unknown format value falls back to json with a warning —
// never a silent shape change.
func TestHandleSearch_FormatUnknown_WarnsAndFallsBackToJSON(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	mustUpsertProject(t, store, "p-fmt-bad", "/tmp/p-fmt-bad", "fmtbad")
	srv.sessionID = "p-fmt-bad"
	srv.sessionRoot = "/tmp/p-fmt-bad"
	seedFormatSearchCorpus(t, store, "p-fmt-bad", 2)

	res, err := srv.handleSearch(context.Background(), makeReq(map[string]any{
		"query":  "FmtHitHandler*",
		"format": "tsv",
	}))
	if err != nil {
		t.Fatalf("handleSearch: %v", err)
	}
	body := decode(t, res)
	if _, ok := body["results"].([]any); !ok {
		t.Errorf("unknown format must fall back to the JSON results array")
	}
	if _, present := body["results_text"]; present {
		t.Errorf("unknown format must not emit results_text")
	}
	meta, _ := body["_meta"].(map[string]any)
	warnings, _ := meta["warnings"].([]any)
	saw := false
	for _, w := range warnings {
		if s, _ := w.(string); strings.Contains(s, `format="tsv"`) {
			saw = true
		}
	}
	if !saw {
		t.Errorf("unknown format must surface a warning naming the value; got %v", warnings)
	}
}
