// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"testing"
)

// Payload diet (conclusion-density batch defaults): inside a batch
// envelope, `search` and `symbol` sub-queries that don't name a field
// projection get a lean default (batchDefaultFields). Coverage follows
// the positive/negative/control/cross-check pattern:
//
//   positive    — injected defaults produce locator-shaped search rows
//                 and chrome-free (but source-bearing) symbol bodies
//   negative    — explicit fields / fields:"*" / snippet_lines suppress
//                 the injection (graceful degradation: caller args win)
//   control     — standalone (non-batch) handlers are byte-shape
//                 unchanged: full rows, all chrome
//   cross-check — chain mode still selects ids/files off dieted rows

// dietBatchSubResult runs a one-entry batch and returns results[0].result.
func dietBatchSubResult(t *testing.T, srv *Server, projectID string, q map[string]any) map[string]any {
	t.Helper()
	res, err := srv.handleBatch(context.Background(), makeReq(map[string]any{
		"project": projectID,
		"queries": []any{q},
	}))
	if err != nil {
		t.Fatalf("handleBatch: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success; got %s", textOf(t, res))
	}
	entries := batchResults(t, decode(t, res))
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry; got %d: %#v", len(entries), entries)
	}
	if errStr, ok := entries[0]["error"].(string); ok {
		t.Fatalf("sub-query errored: %s", errStr)
	}
	body, ok := entries[0]["result"].(map[string]any)
	if !ok {
		t.Fatalf("entry has no result object: %#v", entries[0])
	}
	return body
}

// firstSearchRow extracts results[0] from a search sub-result body.
func firstSearchRow(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	rows, ok := body["results"].([]any)
	if !ok || len(rows) == 0 {
		t.Fatalf("search returned no rows: %#v", body)
	}
	row, ok := rows[0].(map[string]any)
	if !ok {
		t.Fatalf("results[0] is %T, want object", rows[0])
	}
	return row
}

// Positive: search-in-batch without fields ships locator rows only —
// the injected id,name,kind,file_path projection, no snippet and no
// metadata chrome.
func TestBatchPayloadDiet_SearchDefaultsToLocatorRows(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupComposeTestServer(t)

	body := dietBatchSubResult(t, srv, projectID, map[string]any{
		"tool": "search", "args": map[string]any{"query": "Compute"},
	})
	row := firstSearchRow(t, body)

	for _, want := range []string{"id", "name", "kind", "file_path"} {
		if _, ok := row[want]; !ok {
			t.Errorf("dieted search row missing %q; got keys: %v", want, projectableKeys(row))
		}
	}
	for _, chrome := range []string{"snippet", "qualified_name", "start_line", "start_byte", "extraction_confidence", "language", "score"} {
		if _, ok := row[chrome]; ok {
			t.Errorf("dieted search row still carries %q; got keys: %v", chrome, projectableKeys(row))
		}
	}
}

// Positive: symbol-in-batch without fields keeps the answer payload
// (source, docstring, signature, citation fields) and drops the
// byte-offset / confidence / export chrome.
func TestBatchPayloadDiet_SymbolKeepsSourceDropsChrome(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupComposeTestServer(t)

	body := dietBatchSubResult(t, srv, projectID, map[string]any{
		"tool": "symbol", "args": map[string]any{"id": "compose.go::compose.Compute#Function"},
	})

	src, _ := body["source"].(string)
	if src == "" {
		t.Fatalf("dieted symbol lost its source body: %#v", body)
	}
	for _, want := range []string{"id", "name", "kind", "file_path", "start_line", "end_line", "signature", "docstring"} {
		if _, ok := body[want]; !ok {
			t.Errorf("dieted symbol missing %q; got keys: %v", want, projectableKeys(body))
		}
	}
	for _, chrome := range []string{"start_byte", "end_byte", "qualified_name", "extraction_confidence", "is_exported", "complexity", "return_type", "language"} {
		if _, ok := body[chrome]; ok {
			t.Errorf("dieted symbol still carries %q; got keys: %v", chrome, projectableKeys(body))
		}
	}
}

// Negative: an explicit fields arg wins verbatim — no injection on top.
func TestBatchPayloadDiet_ExplicitFieldsWin(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupComposeTestServer(t)

	body := dietBatchSubResult(t, srv, projectID, map[string]any{
		"tool": "search", "args": map[string]any{"query": "Compute", "fields": "id,snippet"},
	})
	row := firstSearchRow(t, body)

	if _, ok := row["snippet"]; !ok {
		t.Errorf("explicit fields=id,snippet lost its snippet; got keys: %v", projectableKeys(row))
	}
	if _, ok := row["name"]; ok {
		t.Errorf("explicit fields=id,snippet grew an uninvited name field; got keys: %v", projectableKeys(row))
	}
}

