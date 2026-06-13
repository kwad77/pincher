// SPDX-License-Identifier: MIT

package server

import (
	"net/http"
	"testing"
)

func TestServeHTTP_BareToolPathAliasesV1ToolPath_2059(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)

	for _, path := range []string{"/search", "/v1/search"} {
		w := httpPost(t, srv, path, `{"query":"definitely-no-matches"}`)
		if w.Code == http.StatusNotFound {
			t.Fatalf("POST %s: status %d, want routed tool response; body=%s", path, w.Code, w.Body.String())
		}
		if got := w.Body.String(); containsAll(got, "unknown tool", "search") {
			t.Fatalf("POST %s should reach the search handler, got dispatcher error: %s", path, got)
		}
	}
}

func TestServeHTTP_BareUnknownPathReportsBareToolName_2059(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)

	w := httpPost(t, srv, "/no-such-tool", `{}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("POST /no-such-tool: status %d, want 404; body=%s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); !containsAll(got, "unknown tool", "no-such-tool") || containsAll(got, `"/no-such-tool"`) {
		t.Fatalf("POST /no-such-tool body should report the normalized tool name; got %s", got)
	}
}
