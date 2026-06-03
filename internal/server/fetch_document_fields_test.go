// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// #766: a fetched Document's symbol fields were sloppy — `search` returned
// an empty snippet (the snippet path byte-seeks a file, but a Document has
// no on-disk file; its text lives in Docstring), `symbol` echoed the full
// text in *both* `source` and `docstring` (doubled payload), and
// `signature` was a third copy of the URL.
func TestHandleFetch_DocumentFields_SnippetSignatureNoDoubling(t *testing.T) {
	t.Parallel()
	srv, _ := fetchTestSetup(t)
	srv.fetchAllowLoopback = true

	body := "# Quark Telemetry Guide\n\n" +
		"The quark telemetry subsystem streams sensor frames over the bus.\n" +
		"Calibration happens once per boot. See the bus protocol appendix.\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/markdown")
		w.Write([]byte(body))
	}))
	defer upstream.Close()

	fetchRes, err := srv.handleFetch(context.Background(), makeReq(fetchArgs(upstream.URL)))
	if err != nil {
		t.Fatalf("handleFetch: %v", err)
	}
	fm := decode(t, fetchRes)
	docID, _ := fm["id"].(string)
	if docID == "" {
		t.Fatalf("fetch returned no id: %v", fm)
	}

	// 1. search must return a non-empty snippet for the Document — the
	//    snippet now sources from Docstring for Document-kind hits.
	searchRes, err := srv.handleSearch(context.Background(), makeReq(map[string]any{
		"query": "quark telemetry", "kind": "Document", "project": "p",
	}))
	if err != nil {
		t.Fatalf("handleSearch: %v", err)
	}
	sm := decode(t, searchRes)
	results, _ := sm["results"].([]any)
	if len(results) == 0 {
		t.Fatalf("search returned no Document results: %v", sm)
	}
	row, _ := results[0].(map[string]any)
	if snip, _ := row["snippet"].(string); strings.TrimSpace(snip) == "" {
		t.Errorf("search snippet is empty for a Document hit — should preview the Docstring text:\n  row: %v", row)
	}
	// signature should be the title, not a third copy of the URL.
	if sig, _ := row["signature"].(string); sig != "Quark Telemetry Guide" {
		t.Errorf("Document signature = %q, want the page title %q (not the URL)", sig, "Quark Telemetry Guide")
	}

	// 2. symbol must return the text in `source` once — `docstring` must
	//    not duplicate it.
	symRes, err := srv.handleSymbol(context.Background(), makeReq(map[string]any{
		"id": docID, "project": "p",
	}))
	if err != nil {
		t.Fatalf("handleSymbol: %v", err)
	}
	ym := decode(t, symRes)
	src, _ := ym["source"].(string)
	doc, _ := ym["docstring"].(string)
	if !strings.Contains(src, "quark telemetry subsystem") {
		t.Errorf("symbol `source` should carry the Document text; got %q", src)
	}
	if doc != "" {
		t.Errorf("symbol `docstring` should be empty for a Document — its text is in `source`, echoing both doubles the payload; got %q", doc)
	}
}
