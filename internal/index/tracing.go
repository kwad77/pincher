// SPDX-License-Identifier: MIT

package index

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// indexTracerName is the OTel instrumentation library name stamped on
// every indexer-emitted span. Stable so routers can filter on
// "instrumentation.library.name=pincher.index" to see only index-pass
// activity (vs per-tool-call spans, which use "pincher").
const indexTracerName = "pincher.index"

// startIndexSpan opens a top-level OTLP span for one index pass and
// returns the new ctx plus the span handle. Caller is responsible for
// calling End() (typically via defer).
//
// Uses the global tracer provider. The server-side OTLP exporter
// (see internal/server/otlp_tracer.go) calls otel.SetTracerProvider
// during New(), so once the server is up, this routes spans through
// the same OTLP/HTTP transport as the per-tool-call spans. When no
// OTLP endpoint is configured, the global provider is a no-op and
// the span pair costs effectively nothing on the hot path.
//
// Span naming convention: "pincher.index.pass" so a router can group
// pass-level spans regardless of project. Per-project breakdown is
// available via the project_id attribute.
func startIndexSpan(ctx context.Context, projectID, projectName, repoPath string, force bool) (context.Context, trace.Span) {
	return otel.Tracer(indexTracerName).Start(ctx, "pincher.index.pass",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("pincher.project_id", projectID),
			attribute.String("pincher.project_name", projectName),
			attribute.String("pincher.repo_path", repoPath),
			attribute.Bool("pincher.force", force),
		),
	)
}

// finishIndexSpan stamps the per-pass outcome attributes and ends the
// span. Separates "stamp post-result data" from span lifecycle so the
// Index function can have one deferred span.End and one explicit
// attribute-stamp call near its successful return.
func finishIndexSpan(span trace.Span, totalFiles, totalSymbols, totalEdges, totalSkipped, totalBlocked, totalDeleted int, durationMs int64, err error) {
	if span == nil {
		return
	}
	span.SetAttributes(
		attribute.Int("pincher.files_indexed", totalFiles),
		attribute.Int("pincher.symbols_total", totalSymbols),
		attribute.Int("pincher.edges_total", totalEdges),
		attribute.Int("pincher.files_skipped", totalSkipped),
		attribute.Int("pincher.files_blocked", totalBlocked),
		attribute.Int("pincher.files_deleted", totalDeleted),
		attribute.Int64("pincher.duration_ms", durationMs),
	)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	} else {
		span.SetStatus(codes.Ok, "")
	}
}
