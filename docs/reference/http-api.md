# HTTP REST API

[Back to reference index](README.md)

All 30 tools are available via `POST /v1/{tool}` with a JSON body. Run alongside MCP stdio — no either/or.

```bash
# Start a loopback-only local gateway with both transports
pincher --http 127.0.0.1:8080

# Index a repo
curl -s -X POST http://localhost:8080/v1/index \
  -H "Content-Type: application/json" \
  -d '{"path": "/path/to/your/project"}' | jq .

# Search with field projection (fewer tokens)
curl -s -X POST http://localhost:8080/v1/search \
  -H "Content-Type: application/json" \
  -H "Accept-Encoding: gzip" \
  -d '{"query": "processPayment", "project": "myproject", "fields": "id,name,file_path"}' | jq .

# Cross-repo search
curl -s -X POST http://localhost:8080/v1/search \
  -d '{"query": "auth*", "project": "*"}' | jq .

# pinchQL graph query (legacy `cypher` parameter still accepted for one release)
curl -s -X POST http://localhost:8080/v1/query \
  -d '{"pinchql": "MATCH (f:Function)-[:CALLS]->(g) WHERE f.name = '\''main'\'' RETURN g.name LIMIT 10", "project": "myproject"}' | jq .

# Liveness probe — no auth required
curl http://localhost:8080/v1/health

# Rich health tool report — follows normal tool auth when --http-key is set
curl -s -X POST http://localhost:8080/v1/health \
  -H "Content-Type: application/json" \
  -d '{}' | jq '.observability'

# OpenAPI spec (Postman / Cursor importable)
curl http://localhost:8080/v1/openapi.json | jq .
```

Responses compress ~65% with `Accept-Encoding: gzip`. Tested clients: curl, Python `requests`, PowerShell `Invoke-WebRequest`. Rate limiting: `--http-rate 60` limits to 60 requests/IP/minute (0 = unlimited). A non-loopback bind such as `--http :8080` requires `--http-key` / `PINCHER_HTTP_KEY`, or the explicit `--http-allow-open` escape hatch when a trusted reverse proxy handles auth.

### Additional HTTP endpoints

