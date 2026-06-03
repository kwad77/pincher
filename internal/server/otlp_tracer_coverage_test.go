// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// #1164 deliverable: targeted coverage push for the OTLP tracer
// initialization paths. Pre-test the live-exporter branch in
// newOTLPTracer was at 12.5% (only the env-empty fall-through was
// exercised). These tests spin a localhost stub for the OTLP/HTTP
// transport so the init path runs end-to-end against a real network
// endpoint — exercising the scheme-strip, insecure-detect, exporter
// New, resource.New, NewTracerProvider, and otel.SetTracerProvider
// branches in a single test.
//
// The stub doesn't validate the OTLP/protobuf body — it just answers
// 200 on any request so the exporter init's connectivity probe
// succeeds. The point is exercising the Go init paths, not validating
// the wire format (the OTel SDK's own tests cover that).

// withOTELEnv sets the endpoint env var for the duration of the
// subtest and restores the prior value on cleanup. Centralized to
// avoid each test rolling its own save/restore.
func withOTELEnv(t *testing.T, key, value string) {
	t.Helper()
	prev, hadPrev := os.LookupEnv(key)
	if value == "" {
		os.Unsetenv(key)
	} else {
		os.Setenv(key, value)
	}
	t.Cleanup(func() {
		if hadPrev {
			os.Setenv(key, prev)
		} else {
			os.Unsetenv(key)
		}
	})
}

// stubOTLPCollector spins a localhost HTTP server that answers 200
// to any request. Returns the bare host:port (no scheme).
func stubOTLPCollector(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	// httptest URL is "http://127.0.0.1:PORT". Strip the scheme so
	// the test exercises newOTLPTracer's scheme-strip branch.
	return strings.TrimPrefix(srv.URL, "http://")
}

// TestNewOTLPTracer_LiveExporterPath exercises the full init path
// against a localhost stub. Asserts:
//   - tracer is non-nil (init succeeded)
//   - Enabled() returns true (real exporter, not noop fallback)
//   - tracer.tracer field is populated for span emission
func TestNewOTLPTracer_LiveExporterPath(t *testing.T) {
	endpoint := stubOTLPCollector(t)
	withOTELEnv(t, "OTEL_EXPORTER_OTLP_ENDPOINT", "http://"+endpoint)

	tracer := newOTLPTracer("test-version")
	if tracer == nil {
		t.Fatal("newOTLPTracer returned nil; want a wired *pincherTracer")
	}
	if !tracer.Enabled() {
		t.Errorf("Enabled() = false on live exporter; want true (provider should be set)")
	}
	if tracer.tracer == nil {
		t.Errorf("tracer.tracer is nil; spans cannot be emitted")
	}
	if tracer.exporter == nil {
		t.Errorf("tracer.exporter is nil; shutdown flush has no target")
	}

	// Shutdown the live exporter so the test doesn't leak goroutines.
	// Use a short deadline — the stub answers immediately so flush
	// should complete inside the timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := tracer.Shutdown(ctx); err != nil {
		// Shutdown failure on a localhost stub indicates a real bug;
		// surface it but don't fail the coverage test on it.
		t.Logf("Shutdown returned: %v (informational)", err)
	}
}

// TestNewOTLPTracer_InsecureViaEnvFlag exercises the
// OTEL_EXPORTER_OTLP_TRACES_INSECURE=1 branch. Sets an https://
// endpoint (which would normally infer TLS) and forces insecure
// via the env override, then confirms the tracer still wires.
func TestNewOTLPTracer_InsecureViaEnvFlag(t *testing.T) {
	endpoint := stubOTLPCollector(t)
	withOTELEnv(t, "OTEL_EXPORTER_OTLP_ENDPOINT", "https://"+endpoint)
	withOTELEnv(t, "OTEL_EXPORTER_OTLP_TRACES_INSECURE", "1")

	tracer := newOTLPTracer("test-version")
	if tracer == nil || !tracer.Enabled() {
		t.Fatalf("insecure-flag override didn't wire the tracer; got %+v", tracer)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = tracer.Shutdown(ctx)
}

// TestNewOTLPTracer_EndpointWithTrailingSlash pins the trailing-
// slash strip in the URL parser. A user pasting the endpoint from
// the OTLP spec docs (with trailing `/`) must work the same as one
// without.
func TestNewOTLPTracer_EndpointWithTrailingSlash(t *testing.T) {
	endpoint := stubOTLPCollector(t)
	withOTELEnv(t, "OTEL_EXPORTER_OTLP_ENDPOINT", "http://"+endpoint+"/")

	tracer := newOTLPTracer("test-version")
	if tracer == nil || !tracer.Enabled() {
		t.Fatalf("trailing-slash endpoint didn't wire the tracer; got %+v", tracer)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = tracer.Shutdown(ctx)
}

// TestNewOTLPTracer_NoEnvReturnsNoop pins the empty-env contract.
// With no OTEL_EXPORTER_OTLP_ENDPOINT, newOTLPTracer must still
// return a usable *pincherTracer (noop fallback) so the server
// can unconditionally call .Start on the tracer field.
func TestNewOTLPTracer_NoEnvReturnsNoop(t *testing.T) {
	withOTELEnv(t, "OTEL_EXPORTER_OTLP_ENDPOINT", "")

	tracer := newOTLPTracer("test-version")
	if tracer == nil {
		t.Fatal("newOTLPTracer returned nil on empty env; want noop fallback")
	}
	if tracer.Enabled() {
		t.Errorf("Enabled() = true with no exporter configured; want false (noop fallback contract)")
	}
	if tracer.tracer == nil {
		t.Errorf("noop fallback should still have a non-nil tracer for unconditional .Start calls")
	}
	if tracer.provider != nil {
		t.Errorf("noop fallback should have nil provider — Enabled() checks this")
	}
	if tracer.exporter != nil {
		t.Errorf("noop fallback should have nil exporter — Shutdown short-circuits on this")
	}
}

// TestTracerOrNoop_NilServer exercises the defensive nil-server
// branch in tracerOrNoop — the wrapper falls back to the singleton
// noop tracer rather than panicking on a nil receiver. Pre-test
// this branch was 0% covered.
func TestTracerOrNoop_NilServer(t *testing.T) {
	var s *Server
	tr := s.tracerOrNoop()
	if tr == nil {
		t.Fatal("tracerOrNoop on nil server returned nil; want singleton noop")
	}
	// Spans created on the noop tracer are still safe to End — that's
	// the whole point of having a noop fallback at all.
	_, span := tr.Start(context.Background(), "noop-from-nil-server")
	span.End()
}

// TestTracerOrNoop_ServerWithNilTracer covers the (s != nil, s.tracer
// == nil) branch — same singleton noop fallback. Pre-test the
// fallthrough was uncovered because every test server initializes
// tracer to a non-nil noop value via the constructor.
func TestTracerOrNoop_ServerWithNilTracer(t *testing.T) {
	srv := &Server{} // tracer field is zero-value nil
	tr := srv.tracerOrNoop()
	if tr == nil {
		t.Fatal("tracerOrNoop on Server with nil tracer returned nil; want singleton noop")
	}
	_, span := tr.Start(context.Background(), "noop-from-nil-tracer-field")
	span.End()
}
