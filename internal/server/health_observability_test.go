// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// #1163 v0.67 follow-up: health.observability discoverability surface.
// Tests pin the three signals (metrics on, sse on, traces off-by-default
// + hint) so the discoverability surface stays predictable.

// Positive: with no OTLP env configured, health reports
// traces_otlp="off (unset...)" and the other two always-on surfaces.
func TestHandleHealth_ObservabilitySurface_Default(t *testing.T) {
	srv, _, _ := newTestServer(t)
	res, err := srv.handleHealth(context.Background(), &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Name: "health", Arguments: []byte(`{}`)},
	})
	if err != nil {
		t.Fatalf("handleHealth: %v", err)
	}
	body := textOf(t, res)
	for _, want := range []string{
		`"observability":`,
		`"metrics_prometheus":"on`,
		`"event_stream_sse":"on`,
		`"traces_otlp":"off`,
		"OTEL_EXPORTER_OTLP_ENDPOINT",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("health body missing %q\n  body: %s", want, body)
		}
	}
}

// Positive: with a tracer that's reporting Enabled()=true, health
// reports traces_otlp="on (OTLP/HTTP → ...)" so a router can see
// where spans are going without parsing _meta.capabilities.
func TestHandleHealth_ObservabilitySurface_OTLPConfigured(t *testing.T) {
	srv, _, _ := newTestServer(t)
	// Sentinel endpoint via env so the handler renders it. Set via
	// t.Setenv so cleanup is automatic.
	t.Setenv(otlpTraceEnvEndpoint, "http://collector:4318")
	// Swap in a tracer with a non-nil provider — Enabled() returns
	// `t.provider != nil`, so any real SDK provider satisfies it.
	// No exporter wired — we don't need real export, just the gate.
	tp := sdktrace.NewTracerProvider()
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	srv.tracer = &pincherTracer{
		provider: tp,
		tracer:   tp.Tracer(otlpTraceServiceName),
	}

	res, err := srv.handleHealth(context.Background(), &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Name: "health", Arguments: []byte(`{}`)},
	})
	if err != nil {
		t.Fatalf("handleHealth: %v", err)
	}
	body := textOf(t, res)
	if !strings.Contains(body, `"traces_otlp":"on`) {
		t.Errorf("expected traces_otlp=on; got body: %s", body)
	}
	if !strings.Contains(body, "collector:4318") {
		t.Errorf("expected endpoint in render; got body: %s", body)
	}
}