| Endpoint | Method | Auth | Description |
|---|---|---|---|
| `/v1/health` | GET | No | Lightweight liveness probe — process/version/auth metadata. Always 200. Use `POST /v1/health` for the health tool's schema, index-staleness, coverage, observability, and `binary_stale` report. |
| `/v1/ready` | GET | No | Readiness probe (#660) — 200 when the server can serve traffic; 503 when an essential dependency (store, indexer, schema migration) isn't ready. Use `/v1/health` for liveness and `/v1/ready` for readiness gating in orchestrator manifests (Kubernetes `readinessProbe`, systemd `Type=notify`). |
| `/v1/dashboard` | GET | No | Self-contained HTML dashboard (stats, search, project cards, sparkline). No external deps. |
| `/v1/dashboard.css` | GET | No | Dashboard stylesheet. Served separately so CSP can drop `'unsafe-inline'`. |
| `/v1/dashboard.js` | GET | No | Dashboard JavaScript. Same CSP rationale. |
| `/v1/openapi.json` | GET | No | OpenAPI 3.1 spec covering all 29 tool endpoints + the GET routes. Import into Postman or Cursor. |
| `/v1/stats` | GET | Yes | Current session + all-time savings as JSON. |
| `/v1/sessions` | GET | Yes | Per-session history, last 90 sessions, sorted by recency. |
| `/v1/projects` | GET | Yes | All indexed projects with file/symbol/edge counts. |
| `/v1/projects` | DELETE | Yes | Remove a project and all its symbols. Body: `{"id":"<project-id>"}`. |
| `/v1/index-progress` | POST | Yes | Live indexing progress: `{files_done, files_total, active}`. |
| `/v1/events` | GET | Yes* | Server-Sent Events stream — `index_started`, `index_complete`, `binary_drift`. Sends a `binary_drift` snapshot on connect, then live events. Optional `?project=<id>` filter. \*Honors `--http-key` when set. |
| `/v1/hook-stats` | GET | Yes | Hook conversion-rate + raw counts over the last 7 days (#628). Powers the Overview tab's Hook Stats panel. |
| `/v1/tool-call-stats` | GET | Yes | Per-tool aggregate over the trailing window (default 7d) — call_count, avg_tokens_used, sum_tokens_saved, avg_tokens_saved_pct, avg_response_bytes (#635 v0.67). Query params: `window_seconds`, `limit`. |
| `/v1/tool-calls` | GET | Yes | Recent per-call token accounting rows for one session. Defaults to the newest session; query params: `session_id`, `limit` (default 50, max 200). Includes `savings_math` when a full-file baseline exists and `improvement_signal` for dashboard inspection. |
| `/v1/tool-tier-stats` | GET | Yes | Per-complexity-tier aggregate (lite/standard/heavy) over the trailing window (#635 v0.67 panel 2). |
| `/v1/tool-payload-stats` | GET | Yes | Per-tool response_bytes distribution (min/avg/max/sum) over the trailing window. Sorted by max_bytes DESC — the dashboard "outlier finder" view (#635 v0.67 panel 3). |
| `/v1/metrics` | GET | No | Prometheus exposition format (#1163 v0.67). Standard counters/histograms/gauges for tool calls, latency, index pass, db/wal size. |
| `/v1/bench-results` | GET | Yes | `pincher bench --persist` history per project (#1263 v0.68). Returns the most recent N runs joined with per-tool aggregates. Query params: `project` (optional; defaults to ALL projects, newest-first), `limit` (default 20, max 200). Drives the dashboard Bench History panel. |
| `/v1/capabilities` | GET | Yes | One-shot read of the per-server capability slice (#1087 v0.69). Drop-in alternative for HTTP clients that don't want to pay the per-call `_meta.capabilities` cost — call once at session start, cache the result. Especially relevant when the operator has set `PINCHER_META_CAPABILITIES=off` to skip the per-call stamp. |

### Server-side env knobs

| Env var | Default | Effect |
|---|---|---|
| `PINCHER_META_CAPABILITIES` | `on` | Set to `off` (or `false`/`0`/`none`/`no`) at server start to drop the per-call `_meta.capabilities` stamp. Saves ~50 tokens/call (#1087). Use the `/v1/capabilities` endpoint to query the slice once. Default-on preserves back-compat. |
| `PINCHER_META_DELTA` | `on` | Session-delta `_meta`: only the **first** success response of a session carries the `_meta.capabilities` array (~74 tokens); subsequent responses omit it and stamp `_meta.meta_delta: true` so consumers can tell intentional omission from accident. The array is re-emitted in full whenever the capability set actually changes (e.g. streamable-HTTP enabled mid-process); `PINCHER_META_CAPABILITIES` is read once at start and cannot toggle mid-process. Per-tool fields (`complexity_tier`, `baseline_method`) and error envelopes are never delta'd. Set to `0` (or `off`/`false`/`none`/`no`) to restore the legacy every-call emission. |
| `PINCHER_TOOL_DESCRIPTIONS` | (unset) | Set to `short` at server start to swap the 5 longest tool descriptions (trace / search / neighborhood / query / changes) for one-sentence variants. Trims ~3 KB / ~750 tokens off every session-start `tools/list` handshake. Long-form pedagogical content stays available via the per-tool sections in [`docs/reference/tools.md`](tools.md) (#1088). |
| `PINCHER_TOKEN_ACCOUNTING` | `cheap` | Per-call `_meta.tokens_used` / `tokens_saved_pct` are computed with a char/4 heuristic by default (#1320, v0.69 perf hardening). Set to `exact` (or `bpe` / `1`) at server start to restore cl100k_base BPE counts — useful for operators benchmarking real token consumption or validating savings reporting. The cheap default cut 60% of per-call allocations on the authenticated handler path; per-call envelopes shift by ~5-15% under cheap mode, the session-flush aggregator is unaffected. |

CORS: all responses include `Access-Control-Allow-Origin: *` so browsers can call directly without a proxy.

### Observability (#1163, #654, #628)

pincher exposes four standard observability surfaces a production router or SRE tool can scrape without any pincher-specific glue:

| Signal | Surface | Default | Capability tag |
|---|---|---|---|
| **Metrics** (counters, histograms, gauges) | `GET /v1/metrics` (Prometheus exposition) | Always on | `metrics_prometheus` |
| **Traces** (per-tool-call spans) | OTLP/HTTP exporter, configured via env | Off — opts in via env | `traces_otlp` |
| **Events** (index lifecycle, binary drift) | `GET /v1/events` (Server-Sent Events) | Always on | `event_stream_sse` |
| **Correlation IDs** | `X-Request-ID` header + `_meta.request_id` | Always on | `request_id_correlation` |

Capability tags appear in the **first** tool response of a session's `_meta.capabilities` array so a router can detect what this binary supports without parsing version strings or scraping a config file. Subsequent responses omit the array and stamp `_meta.meta_delta: true` (session-delta, default on); it is re-emitted in full when the capability set changes. Set `PINCHER_META_DELTA=0` to restore the per-call stamp, or query `GET /v1/capabilities` at any time.

#### Standard metrics (`/v1/metrics`)

| Metric | Type | Labels |
|---|---|---|
| `pincher_tool_calls_total` | counter | `tool`, `outcome` |
| `pincher_tool_latency_seconds` | summary | `tool` |
| `pincher_tool_tokens_saved_total` | counter | `tool` |
| `pincher_index_files_total` | counter | `outcome` |
| `pincher_index_symbols_total` | counter | — |
| `pincher_index_duration_seconds` | histogram | `kind` |
| `pincher_db_size_bytes` | gauge | — |
| `pincher_wal_size_bytes` | gauge | — |

#### Enabling OTLP traces

```bash
# Default OTLP collector port; supports http:// and https:// schemes.
export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318
# Optional override for plain-HTTP collectors in dev:
export OTEL_EXPORTER_OTLP_TRACES_INSECURE=1
```

Per-tool-call spans are exported with the following attributes:

- `rpc.system=mcp`
- `rpc.method=<tool>`
- `pincher.complexity_tier=<lite|standard|heavy>` — same dimension the dashboard panels use
- `pincher.request_id=<#657 correlation ID>` — joins cleanly with `_meta.request_id` and the `X-Request-ID` response header
- `pincher.response_bytes=<int>`

Per-index-pass spans (one per `Index()` call) are exported under instrumentation library `pincher.index` with span name `pincher.index.pass` and these attributes:

- `pincher.project_id`, `pincher.project_name`, `pincher.repo_path`
- `pincher.force=<bool>` — whether the pass was forced (re-index regardless of file hash)
- `pincher.files_indexed`, `pincher.symbols_total`, `pincher.edges_total`
- `pincher.files_skipped`, `pincher.files_blocked`, `pincher.files_deleted`
- `pincher.duration_ms`

Resource attributes: `service.name=pincher`, `service.version=<binary version>` so a router groups spans without parsing.

If `OTEL_EXPORTER_OTLP_ENDPOINT` is unset (the default), the tracer is a zero-allocation no-op — observability never breaks the hot path. The `traces_otlp` capability is advertised only when the OTLP exporter successfully initialized so consumers can distinguish "configured + working" from "best-effort no-op."
