All eight questions answered. Here are the findings with evidence:

## 1. Cross-language trace for "pkg07/submit"

1. **TS**: `web/src/pkg07/actions.ts:5` registers action `"pkg07/submit"` → calls `processPkg07Order` (`web/src/pkg07/process.ts:7`), which calls `postShardOrder("pkg07", …)` (`web/src/api/scaledClient.ts:3`) → `POST /api/v1/pkg/pkg07/orders`.
2. **Go API**: route registered in `internal/api/scaled_routes.go:14`; `handleScaledCreate` (line 19) builds the dispatch key `"pkg07" + ".process"` and calls `dispatch.Dispatch`.
3. **Go dispatch**: `internal/pkg07/register.go:14` binds `"pkg07.process"` → `processActionPkg07` → `ProcessOrderPkg07` (`internal/pkg07/pipeline.go:26`).
4. **Queue**: `ProcessOrderPkg07` publishes job type `"pkg07.fulfill"` (`pipeline.go:40`).
5. **Python**: `workers/worker_main.py` routes by `job_type` via `lib/registry.py`'s `HANDLERS`; `@register("pkg07.fulfill")` binds **`fulfill_pkg07` in `workers/handlers/pkg07_handler.py:8`** — the function that ultimately fulfills the order (not the unregistered twin in `pkg07_legacy.py`).

## 2. Payment capture for a pkg21 order

**`CapturePaymentPkg22` in `internal/pkg22/charge.go:15`.** The live pkg21 pipeline `ProcessOrderPkg21` (`internal/pkg21/pipeline.go:30`) captures through the *pkg22* gateway shard, not pkg21's own. The lookalike `CapturePaymentPkg21` (`internal/pkg21/charge.go`) serves **pkg20** orders (`internal/pkg20/pipeline.go:30`). (The actual stub charge inside is `gatewayChargePkg22`, but `CapturePaymentPkg22` is the capture function.)

## 3. `ProcessOrderPkg33V1` — dead

**Dead.** Evidence: a repo-wide grep finds only its definition (`internal/pkg33/dead_v1.go:12`) and a doc-comment mention in `pipeline.go:25`; pincher's inbound trace (including tests) returns 0 callers; `internal/pkg33/register.go` registers only `processActionPkg33`/`refundActionPkg33`, and `processActionPkg33` calls the live `ProcessOrderPkg33` (register.go:23). Its queue topic `"pkg33.v1"` also has no Python handler (workers register `"pkg33.fulfill"` only).

## 4. Can `fulfill_pkg14` (workers/handlers/pkg14_legacy.py) ever run?

**No.** Registration happens solely via the `@register(job_type)` decorator populating `HANDLERS` in `workers/lib/registry.py`, triggered by module import from `workers/handlers/__init__.py`. `pkg14_legacy.py` (a) has **no `@register` decorator** on `fulfill_pkg14`, and (b) is **never imported** — `__init__.py:13` imports only `*_handler` modules ("the `*_legacy` twins are deliberately NOT imported"). The worker (`worker_main.py:38`) only ever invokes functions found in `HANDLERS`, so the function is unreachable for any queued job.

## 5. Blast radius of `SaveRecordPkg12` (internal/pkg12/store.go:21)

Three non-test call sites, all in `internal/pkg12/`:
- `ProcessOrderPkg12` — `internal/pkg12/pipeline.go:34`
- `RefundOrderPkg12` — `internal/pkg12/refund.go:24`
- `ProcessOrderPkg12V1` (dead code, but still must compile) — `internal/pkg12/dead_v1.go:17`

## 6. Depth-1 callers of `RecordAuditPkg30` (internal/pkg30/store.go:44)

Exactly one: **`ProcessOrderPkg29`** in `internal/pkg29/pipeline.go:37` (cross-shard audit; pkg30 audits pkg29's orders, mirroring the gateway-chain pattern). Confirmed by both pincher trace and grep.

## 7. `pkg26_retry_limit`

- **Effective default: 5**, defined in **`gen/defaults.gen.json:188`**. `config.ScaledKnob` (`internal/config/scaled.go:12`) checks the `PKG26_RETRY_LIMIT` env var, then the loaded generated-defaults map (`load()` in `internal/config/config.go:18` reads `gen/defaults.gen.json`); the in-code fallback `2` at `internal/pkg26/charge.go:16` applies only if that file is missing. Decoys: `fixtures/scaled_fixture_*.json` carry `retry_limit_at_capture` values (2/4), and `web/dist/bundle.min.js` embeds a client-side copy of the table.
- **Consumer**: `CapturePaymentPkg26` — `internal/pkg26/charge.go:16`.
- **Caller of the consumer**: `ProcessOrderPkg25` — `internal/pkg25/pipeline.go:30` (pkg25's pipeline captures through the pkg26 gateway shard).

## 8. Worker job-type count and the pkg09 handler

**43 job types** total: 3 base handlers (`inventory.sync` in `inventory_handler.py`, `process_order` in `order_handler.py`, `process_refund` in `refund_handler.py`) + 40 shard handlers, one `@register("pkgNN.fulfill")` each across `pkg01_handler.py`–`pkg40_handler.py` (verified: 40 decorator occurrences, all distinct keys). The `*_legacy.py` files and `legacy.py` register nothing. pkg09-vertical orders are fulfilled by **`fulfill_pkg09` in `workers/handlers/pkg09_handler.py:8`**, registered under `"pkg09.fulfill"`.
