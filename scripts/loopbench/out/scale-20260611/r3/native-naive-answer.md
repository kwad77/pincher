All evidence is in hand. Here are the answers.

*(Note: the pincher MCP tools required by CLAUDE.md are not connected in this session — ToolSearch finds no pincher tools — so I used the documented fallback of direct Grep/Read.)*

## 1. "pkg07/submit" → Python fulfillment path

1. **TS action**: `web/src/pkg07/actions.ts` registers `"pkg07/submit"` via `registerAction` (`web/src/registry.ts`), bound to **`processPkg07Order`** in `web/src/pkg07/process.ts`.
2. **HTTP**: `processPkg07Order` calls `postShardOrder("pkg07", ...)` (`web/src/api/scaledClient.ts`), which POSTs `/api/v1/pkg/pkg07/orders`.
3. **Go API**: `handleScaledCreate` (`internal/api/scaled_routes.go:20`) builds the action string `"pkg07" + ".process"` and calls `dispatch.Dispatch` (`internal/dispatch/registry.go`).
4. **Go handler**: `init()` in `internal/pkg07/register.go` registered `"pkg07.process"` → `processActionPkg07` → **`ProcessOrderPkg07`** (`internal/pkg07/pipeline.go:26`).
5. **Queue**: the pipeline ends with `queue.Publish("pkg07.fulfill", ...)` — `internal/queue/queue.go` writes a JSON line with `job_type: "pkg07.fulfill"` to `/tmp/orderflow-queue.jsonl`.
6. **Python**: `workers/worker_main.py` `route()` looks up the job_type in `HANDLERS` (`workers/lib/registry.py`); the `@register("pkg07.fulfill")` decorator binds it to **`fulfill_pkg07` in `workers/handlers/pkg07_handler.py`** — the final fulfilling function (not the unregistered twin in `pkg07_legacy.py`).

Connecting string keys: `"pkg07/submit"` (TS registry) → URL path param `pkg07` → `"pkg07.process"` (Go dispatch) → `"pkg07.fulfill"` (queue job_type / Python registry).

## 2. Payment capture for a pkg21-vertical order

**`CapturePaymentPkg22` in `internal/pkg22/charge.go:15`.** The live pipeline `ProcessOrderPkg21` (`internal/pkg21/pipeline.go:30`) charges through the *next* shard's gateway: `pkg22.CapturePaymentPkg22(...)`. The lookalike `CapturePaymentPkg21` (`internal/pkg21/charge.go`) is a decoy — it's only called by `ProcessOrderPkg20` (`internal/pkg20/pipeline.go:30`), i.e. it serves pkg20-vertical orders.

## 3. `ProcessOrderPkg33V1` — dead or alive?

**Dead.** A repo-wide grep for `ProcessOrderPkg33V1` finds only its definition (`internal/pkg33/dead_v1.go:12`) and a "not to be confused with" doc comment in `internal/pkg33/pipeline.go:25`. `internal/pkg33/register.go` registers only `pkg33.process` and `pkg33.refund` (mapping to the live pipeline), and nothing in `cmd/` references it. (Even if it ran, its published job_type `"pkg33.v1"` has no registered Python handler.)

## 4. Can `fulfill_pkg14` in `pkg14_legacy.py` ever run?

**No.** Worker dispatch is purely registry-driven: `worker_main.py` routes jobs through `HANDLERS` in `workers/lib/registry.py`, which is populated only by `@register(...)` decorator side effects when modules are imported. `pkg14_legacy.py` has no `@register` decorator on `fulfill_pkg14`, and `workers/handlers/__init__.py` deliberately imports only the `*_handler` modules ("the `*_legacy` twins are deliberately NOT imported"). So the module is never imported and the function is never registered. The `"pkg14.fulfill"` job_type is instead claimed by the live `fulfill_pkg14` in `workers/handlers/pkg14_handler.py` (`@register("pkg14.fulfill")`).

## 5. Blast radius of changing `SaveRecordPkg12`'s signature

Three non-test call sites (there are no test files in `internal/pkg12/`):

- `ProcessOrderPkg12` — `internal/pkg12/pipeline.go:34`
- `RefundOrderPkg12` — `internal/pkg12/refund.go:24`
- `ProcessOrderPkg12V1` (dead but still compiled) — `internal/pkg12/dead_v1.go:17`

## 6. Direct non-test callers of `RecordAuditPkg30`

Exactly one: **`ProcessOrderPkg29`** in `internal/pkg29/pipeline.go:37` (cross-shard audit call `pkg30.RecordAuditPkg30(o.ID, "captured")`). Nothing inside pkg30 itself calls it.

## 7. `pkg26_retry_limit`

- **Effective default: 5**, defined in **`gen/defaults.gen.json:188`** (`"pkg26_retry_limit": 5`).
- **Consumer:** `CapturePaymentPkg26` (`internal/pkg26/charge.go:16`) via `config.ScaledKnob("pkg26_retry_limit", 2)`. The `2` is only a last-ditch fallback if the generated defaults file is missing — `ScaledKnob` (`internal/config/scaled.go`) checks the env var, then `gen/defaults.gen.json`, before that fallback. `config/app.yaml` explicitly states retry limits do *not* live there, and the `pkg26_retry_limit: 5` inside `web/dist/bundle.min.js` is client-side noise.
- **Caller of the consumer:** `ProcessOrderPkg25` (`internal/pkg25/pipeline.go:30`) — the pkg25 vertical bills through the pkg26 gateway shard; nothing in pkg26's own pipeline calls it.

## 8. Worker job-type count and the pkg09 fulfiller

**43 job types** are registered at startup: `workers/handlers/__init__.py` imports 43 modules (3 base — `inventory_handler`, `order_handler`, `refund_handler` — plus 40 shard `pkgNN_handler` modules), each containing exactly one `@register` decorator with a unique job_type string (verified by counting and deduping the decorators).

pkg09-vertical orders: the Go pipeline publishes `"pkg09.fulfill"` (`internal/pkg09/pipeline.go:40`), fulfilled by **`workers/handlers/pkg09_handler.py::fulfill_pkg09`** (`@register("pkg09.fulfill")`). The same-named function in `pkg09_legacy.py` is unregistered dead code.