// Negative: fields:"*" is the full-payload escape — the standalone
// all-fields shape, with no unknown-field warning.
func TestBatchPayloadDiet_StarEscapesToFullShape(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupComposeTestServer(t)

	res, err := srv.handleBatch(context.Background(), makeReq(map[string]any{
		"project": projectID,
		"queries": []any{
			map[string]any{"tool": "search", "args": map[string]any{"query": "Compute", "fields": "*"}},
			map[string]any{"tool": "symbol", "args": map[string]any{"id": "compose.go::compose.Compute#Function", "fields": "*"}},
		},
	}))
	if err != nil {
		t.Fatalf("handleBatch: %v", err)
	}
	body := decode(t, res)
	entries := batchResults(t, body)

	searchBody, _ := entries[0]["result"].(map[string]any)
	row := firstSearchRow(t, searchBody)
	for _, want := range []string{"qualified_name", "start_line", "snippet"} {
		if _, ok := row[want]; !ok {
			t.Errorf("fields=\"*\" search row missing full-shape key %q; got keys: %v", want, projectableKeys(row))
		}
	}

	symBody, _ := entries[1]["result"].(map[string]any)
	for _, want := range []string{"source", "start_byte", "qualified_name", "extraction_confidence"} {
		if _, ok := symBody[want]; !ok {
			t.Errorf("fields=\"*\" symbol missing full-shape key %q; got keys: %v", want, projectableKeys(symBody))
		}
	}

	// "*" must not leak to the sub-handler as a literal projection —
	// that path would warn about unknown fields.
	for _, e := range entries {
		if meta, ok := e["_meta"].(map[string]any); ok {
			if v2, ok := meta["warnings_v2"].([]any); ok && len(v2) > 0 {
				t.Errorf("fields=\"*\" produced warnings: %#v", v2)
			}
		}
	}
}

// Negative: a search that sizes its snippets is asking for snippets —
// snippet_lines suppresses the injection.
func TestBatchPayloadDiet_SnippetLinesSuppressesInjection(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupComposeTestServer(t)

	body := dietBatchSubResult(t, srv, projectID, map[string]any{
		"tool": "search", "args": map[string]any{"query": "Compute", "snippet_lines": 3},
	})
	row := firstSearchRow(t, body)

	if _, ok := row["snippet"]; !ok {
		t.Errorf("snippet_lines search lost its snippet to the diet; got keys: %v", projectableKeys(row))
	}
}

// Control: standalone (non-batch) handlers are unchanged — full rows
// with all chrome when fields is omitted.
func TestBatchPayloadDiet_StandaloneHandlersUnchanged(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupComposeTestServer(t)

	sres, err := srv.handleSearch(context.Background(), makeReq(map[string]any{
		"query": "Compute", "project": projectID,
	}))
	if err != nil {
		t.Fatalf("handleSearch: %v", err)
	}
	row := firstSearchRow(t, decode(t, sres))
	for _, want := range []string{"id", "name", "qualified_name", "start_line", "snippet", "extraction_confidence"} {
		if _, ok := row[want]; !ok {
			t.Errorf("standalone search row missing %q (diet leaked out of batch); got keys: %v", want, projectableKeys(row))
		}
	}

	yres, err := srv.handleSymbol(context.Background(), makeReq(map[string]any{
		"id": "compose.go::compose.Compute#Function", "project": projectID,
	}))
	if err != nil {
		t.Fatalf("handleSymbol: %v", err)
	}
	sym := decode(t, yres)
	for _, want := range []string{"source", "start_byte", "end_byte", "qualified_name", "is_exported", "complexity"} {
		if _, ok := sym[want]; !ok {
			t.Errorf("standalone symbol missing %q (diet leaked out of batch); got keys: %v", want, projectableKeys(sym))
		}
	}
}

// Cross-check: chain mode still works off dieted rows — the injected
// search projection keeps id (top_id/ids selectors) and file_path
// (files selector), so a dieted upstream feeds a downstream splice.
func TestBatchPayloadDiet_ChainStillSelectsFromDietedRows(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupComposeTestServer(t)

	res, err := srv.handleBatch(context.Background(), makeReq(map[string]any{
		"project": projectID,
		"queries": []any{
			map[string]any{"tool": "search", "args": map[string]any{"query": "Compute", "kind": "Function"}, "quiet": true},
			map[string]any{"tool": "symbol", "from": map[string]any{"query": 0, "select": "top_id"}},
		},
	}))
	if err != nil {
		t.Fatalf("handleBatch: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success; got %s", textOf(t, res))
	}
	entries := batchResults(t, decode(t, res))
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries; got %d", len(entries))
	}
	if skipReason, ok := entries[1]["skipped"].(string); ok {
		t.Fatalf("chained symbol skipped (%s) — diet broke top_id selection", skipReason)
	}
	symBody, _ := entries[1]["result"].(map[string]any)
	if symBody == nil {
		t.Fatalf("chained symbol has no result: %#v", entries[1])
	}
	if src, _ := symBody["source"].(string); src == "" {
		t.Errorf("chained dieted symbol lost source; got keys: %v", projectableKeys(symBody))
	}
}
