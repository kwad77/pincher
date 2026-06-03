// SPDX-License-Identifier: MIT

package server

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

// v0.30 contract tests for the umbrella-close batch
// (#550/#551/#554/#556 + close #519). E2E items (#520/#524/#525)
// are deferred — see CHANGELOG note.

// #550: keyboard shortcuts. Wiring: a keydown handler with the leader
// pattern (g + s/p/o/a/h), '/' to focus search, Esc closes panel,
// j/k navigate project cards. Not typing-target-aware would steal
// '/' from the search box; the test asserts the typing-guard is in.
func TestDashboardJS_KeyboardShortcuts(t *testing.T) {
	t.Parallel()
	js := renderDashboardJS("")
	for _, needle := range []string{
		"function _isTypingTarget",
		`'g'`,
		`_kbdLeader`,
		`document.addEventListener('keydown'`,
		// j/k vim-style navigation.
		`'j' || ev.key === 'k'`,
		// Search shortcut.
		`ev.key === '/'`,
		// kbd-focused class for the cursor highlight.
		"kbd-focused",
	} {
		if !strings.Contains(js, needle) {
			t.Errorf("dashboard JS missing %q — #550 keyboard shortcut wiring incomplete", needle)
		}
	}
	css := renderDashboardCSS()
	if !strings.Contains(css, ".proj-card.kbd-focused") {
		t.Errorf("dashboard CSS missing .proj-card.kbd-focused — #550 keyboard cursor invisible")
	}
}

// #551: CSV/JSON export buttons on Projects + Sessions tables.
// exportTable(format, kind) helper + buttons wired via data-action.
func TestDashboardJS_ExportButtons(t *testing.T) {
	t.Parallel()
	js := renderDashboardJS("")
	for _, needle := range []string{
		"function exportTable",
		"function _downloadExport",
		"new Blob",
		"createObjectURL",
		// Both formats supported (CSV branch is the explicit check;
		// JSON is the else branch with the application/json Blob type).
		"'csv'",
		"application/json",
		// CSV escaping.
		"escCsv",
	} {
		if !strings.Contains(js, needle) {
			t.Errorf("dashboard JS missing %q — #551 export incomplete", needle)
		}
	}
	html := renderDashboard("")
	for _, needle := range []string{
		`data-action="exportTable" data-args='["csv","projects"]'`,
		`data-action="exportTable" data-args='["json","projects"]'`,
		`data-action="exportTable" data-args='["csv","sessions"]'`,
		`data-action="exportTable" data-args='["json","sessions"]'`,
	} {
		if !strings.Contains(html, needle) {
			t.Errorf("dashboard HTML missing %q — #551 export button missing", needle)
		}
	}
}

// #554: deep links — hash format #<tab>/<projectID>. openDetail
// updates hash via history.replaceState; closeDetail strips the
// project ID; restore on load + on hashchange.
func TestDashboardJS_DeepLinks(t *testing.T) {
	t.Parallel()
	js := renderDashboardJS("")
	for _, needle := range []string{
		"function _parseHash",
		"function _restoreFromHash",
		"hashchange",
		// Must use replaceState (not pushState) to avoid polluting history.
		`history.replaceState(null, '', '#projects/'`,
		`history.replaceState(null, '', '#projects')`,
	} {
		if !strings.Contains(js, needle) {
			t.Errorf("dashboard JS missing %q — #554 deep link wiring incomplete", needle)
		}
	}
}

// #556: dashboard.js + dashboard.css must respond with ETag and
// honor If-None-Match (304), and respect Accept-Encoding: gzip.
func TestDashboardAssets_ETagAndGzip(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	for _, path := range []string{"/v1/dashboard.js", "/v1/dashboard.css"} {
		t.Run(path, func(t *testing.T) {
			// First request — record the ETag.
			r1 := httptest.NewRequest("GET", path, nil)
			w1 := httptest.NewRecorder()
			srv.ServeHTTP(w1, r1)
			if w1.Code != 200 {
				t.Fatalf("first %s: status %d, want 200", path, w1.Code)
			}
			etag := w1.Header().Get("ETag")
			if etag == "" {
				t.Fatalf("%s: missing ETag header (#556)", path)
			}
			if vary := w1.Header().Get("Vary"); !strings.Contains(vary, "Accept-Encoding") {
				t.Errorf("%s: Vary header missing Accept-Encoding — proxies will mis-cache", path)
			}

			// Second request with If-None-Match — must be 304 + empty body.
			r2 := httptest.NewRequest("GET", path, nil)
			r2.Header.Set("If-None-Match", etag)
			w2 := httptest.NewRecorder()
			srv.ServeHTTP(w2, r2)
			if w2.Code != 304 {
				t.Errorf("%s: revalidation status %d, want 304", path, w2.Code)
			}
			if w2.Body.Len() != 0 {
				t.Errorf("%s: 304 response has %d bytes of body, want 0", path, w2.Body.Len())
			}

			// Third request with Accept-Encoding: gzip — body must be
			// gzip-compressed and decompress back to the original.
			r3 := httptest.NewRequest("GET", path, nil)
			r3.Header.Set("Accept-Encoding", "gzip")
			w3 := httptest.NewRecorder()
			srv.ServeHTTP(w3, r3)
			if w3.Code != 200 {
				t.Fatalf("%s gzip: status %d, want 200", path, w3.Code)
			}
			if enc := w3.Header().Get("Content-Encoding"); enc != "gzip" {
				t.Errorf("%s: Content-Encoding %q, want gzip", path, enc)
			}
			gz, err := gzip.NewReader(bytes.NewReader(w3.Body.Bytes()))
			if err != nil {
				t.Fatalf("%s: gzip reader error: %v", path, err)
			}
			defer gz.Close()
			decoded, _ := io.ReadAll(gz)
			if len(decoded) == 0 {
				t.Errorf("%s: decompressed body empty", path)
			}
			// gzip should compress meaningfully on a >10 KB JS payload
			// (the outer middleware handles the actual compression — see
			// writeAssetWithETagAndGzip docstring for why we don't double-gzip).
			if path == "/v1/dashboard.js" && len(w3.Body.Bytes()) > len(decoded)/2 {
				t.Errorf("%s: gzip body (%d) >50%% of uncompressed (%d) — outer middleware not applying compression?",
					path, len(w3.Body.Bytes()), len(decoded))
			}
		})
	}
}
