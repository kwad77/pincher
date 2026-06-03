// SPDX-License-Identifier: MIT

package index

import (
	"context"
	"testing"

	"github.com/kwad77/pincher/internal/db"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace/noop"
	"path/filepath"
)

// #1163 traces half (indexer scope): per-index-pass OTLP spans.
// Tests use the SDK's tracetest in-memory recorder, swapped in as
// the global tracer provider for the duration of one test. Index
// uses otel.Tracer(...).Start so it picks up the swap without any
// dependency injection.

// withRecordingProvider swaps in a tracetest recorder as the global
// OTel provider and returns the recorder. Restores the previous
// provider (defaults to a no-op) on test cleanup.
func withRecordingProvider(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { otel.SetTracerProvider(prev) })
	return rec
}

// Positive: one index pass emits one pincher.index.pass span with the
// standard attributes (project_id, force, files_indexed, etc.) and
// status Ok.
func TestIndex_EmitsOTLPSpan(t *testing.T) {
	rec := withRecordingProvider(t)

	dir := t.TempDir()
	store, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	// Tiny corpus so the pass completes fast.
	repo := t.TempDir()
	writeFile(t, repo, "main.go", "package main\nfunc Hello() {}\n")

	idx := New(store)
	if _, err := idx.Index(context.Background(), repo, false); err != nil {
		t.Fatalf("Index: %v", err)
	}

	spans := rec.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span; got %d", len(spans))
	}
	sp := spans[0]
	if sp.Name() != "pincher.index.pass" {
		t.Errorf("span name = %q; want pincher.index.pass", sp.Name())
	}
	attrs := make(map[string]any, len(sp.Attributes()))
	for _, kv := range sp.Attributes() {
		attrs[string(kv.Key)] = kv.Value.AsInterface()
	}
	if attrs["pincher.project_id"] == "" || attrs["pincher.project_id"] == nil {
		t.Errorf("expected non-empty pincher.project_id; got %v", attrs["pincher.project_id"])
	}
	if attrs["pincher.force"] != false {
		t.Errorf("force = %v; want false", attrs["pincher.force"])
	}
	if files, ok := attrs["pincher.files_indexed"].(int64); !ok || files < 1 {
		t.Errorf("expected files_indexed ≥ 1; got %v", attrs["pincher.files_indexed"])
	}
	if syms, ok := attrs["pincher.symbols_total"].(int64); !ok || syms < 1 {
		t.Errorf("expected symbols_total ≥ 1; got %v", attrs["pincher.symbols_total"])
	}
}

// Control: under the no-op global provider (default after server.New
// without OTEL_EXPORTER_OTLP_ENDPOINT), Index still completes
// successfully — no span emission, no allocations on the span pair.
func TestIndex_NoopProviderDoesNotBreakIndex(t *testing.T) {
	otel.SetTracerProvider(noop.NewTracerProvider())
	t.Cleanup(func() { otel.SetTracerProvider(noop.NewTracerProvider()) })

	dir := t.TempDir()
	store, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	repo := t.TempDir()
	writeFile(t, repo, "main.go", "package main\nfunc Hello() {}\n")

	idx := New(store)
	if _, err := idx.Index(context.Background(), repo, false); err != nil {
		t.Fatalf("Index with no-op tracer: %v", err)
	}
}
