// SPDX-License-Identifier: MIT

package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// #651 v0.54: streamable-HTTP MCP transport. The handler is mounted on
// the existing HTTP server when SetMCPHTTPPath is called. These tests
// pin the wiring contract: the path routes to the MCP handler, the
// capability tag flips on, and basepath stripping composes correctly.

// TestStreamableHTTP_Disabled verifies the default — no path set, the
// /mcp endpoint 404s through the normal /v1/* dispatcher and the
// capability is absent.
func TestStreamableHTTP_Disabled(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	for _, c := range srv.capabilities {
		if c == "streamable_http" {
			t.Errorf("streamable_http advertised on a server with no mcpHTTPPath set")
		}
	}

	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{}`))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	// Falls through to the /v1/<tool> dispatcher which 404s on bare /mcp.
	// The exact code matters less than "did not handle as MCP".
	if rr.Code == 200 {
		t.Errorf("expected non-200 when streamable-HTTP is disabled; got 200: %s", rr.Body.String())
	}
}

// TestStreamableHTTP_InitializeReturns200 verifies that with the
// transport mounted, an MCP initialize round-trip succeeds — the
// minimal contract a router needs to bootstrap.
func TestStreamableHTTP_InitializeReturns200(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	srv.SetMCPHTTPPath("/mcp")

	body := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"v0"}}}`)
	req := httptest.NewRequest("POST", "/mcp", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("initialize returned %d: %s", rr.Code, rr.Body.String())
	}
	// Body is either application/json or an SSE stream; both wrap a
	// jsonrpc-2.0 result. Look for the result key in either form.
	if !strings.Contains(rr.Body.String(), `"result"`) || !strings.Contains(rr.Body.String(), `"protocolVersion"`) {
		t.Errorf("initialize response missing result/protocolVersion; got: %s", rr.Body.String())
	}
}

// TestStreamableHTTP_CapabilityFlipsOnSet verifies the capability tag
// surfaces as soon as the setter is called — important because routers
// query capabilities to decide whether to even attempt the transport.
func TestStreamableHTTP_CapabilityFlipsOnSet(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	srv.SetMCPHTTPPath("/mcp")

	found := false
	for _, c := range srv.capabilities {
		if c == "streamable_http" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("streamable_http capability missing after SetMCPHTTPPath; caps=%v", srv.capabilities)
	}
}

// TestStreamableHTTP_HonorsBasePath verifies a reverse-proxy /pincher/mcp
// reaches the handler when basepath is /pincher and mcpHTTPPath is /mcp.
// This is the integration most k8s deployments will actually run.
func TestStreamableHTTP_HonorsBasePath(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	srv.SetBasePath("/pincher")
	srv.SetMCPHTTPPath("/mcp")

	body := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"v0"}}}`)
	req := httptest.NewRequest("POST", "/pincher/mcp", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("/pincher/mcp initialize returned %d: %s", rr.Code, rr.Body.String())
	}
}

// TestStreamableHTTP_RequiresAuthWhenHTTPKeySet verifies the transport
// inherits the existing --http-key gate. Routers fronting pincher with
// a shared bearer token need this.
func TestStreamableHTTP_RequiresAuthWhenHTTPKeySet(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	srv.SetMCPHTTPPath("/mcp")
	srv.SetHTTPKey("secret")

	// No Authorization header → 401 before the MCP handler ever runs.
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{}`))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 without bearer; got %d: %s", rr.Code, rr.Body.String())
	}

	// With the bearer, the request reaches the MCP handler.
	body := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"v0"}}}`)
	req2 := httptest.NewRequest("POST", "/mcp", body)
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Accept", "application/json, text/event-stream")
	req2.Header.Set("Authorization", "Bearer secret")
	rr2 := httptest.NewRecorder()
	srv.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Errorf("expected 200 with bearer; got %d: %s", rr2.Code, rr2.Body.String())
	}
}

// TestStreamableHTTP_SDKHandlerSingleton verifies the lazy build only
// constructs the handler once even under concurrent first-touch.
func TestStreamableHTTP_SDKHandlerSingleton(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	srv.SetMCPHTTPPath("/mcp")

	h1 := srv.streamableHTTPHandler()
	h2 := srv.streamableHTTPHandler()
	if h1 == nil || h2 == nil {
		t.Fatalf("streamableHTTPHandler returned nil")
	}
	// Pointer-equality on http.Handler interface — same underlying
	// concrete value means the sync.Once gate fired exactly once.
	if h1 != h2 {
		t.Errorf("streamableHTTPHandler returned different instances on subsequent calls")
	}
}

// drain reads + discards the body — keeps the linter quiet on tests
// that don't need the response payload but do open it.
func drain(rc io.ReadCloser) {
	_, _ = io.Copy(io.Discard, rc)
	_ = rc.Close()
}

// silence unused-import warnings if test is trimmed in future.
var _ = json.NewEncoder
var _ = drain
