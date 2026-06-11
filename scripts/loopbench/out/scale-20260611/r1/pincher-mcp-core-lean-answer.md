All eight questions answered. Here are the findings:

## 1. `pkg07/submit` → Python fulfillment trace

The order is ultimately fulfilled by **`fulfill_pkg07` in `workers/handlers/pkg07_handler.py:8`**. The full path:

1. **TS**: `web/src/pkg07/actions.ts:5` registers the string `"pkg07/submit"` → `processPkg07Order` (`web/src/pkg07/process.ts:7`), which calls `postShardOrder("pkg07", …)` (`web/src/api/scaledClient.ts:3`) → `POST /api/v1/pkg/pkg07/orders`.
2. **Go API**: `handleScaledCreate` (`internal/api/scaled_routes.go:19`) takes the `{pkg}` path value and dispatches the action string `"pkg07.process"` via `dispatch.Dispatch`.
3. **Go shard**: `init()` in `internal/pkg07/register.go:14` binds `"pkg07.process"` → `processActionPkg07` → `ProcessOrderPkg07` (`internal/pkg07/pipeline.go:26`).
4. **Queue**: `ProcessOrderPkg07` calls `queue.Publish("pkg07.fulfill", …)` (`pipeline.go:40`); `Publish` writes a JSON line with `"job_type": "pkg07.fulfill"` to `/tmp/orderflow-queue.jsonl` (`internal/queue/queue.go:13-14`).
5. **Python**: `worker_main.py:route()` looks up `job_type` in `HANDLERS` (`workers/lib/registry.py`), where `@register("pkg07.fulfill")` bound **`fulfill_pkg07`** (`workers/handlers/pkg07_handler.py:7-8`) — not its unregistered twin in `pkg07_legacy.py`.

Connecting string keys: `"pkg07/submit"` (UI action) → URL path `pkg07` → `"pkg07.process"` (Go dispatch action) → `"pkg07.fulfill"` (queue job_type).

## 2. Payment capture for a pkg21 order

**`CapturePaymentPkg22` in `internal/pkg22/charge.go:15`.** The shards capture through the *next* shard's gateway: `ProcessOrderPkg21` (`internal/pkg21/pipeline.go:30`) calls `pkg22.CapturePaymentPkg22`, which retries the stub `gatewayChargePkg22` up to its retry limit. It is **not** `CapturePaymentPkg21` — that one serves pkg20-vertical orders.

## 3. `ProcessOrderPkg33V1` — dead or alive?

**Dead.** A repo-wide grep over all `.go` files finds only its definition (`internal/pkg33/dead_v1.go:12`) and a doc-comment mention in `internal/pkg33/pipeline.go:25`. `internal/pkg33/register.go` registers only the live `processActionPkg33`/`ProcessOrderPkg33`, and nothing anywhere (Go, Python, TS) references `Pkg33V1` or its `"pkg33.v1"` job type — no Python handler registers that job_type either.

## 4. Can `fulfill_pkg14` in `pkg14_legacy.py` ever run?

**No.** Registration happens only via the `@register(job_type)` decorator side effect (`workers/lib/registry.py:6`), which fires when a module is imported. `workers/handlers/__init__.py` imports only the `*_handler` modules and explicitly notes the `*_legacy` twins are not imported (`__init__.py:12-13`). `pkg14_legacy.py` is never imported anywhere and its `fulfill_pkg14` carries no decorator (`pkg14_legacy.py:10`), so it never enters `HANDLERS`; queued `"pkg14.fulfill"` jobs go to the decorated `fulfill_pkg14` in `pkg14_handler.py` instead.

## 5. Blast radius of `SaveRecordPkg12`

Three non-test call sites, all in `internal/pkg12/`:
- `ProcessOrderPkg12` — `internal/pkg12/pipeline.go:34`
- `RefundOrderPkg12` — `internal/pkg12/refund.go:24`
- `ProcessOrderPkg12V1` (dead but compiled) — `internal/pkg12/dead_v1.go:17`

## 6. Direct callers of `RecordAuditPkg30`

Exactly one: **`ProcessOrderPkg29`** at `internal/pkg29/pipeline.go:37` (the cross-shard audit pattern — shard N audits via shard N+1).

## 7. `pkg26_retry_limit`

- **Effective default: 5**, defined in `gen/defaults.gen.json:188`.
- **Consumer:** `CapturePaymentPkg26` (`internal/pkg26/charge.go:16`) via `config.ScaledKnob("pkg26_retry_limit", 2)` — the in-code `2` is only a last-ditch fallback if the generated file is missing; `ScaledKnob` (`internal/config/scaled.go:12`) loads `gen/defaults.gen.json` and prefers the env var `PKG26_RETRY_LIMIT` if set.
- **Caller of the consumer:** `ProcessOrderPkg25` (`internal/pkg25/pipeline.go:30`). Decoys: `config/app.yaml` explicitly contains no tuning values, and the fixture/queue-depth entries (`pkg25_retry_limit: 7`, `pkg26_queue_depth: 672`) are different knobs.

## 8. Worker job-type count and the pkg09 handler

**43 job types** are registered at startup: 40 shard handlers (`pkg01.fulfill` … `pkg40.fulfill`, one `@register` each) plus `process_order` (`order_handler.py`), `process_refund` (`refund_handler.py`), and `inventory.sync` (`inventory_handler.py`). The legacy modules contribute zero since they're not imported. pkg09 orders are fulfilled by **`workers/handlers/pkg09_handler.py::fulfill_pkg09`**, registered under `"pkg09.fulfill"` (`pkg09_handler.py:7-8`).
