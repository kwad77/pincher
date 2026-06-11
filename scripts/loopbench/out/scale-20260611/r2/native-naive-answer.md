All eight answered. (Note: pincher MCP wasn't connected in this session, so I used direct file inspection per the CLAUDE.md fallback rule.)

## 1. Cross-language path for `"pkg07/submit"`

The order is ultimately fulfilled by **`fulfill_pkg07` in `workers/handlers/pkg07_handler.py`**. The full path:

1. **TS action**: `web/src/pkg07/actions.ts:5` registers `"pkg07/submit"` (via `registerAction` in `web/src/registry.ts`) → calls `processPkg07Order` (`web/src/pkg07/process.ts:7`).
2. **HTTP**: `processPkg07Order` → `postShardOrder("pkg07", ...)` (`web/src/api/scaledClient.ts:3`) → `POST /api/v1/pkg/pkg07/orders`.
3. **Go API**: `handleScaledCreate` (`internal/api/scaled_routes.go:19`) builds the action string `"pkg07" + ".process"` and calls `dispatch.Dispatch` (`internal/dispatch/registry.go:28`).
4. **Go shard**: `internal/pkg07/register.go:14` bound `"pkg07.process"` → `processActionPkg07` → `ProcessOrderPkg07` (`internal/pkg07/pipeline.go:26`).
5. **Queue**: `ProcessOrderPkg07` calls `queue.Publish("pkg07.fulfill", ...)` (`pipeline.go:40`); `queue.Publish` writes a JSON line with `"job_type": "pkg07.fulfill"` to `/tmp/orderflow-queue.jsonl` (`internal/queue/queue.go:13`).
6. **Python**: `workers/worker_main.py:36` (`route`) looks up `HANDLERS["pkg07.fulfill"]`, populated by the `@register("pkg07.fulfill")` decorator on `fulfill_pkg07` (`workers/handlers/pkg07_handler.py:7-8`), imported via `workers/handlers/__init__.py`.

Connecting string keys: `"pkg07/submit"` (TS registry) → URL path `pkg07` → Go action `"pkg07.process"` → job type `"pkg07.fulfill"`.

## 2. Payment capture for a pkg21 order

**`CapturePaymentPkg22` in `internal/pkg22/charge.go:15`.** The live pipeline `ProcessOrderPkg21` routes capture through the *next* shard's gateway: `pkg22.CapturePaymentPkg22(...)` at `internal/pkg21/pipeline.go:30` (the actual gateway stub it retries against is `gatewayChargePkg22` in the same file). The decoy `CapturePaymentPkg21` in `internal/pkg21/charge.go` serves pkg20's orders, not pkg21's.

## 3. `ProcessOrderPkg33V1` — dead or alive?

**Dead.** A repo-wide grep for `ProcessOrderPkg33V1` finds only its definition (`internal/pkg33/dead_v1.go:12`) and a doc-comment mention in `internal/pkg33/pipeline.go:25`. `internal/pkg33/register.go` registers only the live `processActionPkg33`/`refundActionPkg33` (wrapping `ProcessOrderPkg33`), and no test, cmd, or generated file references the V1 function.

## 4. Can `fulfill_pkg14` in `pkg14_legacy.py` ever run?

**No, for two independent reasons.** The worker dispatches solely through the `HANDLERS` dict in `workers/lib/registry.py`, populated only by `@register(...)` decorator side effects at import time (`worker_main.py:36-42`). (1) `fulfill_pkg14` in `workers/handlers/pkg14_legacy.py:10` has no `@register` decorator, so even importing it would register nothing. (2) The module is never imported anyway — `workers/handlers/__init__.py` imports only the `*_handler` twins and explicitly notes the `*_legacy` modules are deliberately excluded. The live `pkg14.fulfill` handler is `fulfill_pkg14` in `pkg14_handler.py`.

## 5. Blast radius of changing `SaveRecordPkg12`'s signature

Three non-test call sites, all inside `internal/pkg12/`:
- `ProcessOrderPkg12` — `internal/pkg12/pipeline.go:34`
- `RefundOrderPkg12` — `internal/pkg12/refund.go:24`
- `ProcessOrderPkg12V1` — `internal/pkg12/dead_v1.go:17` (dead code, but still compiled, so it must be updated)

## 6. Direct non-test callers of `RecordAuditPkg30`

Exactly one: **`ProcessOrderPkg29`** in `internal/pkg29/pipeline.go:37` (the cross-shard audit call in the pkg29 live pipeline). No other non-test code references it.

## 7. `pkg26_retry_limit`

- **Effective default: 5**, defined in `gen/defaults.gen.json:188`. The hardcoded `2` in the consumer is only a fallback used if the generated defaults file is missing, and `config/app.yaml` explicitly states retry limits do *not* live there (it punts to the gen file).
- **Consumer**: `CapturePaymentPkg26` at `internal/pkg26/charge.go:16`, via `config.ScaledKnob("pkg26_retry_limit", 2)` (`internal/config/scaled.go:12`, env-overridable as `PKG26_RETRY_LIMIT`).
- **Caller of the consumer**: `ProcessOrderPkg25` at `internal/pkg25/pipeline.go:30` — pkg25's pipeline routes capture through the pkg26 gateway shard.

## 8. Worker job-type count and pkg09 handler

**43 job types** are registered at startup: 40 shard handlers (`pkg01.fulfill` … `pkg40.fulfill`) plus `inventory_handler`, `order_handler` (`process_order`), and `refund_handler` (`process_refund`) — 43 `@register` decorators across the 43 modules imported by `workers/handlers/__init__.py`, all with unique keys (verified by sort/uniq). pkg09 orders are fulfilled by **`fulfill_pkg09` in `workers/handlers/pkg09_handler.py:8`**, registered under `"pkg09.fulfill"`, matching the publish key at `internal/pkg09/pipeline.go:40`.
