// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// #657: every tool response — over stdio AND HTTP — carries a
// correlation ID. ServeHTTP echoes it on the X-Request-ID header;
// the handler wrapper stamps it into _meta.request_id.

func TestNewRequestID_IsUUIDv7(t *testing.T) {
	id := newRequestID()
	u, err := uuid.Parse(id)
	if err != nil {
		t.Fatalf("newRequestID() = %q, not a valid UUID: %v", id, err)
	}
	if u.Version() != 7 {
		t.Errorf("newRequestID() version = %d, want 7", u.Version())
	}
}

func TestSanitizeRequestID(t *testing.T) {
	cases := []struct {
		name      string
		raw       string
		wantSame  bool // true: raw passes through; false: a fresh ID is minted
	}{
		{"valid passthrough", "router-abc-123", true},
		{"empty mints fresh", "", false},
		{"too long mints fresh", strings.Repeat("a", maxRequestIDLen+1), false},
		{"crlf mints fresh", "abc\r\nInjected: header", false},
		{"control char mints fresh", "abc\x00def", false},
		{"non-ascii mints fresh", "abcédef", false},
		{"max length passes", strings.Repeat("a", maxRequestIDLen), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := sanitizeRequestID(c.raw)
			if got == "" {
				t.Fatal("sanitizeRequestID returned empty string")
			}
			if c.wantSame && got != c.raw {
				t.Errorf("sanitizeRequestID(%q) = %q, want passthrough", c.raw, got)
			}
			if !c.wantSame && got == c.raw {
				t.Errorf("sanitizeRequestID(%q) passed junk through unchanged", c.raw)
			}
		})
	}
}

func TestInjectRequestID_SuccessShape(t *testing.T) {
	res := &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: `{"result":1,"_meta":{"latency_ms":3}}`}},
	}
	injectRequestID(res, "rid-xyz")
	var got map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &got); err != nil {
		t.Fatalf("result no longer valid JSON: %v", err)
	}
	meta := got["_meta"].(map[string]any)
	if meta["request_id"] != "rid-xyz" {
		t.Errorf("_meta.request_id = %v, want rid-xyz", meta["request_id"])
	}
	if meta["latency_ms"].(float64) != 3 {
		t.Errorf("injection clobbered existing _meta fields: %v", meta)
	}
}

func TestInjectRequestID_NoMetaCreatesIt(t *testing.T) {
	res := &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: `{"result":1}`}},
	}
	injectRequestID(res, "rid-xyz")
	var got map[string]any
	json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &got)
	meta, ok := got["_meta"].(map[string]any)
	if !ok || meta["request_id"] != "rid-xyz" {
		t.Errorf("injection did not create _meta.request_id: %v", got)
	}
}

func TestInjectRequestID_NonJSONIsNoop(t *testing.T) {
	res := &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: "plain error text"}},
	}
	injectRequestID(res, "rid-xyz") // must not panic
	if res.Content[0].(*mcp.TextContent).Text != "plain error text" {
		t.Errorf("non-JSON content was mutated: %q", res.Content[0].(*mcp.TextContent).Text)
	}
}

func TestWithRequestID_StdioMintsID(t *testing.T) {
	srv, _, _ := newTestServer(t)
	inner := func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: `{"_meta":{}}`}},
		}, nil
	}
	wrapped := srv.withRequestID(inner)
	// Bare context — no HTTP header, the stdio path.
	res, err := wrapped(context.Background(), makeReq(nil))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &got)
	rid, _ := got["_meta"].(map[string]any)["request_id"].(string)
	if _, err := uuid.Parse(rid); err != nil {
		t.Errorf("stdio call _meta.request_id = %q, want minted UUID: %v", rid, err)
	}
}

func TestWithRequestID_HTTPUsesContextID(t *testing.T) {
	srv, _, _ := newTestServer(t)
	inner := func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: `{"_meta":{}}`}},
		}, nil
	}
	wrapped := srv.withRequestID(inner)
	ctx := withRequestIDContext(context.Background(), "from-router")
	res, _ := wrapped(ctx, makeReq(nil))
	var got map[string]any
	json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &got)
	if rid := got["_meta"].(map[string]any)["request_id"]; rid != "from-router" {
		t.Errorf("_meta.request_id = %v, want from-router (ctx ID must win)", rid)
	}
}

func TestServeHTTP_EchoesSuppliedRequestID(t *testing.T) {
	srv, _, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/list", strings.NewReader("{}"))
	req.Header.Set("X-Request-ID", "router-trace-42")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Request-ID"); got != "router-trace-42" {
		t.Errorf("X-Request-ID response header = %q, want router-trace-42", got)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response not JSON: %v\n%s", err, rec.Body.String())
	}
	meta, _ := body["_meta"].(map[string]any)
	if meta == nil || meta["request_id"] != "router-trace-42" {
		t.Errorf("_meta.request_id = %v, want router-trace-42", meta)
	}
}

func TestServeHTTP_MintsRequestIDWhenAbsent(t *testing.T) {
	srv, _, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/list", strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	hdr := rec.Header().Get("X-Request-ID")
	if _, err := uuid.Parse(hdr); err != nil {
		t.Errorf("X-Request-ID header = %q, want minted UUID: %v", hdr, err)
	}
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	meta, _ := body["_meta"].(map[string]any)
	if meta == nil || meta["request_id"] != hdr {
		t.Errorf("_meta.request_id %v != X-Request-ID header %q", meta["request_id"], hdr)
	}
}

func TestServeHTTP_RejectsJunkRequestID(t *testing.T) {
	srv, _, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/list", strings.NewReader("{}"))
	req.Header.Set("X-Request-ID", "evil\r\nX-Injected: 1")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	hdr := rec.Header().Get("X-Request-ID")
	if strings.ContainsAny(hdr, "\r\n") {
		t.Fatalf("CRLF survived into response header: %q", hdr)
	}
	if _, err := uuid.Parse(hdr); err != nil {
		t.Errorf("junk X-Request-ID not replaced with a fresh UUID: %q", hdr)
	}
}
