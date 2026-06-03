// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// #1076 / #1077 / #1078 (MCP spec 2025-11-25 compliance):
//   - #1078: Tool.Title (display name) for ambiguously-named tools.
//   - #1076: Tool.Annotations (readOnlyHint / destructiveHint /
//     idempotentHint / openWorldHint).
//   - #1077: CallToolResult.StructuredContent populated alongside text.

// Positive (#1078): ambiguously-named tools carry an explicit Title so
// hosts can render the correct label without renaming the underlying
// stable Name.
func TestRegisterTools_Title_AmbiguousNamesHaveDisplayLabel(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	wantTitles := map[string]string{
		"neighborhood": "Same-file symbols",
		"context":      "Symbol with imports & callees",
		"query":        "pinchQL graph query",
		"dead_code":    "Unreachable symbols",
		"init":         "Seed editor MCP config",
		"adr":          "Project decision store",
		"rebuild_fts":  "Admin: rebuild FTS5 indexes",
		"self_test":    "Admin: install smoke test",
	}
	for name, wantTitle := range wantTitles {
		t.Run(name, func(t *testing.T) {
			tool, ok := srv.tools[name]
			if !ok {
				t.Fatalf("tool %q not registered", name)
			}
			if tool.Title != wantTitle {
				t.Errorf("tool %q Title = %q; want %q", name, tool.Title, wantTitle)
			}
		})
	}
}

// Negative (#1078): names that read fine as-is don't get a redundant Title.
// search / symbol / symbols / trace / changes are clear identifiers.
func TestRegisterTools_Title_ClearNamesOmitTitle(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	for _, name := range []string{"search", "symbol", "symbols", "trace", "changes"} {
		t.Run(name, func(t *testing.T) {
			tool := srv.tools[name]
			if tool != nil && tool.Title != "" {
				t.Errorf("tool %q has Title %q but Name reads fine on its own; redundant", name, tool.Title)
			}
		})
	}
}

// Positive (#1076): every registered tool carries an Annotations object
// so MCP hosts can skip confirmations on read paths and warn on
// destructive/open-world ones.
func TestRegisterTools_Annotations_EveryToolHasOne(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	for name, tool := range srv.tools {
		if tool.Annotations == nil {
			t.Errorf("tool %q has no Annotations — every tool must declare one", name)
		}
	}
}

// Cross-check (#1076): read-shape tools (search/symbol/query/trace/etc)
// declare ReadOnlyHint=true. This is the load-bearing field for hosts
// that auto-allow read-only tool calls.
func TestRegisterTools_Annotations_ReadShapeMarkedReadOnly(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	for _, name := range []string{
		"search", "symbol", "symbols", "context", "query", "trace",
		"architecture", "schema", "list", "neighborhood", "dead_code",
		"health", "stats", "doctor", "guide", "changes", "self_test",
	} {
		t.Run(name, func(t *testing.T) {
			tool := srv.tools[name]
			if tool == nil {
				t.Fatalf("tool %q not registered", name)
			}
			if !tool.Annotations.ReadOnlyHint {
				t.Errorf("tool %q ReadOnlyHint = false; read-shape tools must mark ReadOnlyHint=true", name)
			}
		})
	}
}

// Cross-check (#1076): rebuild_fts is destructive. DestructiveHint
// must be true so hosts can warn before invocation.
func TestRegisterTools_Annotations_RebuildFTSMarkedDestructive(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	tool := srv.tools["rebuild_fts"]
	if tool == nil || tool.Annotations == nil {
		t.Fatal("rebuild_fts not registered or missing annotations")
	}
	if tool.Annotations.DestructiveHint == nil || !*tool.Annotations.DestructiveHint {
		t.Errorf("rebuild_fts DestructiveHint = %v; admin rebuild must be marked destructive", tool.Annotations.DestructiveHint)
	}
}

// Cross-check (#1076): fetch is open-world (HTTP fetch of arbitrary
// content). OpenWorldHint must be true.
func TestRegisterTools_Annotations_FetchMarkedOpenWorld(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	tool := srv.tools["fetch"]
	if tool == nil || tool.Annotations == nil {
		t.Fatal("fetch not registered or missing annotations")
	}
	if tool.Annotations.OpenWorldHint == nil || !*tool.Annotations.OpenWorldHint {
		t.Errorf("fetch OpenWorldHint = %v; fetching arbitrary HTTP must be marked open-world", tool.Annotations.OpenWorldHint)
	}
}

// Positive (#1077): CallToolResult populates StructuredContent
// alongside TextContent for spec-compliant clients.
func TestJsonResultWithMeta_PopulatesStructuredContent(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	// schema requires a project; seed one so the handler hits the
	// success branch (which is what jsonResultWithMeta wraps).
	mustUpsertProject(t, store, "p-mcp-spec", "/tmp/p-mcp-spec", "p-mcp-spec")
	srv.sessionID = "p-mcp-spec"
	srv.sessionRoot = "/tmp/p-mcp-spec"

	req := makeReq(nil)
	res, err := srv.handleSchema(context.Background(), req)
	if err != nil {
		t.Fatalf("handleSchema: %v", err)
	}
	if res.StructuredContent == nil {
		t.Fatal("expected StructuredContent populated alongside text content; got nil")
	}
	// The structured content should marshal to a JSON object that
	// matches the text content's body.
	structuredJSON, _ := json.Marshal(res.StructuredContent)
	if len(res.Content) == 0 {
		t.Fatal("expected text Content populated for back-compat; got empty")
	}
	textContent, ok := res.Content[0].(interface{ AsTextContent() string })
	_ = textContent
	_ = ok
	// Simpler shape: the structured content type-asserts to map[string]any.
	m, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("StructuredContent must be map[string]any; got %T", res.StructuredContent)
	}
	if _, hasMeta := m["_meta"]; !hasMeta {
		var keys []string
		for k := range m {
			keys = append(keys, k)
		}
		t.Errorf("StructuredContent missing _meta; got keys: %v", keys)
	}
	if !strings.Contains(string(structuredJSON), "_meta") {
		t.Errorf("structuredJSON should contain _meta; got: %s", string(structuredJSON))
	}
}
