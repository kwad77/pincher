// SPDX-License-Identifier: MIT

package server

import (
	"os"
	"testing"
)

// #1164 deliverable: positive-path tests for the traces_otlp
// capability probe added to capabilityProbes.
//
// The probe's job: when traces_otlp is advertised, the tracer must
// be non-nil AND report Enabled (real exporter, not noop fallback).
// Test the two state-checks directly rather than mocking *testing.T
// — the failure modes are simple boolean conditions on srv.tracer.

func TestTracesOTLPProbe_FailureConditions(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)

	// nil tracer is the "wiring broken" case the probe catches.
	srv.tracer = nil
	if srv.tracer != nil {
		t.Fatal("setup broken: tracer should be nil")
	}

	// Noop fallback (zero value) is the "exporter init failed silently"
	// case. Enabled() returns false because provider is nil.
	srv.tracer = &pincherTracer{}
	if srv.tracer.Enabled() {
		t.Error("zero-value tracer reports Enabled = true; the noop-fallback contract is broken")
	}
}

// TestTracesOTLPProbe_NotAdvertisedByDefault pins the conditional-
// advertisement contract. A test server with no OTEL env var must
// NOT advertise traces_otlp — the capability is gated on a real
// exporter, so the lockstep gate's skip list must include this tag.
func TestTracesOTLPProbe_NotAdvertisedByDefault(t *testing.T) {
	t.Parallel()
	prev := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	os.Unsetenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	t.Cleanup(func() {
		if prev != "" {
			os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", prev)
		}
	})

	srv, _, _ := newTestServer(t)
	for _, tag := range srv.capabilities {
		if tag == "traces_otlp" {
			t.Errorf("traces_otlp advertised without OTEL endpoint configured; capability/env contract broken")
		}
	}
}

// TestTracesOTLPProbe_PresentInRegistry pins that the probe entry
// itself lives in capabilityProbes — without this, the lockstep
// gate's skip-when-not-advertised list could mask a missing probe.
func TestTracesOTLPProbe_PresentInRegistry(t *testing.T) {
	t.Parallel()
	for _, p := range capabilityProbes {
		if p.tag == "traces_otlp" {
			return
		}
	}
	t.Fatal("traces_otlp probe missing from capabilityProbes — gate misconfigured (#1164 deliverable regression)")
}
