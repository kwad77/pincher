// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// otlpTraceServiceName is the resource.service.name attribute stamped
// on every emitted span. Stable string so routers can group spans by
// "service:pincher" without having to parse version strings.
const otlpTraceServiceName = "pincher"

// otlpTraceEnvEndpoint is the standard OTLP env var. Honored at New()
// time — empty means the binary uses a noop tracer (zero overhead, no
// span emission, capability unadvertised).
const otlpTraceEnvEndpoint = "OTEL_EXPORTER_OTLP_ENDPOINT"

// otlpTraceEnvInsecure forces HTTP rather than HTTPS for the OTLP
// endpoint. Matches the upstream env-var contract (otel-collector
// agents commonly bind to http://localhost:4318 in dev).
const otlpTraceEnvInsecure = "OTEL_EXPORTER_OTLP_TRACES_INSECURE"

// pincherTracer wraps the SDK tracer provider with the shutdown hook
// the server's Stop() needs to call to flush pending spans before
// process exit. The handler-side surface only ever uses Tracer.
type pincherTracer struct {
	provider *sdktrace.TracerProvider
	tracer   trace.Tracer
	exporter *otlptrace.Exporter
}

// newOTLPTracer initializes an OTLP/HTTP exporter against the
// configured endpoint. Returns a noop tracer when no endpoint is
// configured so the rest of the server can call s.tracer.Start
// unconditionally without nil-checks.
//
// Initialization errors are logged but do NOT block server startup —
// observability is a nice-to-have, never load-bearing. The capability
// advertisement is gated on a successful init so consumers can
// distinguish "configured + working" from "best-effort no-op."
func newOTLPTracer(version string) *pincherTracer {
	endpoint := strings.TrimSpace(os.Getenv(otlpTraceEnvEndpoint))
	if endpoint == "" {
		return &pincherTracer{tracer: noop.NewTracerProvider().Tracer(otlpTraceServiceName)}
	}

	// Parse the standard OTEL endpoint env var. The SDK's otlptracehttp
	// transport wants a bare host:port, so strip any scheme the user
	// supplied. Also honor the trailing /v1/traces path the OTLP spec
	// recommends as the default ingest path.
	host := endpoint
	host = strings.TrimPrefix(host, "https://")
	host = strings.TrimPrefix(host, "http://")
	host = strings.TrimSuffix(host, "/")

	insecure := strings.HasPrefix(endpoint, "http://") || os.Getenv(otlpTraceEnvInsecure) == "1"

	opts := []otlptracehttp.Option{
		otlptracehttp.WithEndpoint(host),
		otlptracehttp.WithTimeout(5 * time.Second),
	}
	if insecure {
		opts = append(opts, otlptracehttp.WithInsecure())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	exp, err := otlptracehttp.New(ctx, opts...)
	if err != nil {
		slog.Warn("pincher.otlp.exporter.init_failed",
			"endpoint", endpoint,
			"err", err,
			"hint", "OTLP traces disabled this run — fix endpoint or unset OTEL_EXPORTER_OTLP_ENDPOINT")
		return &pincherTracer{tracer: noop.NewTracerProvider().Tracer(otlpTraceServiceName)}
	}

	res, err := resource.New(context.Background(),
		resource.WithAttributes(
			semconv.ServiceName(otlpTraceServiceName),
			semconv.ServiceVersion(version),
		),
	)
	if err != nil {
		res = resource.Default()
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	slog.Info("pincher.otlp.exporter.ready",
		"endpoint", endpoint,
		"insecure", insecure)
	return &pincherTracer{
		provider: tp,
		tracer:   tp.Tracer(otlpTraceServiceName),
		exporter: exp,
	}
}

// Enabled reports whether the tracer is wired to a real OTLP exporter
// (vs the no-op fallback). Drives the traces_otlp capability advertisement
// AND the health-tool observability-surface render. The only external
// consumers; the embedded tracer is reached via tracerOrNoop().
func (t *pincherTracer) Enabled() bool { return t.provider != nil }

// Shutdown flushes pending spans then closes the exporter. Safe to
// call when the exporter is nil (no-op tracer / unconfigured) — the
// nil-guard makes the cmd/pinch defer site unconditional. Without
// this call, the BatchSpanProcessor's final spans may not reach the
// collector before the process exits.
func (t *pincherTracer) Shutdown(ctx context.Context) error {
	if t == nil || t.provider == nil {
		return nil
	}
	return errors.Join(
		t.provider.ForceFlush(ctx),
		t.provider.Shutdown(ctx),
	)
}

// ShutdownTracer flushes pending OTLP spans then closes the exporter.
// Safe to call when no OTLP endpoint is configured (the no-op tracer
// path returns nil). Called from cmd/pinch's signal-shutdown defer so
// the BatchSpanProcessor's final spans reach the collector before
// the process exits.
func (s *Server) ShutdownTracer(ctx context.Context) error {
	if s == nil {
		return nil
	}
	return s.tracer.Shutdown(ctx)
}

// withTracing wraps a tool handler in a per-tool-call OTLP span.
// Sibling of withRequestID — both run inside addTool so every
// registered tool gets the same treatment over stdio and HTTP.
//
// Span name uses the convention "pincher.tool.<name>" so a router can
// filter on the prefix without enumerating every tool. Attributes
// follow the OTel semantic conventions where applicable (rpc.method =
// tool name) plus the pincher-specific request_id correlation field.
//
// When the tracer is the noop fallback the cost is one virtual call
// and one zero-allocation StartSpan/End pair — fine on the hot path.
func (s *Server) withTracing(handler mcp.ToolHandler) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		tracer := s.tracerOrNoop()
		tool := ""
		if req != nil && req.Params != nil {
			tool = req.Params.Name
		}
		ctx, span := tracer.Start(ctx, "pincher.tool."+tool,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				attribute.String("rpc.system", "mcp"),
				attribute.String("rpc.method", tool),
				attribute.String("pincher.complexity_tier", toolComplexityTier(tool)),
			),
		)
		defer span.End()

		// Stamp the resolved request_id once it has been minted by
		// withRequestID upstream. Doing this at span creation time
		// catches the HTTP-supplied ID; stamping again before End
		// catches the case where withRequestID minted it.
		if rid := requestIDFromContext(ctx); rid != "" {
			span.SetAttributes(attribute.String("pincher.request_id", rid))
		}

		res, err := handler(ctx, req)

		// Stamp post-handler attributes the dashboard panels also
		// surface: response payload size and resolved request_id.
		if rid := requestIDFromContext(ctx); rid != "" {
			span.SetAttributes(attribute.String("pincher.request_id", rid))
		}
		if res != nil && len(res.Content) > 0 {
			if tc, ok := res.Content[0].(*mcp.TextContent); ok {
				span.SetAttributes(attribute.Int("pincher.response_bytes", len(tc.Text)))
			}
		}
		// Status: Go-level err is the obvious failure signal; res.IsError
		// is the protocol-level signal pincher uses for tool-shaped errors
		// (errResult / errResultRich wrap an error envelope into the
		// response rather than returning a Go err). Treat both as Error
		// so a router's OTLP latency dashboard isn't optimistic by 80%.
		// Stamp pincher.is_error too so a filter can split the two.
		switch {
		case err != nil:
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			span.SetAttributes(attribute.Bool("pincher.is_error", true))
		case res != nil && res.IsError:
			span.SetStatus(codes.Error, "tool returned error envelope")
			span.SetAttributes(attribute.Bool("pincher.is_error", true))
		default:
			span.SetStatus(codes.Ok, "")
		}
		return res, err
	}
}

// tracerOrNoop is the lookup the wrapper uses. Pulls from the server's
// configured tracer when wired, falls back to the global no-op
// provider's tracer otherwise — keeps the wrapper safe in tests that
// don't initialize OTLP.
var noopTracerOnce sync.Once
var noopTracer trace.Tracer

func (s *Server) tracerOrNoop() trace.Tracer {
	if s != nil && s.tracer != nil && s.tracer.tracer != nil {
		return s.tracer.tracer
	}
	noopTracerOnce.Do(func() {
		noopTracer = noop.NewTracerProvider().Tracer(otlpTraceServiceName)
	})
	return noopTracer
}

