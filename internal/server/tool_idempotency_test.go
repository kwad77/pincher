// SPDX-License-Identifier: MIT

package server

import (
	"encoding/json"
	"net/http/httptest"
	"sort"
	"testing"
)

// #659: per-tool x-pincher-idempotent declaration. Routers retry on
// failure; without an explicit declaration they have to assume
// "not idempotent" and skip retries conservatively. Pre-fix the
// declaration was implicit in the code path; this gate test ensures
// every registered tool has an entry and the OpenAPI spec stamps
// x-pincher-idempotent on every endpoint.

// Every registered tool MUST have an entry in toolIdempotent — same
// shape as toolComplexityTiers (#650). When a new tool lands, this
// gate fails until the author classifies it.
func TestToolIdempotency_EveryToolClassified(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)

	var missing []string
	for name := range srv.handlers {
		if _, ok := toolIdempotent[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("tools missing idempotency declaration (add to toolIdempotent map):\n  - %v",
			missing)
	}
}

// OpenAPI spec must stamp x-pincher-idempotent on every tool
// endpoint. Pre-fix consumers had no machine-readable signal.
func TestOpenAPI_EveryToolStampsIdempotency(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1/openapi.json", nil)
	srv.ServeHTTP(w, r)

	var spec map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &spec); err != nil {
		t.Fatalf("openapi.json parse: %v", err)
	}
	paths := spec["paths"].(map[string]any)

	var missing []string
	for name := range srv.handlers {
		path, ok := paths["/v1/"+name].(map[string]any)
		if !ok {
			continue
		}
		post, ok := path["post"].(map[string]any)
		if !ok {
			continue
		}
		if _, has := post["x-pincher-idempotent"]; !has {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("OpenAPI endpoints missing x-pincher-idempotent stamp: %v\n"+
			"openAPISpec must call toolIsIdempotent(t) for every tool endpoint.",
			missing)
	}
}

// Spot-check the known-not-idempotent set: `index`, `rebuild_fts`,
// `init`, `adr` should all be false (writes / side-effects). Their
// inverse is implied by the gate above + the classification map.
func TestToolIdempotency_KnownWritersAreFalse(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"index", "rebuild_fts", "init", "adr"} {
		if toolIsIdempotent(name) {
			t.Errorf("%s declared idempotent=true; expected false (writes / mutates)", name)
		}
	}
}

func TestToolIdempotency_KnownReadersAreTrue(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"search", "symbol", "trace", "query", "list", "health"} {
		if !toolIsIdempotent(name) {
			t.Errorf("%s declared idempotent=false; expected true (read-only)", name)
		}
	}
}
