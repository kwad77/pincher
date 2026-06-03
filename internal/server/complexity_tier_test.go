// SPDX-License-Identifier: MIT

package server

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// #650: per-tool complexity tier classification. Two surfaces, same data:
//   - x-pincher-tier in the OpenAPI spec (planning-time)
//   - _meta.complexity_tier on every response (call-time)
// Routers (detour-shape model routers in particular) consume to decide
// which model handles the agent step that consumes the response.

// TestComplexityTier_EveryRegisteredToolClassified is the gate. Adding
// a new tool requires adding a tier; CI fails otherwise.
func TestComplexityTier_EveryRegisteredToolClassified(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	for name := range srv.tools {
		if tier := toolComplexityTier(name); tier == "" {
			t.Errorf("tool %q has no complexity_tier classification — add it to toolComplexityTiers in server.go", name)
		}
	}
	// Inverse: every classified tool must be registered (catches stale
	// classifications when a tool is removed).
	for name := range toolComplexityTiers {
		if _, ok := srv.tools[name]; !ok {
			t.Errorf("complexity_tier for %q exists but tool is not registered — drop the entry or re-add the tool", name)
		}
	}
}

// TestComplexityTier_OnlyKnownTierValues guards the vocabulary.
// Routers will rely on the enum being stable.
func TestComplexityTier_OnlyKnownTierValues(t *testing.T) {
	t.Parallel()
	allowed := map[string]bool{"lite": true, "standard": true, "heavy": true}
	for tool, tier := range toolComplexityTiers {
		if !allowed[tier] {
			t.Errorf("tool %q classified as %q — must be one of lite/standard/heavy", tool, tier)
		}
	}
}

// TestComplexityTier_MetaEnvelopeCarriesTier verifies the per-response
// injection happens for a representative tool.
func TestComplexityTier_MetaEnvelopeCarriesTier(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	cases := []struct {
		tool string
		want string
	}{
		{"search", "lite"},
		{"context", "standard"},
		{"guide", "heavy"},
	}
	for _, c := range cases {
		t.Run(c.tool, func(t *testing.T) {
			result := srv.jsonResultWithMeta(
				map[string]any{"ok": true},
				time.Now(), c.tool,
				map[string]any{}, 0,
			)
			text := result.Content[0].(*mcp.TextContent).Text
			var parsed map[string]any
			if err := json.Unmarshal([]byte(text), &parsed); err != nil {
				t.Fatalf("response not JSON: %v", err)
			}
			meta := parsed["_meta"].(map[string]any)
			got, ok := meta["complexity_tier"].(string)
			if !ok {
				t.Fatalf("_meta.complexity_tier missing for tool %q", c.tool)
			}
			if got != c.want {
				t.Errorf("tool %q: complexity_tier=%q, want %q", c.tool, got, c.want)
			}
		})
	}
}

// TestComplexityTier_OpenAPIAnnotationPresent verifies x-pincher-tier
// shows up in the OpenAPI spec for every tool. Planning-time consumers
// (codegen, router config tools) use this without needing to invoke.
func TestComplexityTier_OpenAPIAnnotationPresent(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	req := httptest.NewRequest("GET", "/v1/openapi.json", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("openapi.json returned %d", rr.Code)
	}
	var spec map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &spec); err != nil {
		t.Fatalf("openapi.json not JSON: %v", err)
	}
	paths, _ := spec["paths"].(map[string]any)
	if paths == nil {
		t.Fatalf("openapi.json missing paths")
	}
	for tool := range toolComplexityTiers {
		path := "/v1/" + tool
		entry, ok := paths[path].(map[string]any)
		if !ok {
			t.Errorf("openapi paths missing %s", path)
			continue
		}
		// health is the one GET-only entry (hardcoded as a probe in
		// openAPISpec). Every other tool gets a POST entry. The tier
		// annotation lives on whichever method shape the spec emits.
		var op map[string]any
		if get, ok := entry["get"].(map[string]any); ok {
			op = get
		} else if post, ok := entry["post"].(map[string]any); ok {
			op = post
		} else {
			t.Errorf("openapi paths[%s] has neither 'get' nor 'post' key", path)
			continue
		}
		got, ok := op["x-pincher-tier"].(string)
		if !ok {
			t.Errorf("openapi paths[%s] missing x-pincher-tier", path)
			continue
		}
		if got != toolComplexityTiers[tool] {
			t.Errorf("openapi paths[%s] x-pincher-tier=%q, want %q", path, got, toolComplexityTiers[tool])
		}
	}
}

// TestComplexityTier_AdvertisedAsCapability verifies the capability
// tag is present (gate-tested for runtime backing in capability_test.go).
func TestComplexityTier_AdvertisedAsCapability(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	found := false
	for _, c := range srv.capabilities {
		if c == "complexity_tier" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("complexity_tier feature shipped but not in capabilities advertisement")
	}
}
