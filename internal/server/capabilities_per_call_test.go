// SPDX-License-Identifier: MIT

package server

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// #1087: tests for the per-call capabilities opt-out (env-gated)
// and the GET /v1/capabilities companion endpoint.

func TestParseCapabilitiesEnv_Cases(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", true},          // default
		{"on", true},        // explicit on
		{"true", true},      // explicit on
		{"1", true},         // explicit on
		{"off", false},      // canonical opt-out
		{"OFF", false},      // case-insensitive
		{"  off  ", false},  // whitespace-tolerant
		{"false", false},    // opt-out alias
		{"0", false},        // opt-out alias
		{"none", false},     // opt-out alias
		{"no", false},       // opt-out alias
		{"banana", true},    // unknown defaults to on (failure-as-pedagogy: typo'd opt-out keeps current behavior)
	}
	for _, c := range cases {
		got := parseCapabilitiesEnv(c.in)
		if got != c.want {
			t.Errorf("parseCapabilitiesEnv(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestHTTPCapabilitiesEndpoint_ReturnsServerSlice pins the GET /v1/
// capabilities companion endpoint: returns the same slice that's
// stamped on per-call _meta when the env opt-out isn't set.
func TestHTTPCapabilitiesEndpoint_ReturnsServerSlice(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)

	req := httptest.NewRequest("GET", "/v1/capabilities", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("GET /v1/capabilities returned %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response not JSON: %v", err)
	}
	caps, ok := resp["capabilities"].([]any)
	if !ok {
		t.Fatalf("response missing capabilities array; got %v", resp)
	}
	if len(caps) == 0 {
		t.Error("capabilities array is empty; server should advertise at least schema_vN")
	}
	// Must match the in-memory slice exactly — that's the whole
	// point of the endpoint (drop-in alternative to per-call _meta).
	gotSet := make(map[string]bool, len(caps))
	for _, c := range caps {
		gotSet[c.(string)] = true
	}
	for _, want := range srv.capabilities {
		if !gotSet[want] {
			t.Errorf("endpoint missing capability %q; got %v", want, gotSet)
		}
	}
}

// TestHTTPCapabilitiesEndpoint_PostRejected pins the GET-only
// contract per #609. POST must return 404/405 (the catch-all
// "method not allowed" path), not 200.
func TestHTTPCapabilitiesEndpoint_PostRejected(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	req := httptest.NewRequest("POST", "/v1/capabilities", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != 404 && rr.Code != 405 {
		t.Errorf("expected 404/405 on POST; got %d", rr.Code)
	}
}

// TestServer_IncludeCapabilitiesPerCall_DefaultOn pins back-compat:
// a fresh Server (no env var set) MUST have includeCapabilitiesPerCall
// true so existing routers reading _meta.capabilities keep working.
func TestServer_IncludeCapabilitiesPerCall_DefaultOn(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	if !srv.includeCapabilitiesPerCall {
		t.Error("default server has includeCapabilitiesPerCall=false; back-compat broken (router reading _meta.capabilities will get nil)")
	}
}
