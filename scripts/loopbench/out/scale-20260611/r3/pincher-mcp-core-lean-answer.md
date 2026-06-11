All eight questions are now resolved with verified evidence. Here are the answers.

## 1. `pkg07/submit` cross-language trace

1. **TS action**: `registerAction("pkg07/submit", …)` in `web/src/pkg07/actions.ts:5` → calls `processPkg07Order()` (`web/src/pkg07/process.ts:7`).
2. **TS HTTP client**: `postShardOrder("pkg07", …)` (`web/src/api/scaledClient.ts:3`) → `POST /api/v1/pkg/pkg07/orders`.
3. **Go route**: `handleScaledCreate` (`internal/api/scaled_routes.go:19`) builds the action string `"pkg07" + ".process"` and calls `dispatch.Dispatch(ctx, "pkg07.process", body)`.
4. **Go dispatch**: `init()` in `internal/pkg07/register.go` registered `"pkg07.process"` → `processActionPkg07` → `ProcessOrderPkg07` (`internal/pkg07/pipeline.go:26`).
5. **Queue**: `ProcessOrderPkg07` ends with `queue.Publish("pkg07.fulfill", …)` (`internal/pkg07/pipeline.go:39`), which appends a JSON line with `job_type: "pkg07.fulfill"` to `/tmp/orderflow-queue.jsonl` (`internal/queue/queue.go`).
6. **Python**: `worker_main.py:route()` looks up `job_type` in `lib/registry.py`'s `HANDLERS`; the decorator `@register("pkg07.fulfill")` binds it to **`fulfill_pkg07` in `workers/handlers/pkg07_handler.py:8`** — the live handler (its decorator-free twin in `pkg07_legacy.py` is never registered).

Connecting string keys: `"pkg07/submit"` (UI action) → `"pkg07.process"` (Go dispatch action) → `"pkg07.fulfill"` (queue job_type).

## 2. Payment capture for a pkg21 order

**`CapturePaymentPkg22` in `internal/pkg22/charge.go`.** The live pipeline `ProcessOrderPkg21` (`internal/pkg21/pipeline.go:30`) captures via the *next* shard's gateway: `pkg22.CapturePaymentPkg22(ctx, "inv-"+o.ID, o.Total)`. The lookalike `CapturePaymentPkg21` in `internal/pkg21/charge.go` is a decoy — its own comment says it's called only from upstream of pkg21 (it serves pkg20's pipeline), and the dead `ProcessOrderPkg21V1` does no capture at all.

## 3. `ProcessOrderPkg33V1` — dead or alive?

**Dead.** Evidence: a repo-wide grep for `ProcessOrderPkg33V1` finds only its definition (`internal/pkg33/dead_v1.go:12`, with a comment saying it's kept for rollback) and a "not to be confused with" comment in `internal/pkg33/pipeline.go:25`. A pincher inbound trace returns zero callers. The dispatch wiring in `internal/pkg33/register.go` registers only the live `ProcessOrderPkg33` (via `processActionPkg33`), and `cmd/orderd/scaled_imports.go` only triggers those `init()` registrations — nothing references the V1 function.

## 4. Can `fulfill_pkg14` in `workers/handlers/pkg14_legacy.py` ever run?

**No.** The worker dispatches strictly through `lib/registry.py`'s `HANDLERS` dict, populated only by `@register(...)` decorator side effects at import time (`worker_main.py:route()`). Two independent blocks apply:
- `workers/handlers/__init__.py` deliberately imports only the `*_handler` modules ("the `*_legacy` twins are deliberately NOT imported"), so `pkg14_legacy.py` is never even imported.
- Even if it were imported, its `fulfill_pkg14` has no `@register` decorator (`workers/handlers/pkg14_legacy.py:10`), so it would never enter `HANDLERS`.

The `"pkg14.fulfill"` job type is instead bound to the same-named but distinct `fulfill_pkg14` in `workers/handlers/pkg14_handler.py:8` via `@register("pkg14.fulfill")`.

## 5. Blast radius of changing `SaveRecordPkg12`'s signature

Three non-test call sites, all inside `internal/pkg12/`:
- `ProcessOrderPkg12` — `internal/pkg12/pipeline.go:34`
- `RefundOrderPkg12` — `internal/pkg12/refund.go:24`
- `ProcessOrderPkg12V1` — `internal/pkg12/dead_v1.go:17` (dead code, but it still compiles, so it must be updated)

Confirmed by both pincher inbound trace and a repo-wide grep (no other `*_test.go`-excluded hits).

## 6. Direct callers of `RecordAuditPkg30`

Exactly one: **`ProcessOrderPkg29` in `internal/pkg29/pipeline.go:37`** (`pkg30.RecordAuditPkg30(o.ID, "captured")`). Note the cross-shard pattern: each pkgNN pipeline audits through pkg(NN+1), so pkg30's own pipeline does *not* call it.

## 7. `pkg26_retry_limit`

- **Effective default: 5**, defined in **`gen/defaults.gen.json:188`**. The consumer reads it via `config.ScaledKnob("pkg26_retry_limit", 2)` (`internal/config/scaled.go`), which checks the env var, then the generated defaults file; the in-code `2` is only a last-ditch fallback if `gen/defaults.gen.json` is missing. The `pkg26_retry_limit: 5` inside `web/dist/bundle.min.js` is a frontend-bundle decoy with no effect on the Go service.
- **Consumer**: `CapturePaymentPkg26` — `internal/pkg26/charge.go:16`.
- **Caller of the consumer**: `ProcessOrderPkg25` — `internal/pkg25/pipeline.go:30` (again the cross-shard gateway pattern; nothing in pkg26's own pipeline calls it).

## 8. Worker job-type count and the pkg09 handler

**43 job types** are registered at startup: `workers/handlers/__init__.py` imports 3 base handlers — `order_handler` (`"process_order"`), `refund_handler` (`"process_refund"`), `inventory_handler` (`"inventory.sync"`) — plus the 40 shard handlers `pkg01_handler`…`pkg40_handler`, each containing exactly one `@register("pkgNN.fulfill")` (verified: 40 decorator occurrences, all distinct keys, so 43 entries in `HANDLERS`; `legacy.py` and the `*_legacy` twins are not imported).

pkg09 orders are fulfilled by **`fulfill_pkg09` in `workers/handlers/pkg09_handler.py:8`**, registered under `"pkg09.fulfill"` — matching the `queue.Publish("pkg09.fulfill", …)` in `internal/pkg09/pipeline.go:40`.
