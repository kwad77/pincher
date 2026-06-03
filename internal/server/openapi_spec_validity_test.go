// SPDX-License-Identifier: MIT

package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// #1262 prerequisite: OAS 3.1.0 spec-validity gate. The existing
// openapi_parity_test.go suite proves the spec MENTIONS every
// registered tool; this file proves the spec is structurally well-
// formed enough that a downstream code generator (openapi-generator,
// swagger-codegen) can actually consume it without choking.
//
// Without this gate, a future tool registration that emits a
// malformed path entry (missing `responses`, undeclared method,
// $ref to a nonexistent component) ships green tests but breaks
// SDK generation downstream — exactly the failure mode #1262 calls
// out as the trust-erosion path for the typed-SDK story.
//
// Pure Go, no external deps. Catches the structural regressions
// that the bigger openapi-generator-in-CI step (when #1262 ships
// full) would also catch but earlier in the loop. Hardening-shape
// addition to v0.69 ahead of the full SDK-generation work.

// TestOpenAPISpec_TopLevelInvariants pins the required top-level
// fields per OAS 3.1.0 §4: openapi, info, paths. Without all three,
// no downstream tool parses the spec.
func TestOpenAPISpec_TopLevelInvariants(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	spec := fetchOpenAPISpec(t, srv)

	if ver, _ := spec["openapi"].(string); !strings.HasPrefix(ver, "3.") {
		t.Errorf("openapi version = %v, want 3.x prefix per OAS 3.1.0 spec", spec["openapi"])
	}
	info, ok := spec["info"].(map[string]any)
	if !ok {
		t.Fatal("spec missing required `info` object per OAS 3.1.0 §4")
	}
	if title, _ := info["title"].(string); title == "" {
		t.Error("info.title is required (OAS 3.1.0 §4.8.2) — downstream SDK generators use it as the package title")
	}
	if version, _ := info["version"].(string); version == "" {
		t.Error("info.version is required (OAS 3.1.0 §4.8.2) — downstream SDK generators use it as the published version")
	}
	if _, ok := spec["paths"].(map[string]any); !ok {
		t.Fatal("spec missing required `paths` object per OAS 3.1.0 §4")
	}
}

// TestOpenAPISpec_EveryPathHasMethodWithResponses pins per-path
// structural validity. Each path item must have at least one HTTP
// method, and each method must have a `responses` block — without
// it, openapi-generator refuses to emit a client function for the
// endpoint.
func TestOpenAPISpec_EveryPathHasMethodWithResponses(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	spec := fetchOpenAPISpec(t, srv)
	paths := spec["paths"].(map[string]any)

	validMethods := map[string]bool{
		"get": true, "post": true, "put": true, "delete": true,
		"patch": true, "head": true, "options": true, "trace": true,
	}

	for path, pathItemRaw := range paths {
		pathItem, ok := pathItemRaw.(map[string]any)
		if !ok {
			t.Errorf("path %q is not an object", path)
			continue
		}
		methodCount := 0
		for key, opRaw := range pathItem {
			// Skip non-method keys (parameters, summary, description,
			// servers, $ref, extensions).
			if !validMethods[strings.ToLower(key)] {
				continue
			}
			methodCount++
			op, ok := opRaw.(map[string]any)
			if !ok {
				t.Errorf("path %q method %q is not an object", path, key)
				continue
			}
			responses, ok := op["responses"].(map[string]any)
			if !ok || len(responses) == 0 {
				t.Errorf("path %q method %q missing `responses` block (OAS 3.1.0 §4.8.16) — downstream SDK generators won't emit a client function for it", path, key)
			}
		}
		if methodCount == 0 {
			t.Errorf("path %q has zero HTTP methods declared — every path item needs ≥1 method", path)
		}
	}
}

// TestOpenAPISpec_ComponentRefsResolve pins that every `$ref` in
// the document resolves to an actual entry under `components/`.
// Dangling refs are the most common openapi-generator failure mode
// — the generator silently produces an SDK with broken types when
// a referenced schema doesn't exist.
func TestOpenAPISpec_ComponentRefsResolve(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	spec := fetchOpenAPISpec(t, srv)

	components, _ := spec["components"].(map[string]any)
	refs := collectRefs(spec)
	if len(refs) == 0 {
		// Acceptable: the spec might inline everything. Not a failure.
		return
	}

	for ref := range refs {
		// $ref values must start with "#/components/" for local refs;
		// external refs ("https://...") are out of scope for this gate.
		if !strings.HasPrefix(ref, "#/components/") {
			continue
		}
		// Walk the path: #/components/schemas/Foo → components.schemas.Foo
		segments := strings.Split(strings.TrimPrefix(ref, "#/"), "/")
		var cur any = components
		// Replace the leading "components" with the components map we
		// already pulled — saves one segment of traversal.
		segments = segments[1:]
		for i, seg := range segments {
			m, ok := cur.(map[string]any)
			if !ok {
				t.Errorf("$ref %q broke at segment %d (%q) — parent is not an object", ref, i, seg)
				cur = nil
				break
			}
			cur, ok = m[seg]
			if !ok {
				t.Errorf("$ref %q points at nonexistent component (missing segment %q at depth %d)", ref, seg, i)
				cur = nil
				break
			}
		}
	}
}

// fetchOpenAPISpec hits /v1/openapi.json on the test server and
// returns the parsed JSON. Tiny shared helper to avoid four
// near-identical setup blocks across the tests above.
func fetchOpenAPISpec(t *testing.T, srv *Server) map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1/openapi.json", nil)
	srv.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("GET /v1/openapi.json returned %d: %s", w.Code, w.Body.String())
	}
	var spec map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &spec); err != nil {
		t.Fatalf("openapi.json parse: %v", err)
	}
	return spec
}

// collectRefs walks the spec object and returns every `$ref` string
// value found, deduplicated. Used by the ref-resolve test to know
// which refs to check.
func collectRefs(v any) map[string]bool {
	out := map[string]bool{}
	var walk func(any)
	walk = func(n any) {
		switch t := n.(type) {
		case map[string]any:
			for k, child := range t {
				if k == "$ref" {
					if s, ok := child.(string); ok {
						out[s] = true
					}
				}
				walk(child)
			}
		case []any:
			for _, c := range t {
				walk(c)
			}
		}
	}
	walk(v)
	return out
}
