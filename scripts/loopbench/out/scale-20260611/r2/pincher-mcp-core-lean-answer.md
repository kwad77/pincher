All eight answered. Everything below was verified against the actual source (pincher traces cross-checked with grep), ignoring the generated decoys in `gen/`, `fixtures/`, and `web/dist/`.

**1. `pkg07/submit` cross-language trace**
- `registerAction("pkg07/submit", …)` in `web/src/pkg07/actions.ts:5` calls `processPkg07Order` (`web/src/pkg07/process.ts:7`).
- That calls `postShardOrder("pkg07", …)` (`web/src/api/scaledClient.ts:3`), which POSTs to `/api/v1/pkg/pkg07/orders`.
- Go: `handleScaledCreate` (`internal/api/scaled_routes.go:19`) builds the action string `"pkg07" + ".process"` and calls `dispatch.Dispatch` (`internal/dispatch/registry.go:28`).
- `"pkg07.process"` was bound in `init()` of `internal/pkg07/register.go:14` to `processActionPkg07`, which calls `ProcessOrderPkg07` (`internal/pkg07/pipeline.go:26`).
- That pipeline ends with `queue.Publish("pkg07.fulfill", …)` (`pipeline.go:40`); `Publish` writes the job with `"job_type": "pkg07.fulfill"` (`internal/queue`, line 13-14) to the JSONL queue.
- Python: `worker_main.route` (`workers/worker_main.py:36`) looks up `HANDLERS["pkg07.fulfill"]`, populated by the `@register("pkg07.fulfill")` decorator — so the fulfiller is **`fulfill_pkg07` in `workers/handlers/pkg07_handler.py:8`** (not the unregistered twin in `pkg07_legacy.py`).

**2. Payment capture for a live pkg21 order**
**`CapturePaymentPkg22` in `internal/pkg22/charge.go:15`.** The live pipeline `ProcessOrderPkg21` captures "through the pkg22 gateway shard" at `internal/pkg21/pipeline.go:30`. The lookalike `CapturePaymentPkg21` (`internal/pkg21/charge.go`) serves *upstream* shards (it's called from `pkg20`'s pipeline), not pkg21 orders.

**3. `ProcessOrderPkg33V1` — dead.** Grep across all `.go` files finds it only at its definition (`internal/pkg33/dead_v1.go:12`) and in a "not to be confused with" comment in `internal/pkg33/pipeline.go:25`. The dispatch registration in `internal/pkg33/register.go` binds `"pkg33.process"` to `processActionPkg33` → `ProcessOrderPkg33`, not the V1. Pincher's inbound trace also returns zero callers.

**4. `fulfill_pkg14` in `workers/handlers/pkg14_legacy.py` — can never run.** Two independent blocks: (a) the only registration mechanism is the `@register(job_type)` decorator populating `lib/registry.HANDLERS` (`workers/lib/registry.py:6-13`), and this function has no decorator; (b) the module isn't even imported — `workers/handlers/__init__.py:13` imports only the `*_handler` modules ("the `*_legacy` twins are deliberately NOT imported"), so its module-level code never executes. The dispatcher (`worker_main.py:38`) only calls what's in `HANDLERS`, and there are no manual `HANDLERS[...]` assignments anywhere.

**5. Blast radius of `SaveRecordPkg12` (`internal/pkg12/store.go:21`)** — three non-test call sites, all inside pkg12:
- `ProcessOrderPkg12` — `internal/pkg12/pipeline.go:34`
- `RefundOrderPkg12` — `internal/pkg12/refund.go:24`
- `ProcessOrderPkg12V1` — `internal/pkg12/dead_v1.go:17` (dead but compiled non-test code, so it still must be updated)

**6. Direct callers of `RecordAuditPkg30` (`internal/pkg30/store.go:44`)** — exactly one: **`ProcessOrderPkg29` in `internal/pkg29/pipeline.go:37`**. Note the off-by-one shard pattern: pkg29's pipeline audits via the pkg30 store; nothing in pkg30 itself calls it.

**7. `pkg26_retry_limit` effective default = 5**, defined in `gen/defaults.gen.json`. The consumer is `CapturePaymentPkg26` (`internal/pkg26/charge.go:16`), which calls `config.ScaledKnob("pkg26_retry_limit", 2)`; `ScaledKnob` (`internal/config/scaled.go:12`) loads `gen/defaults.gen.json` and only uses the inline `2` if that file is missing, so the inline literal is not the effective default. `config/app.yaml` explicitly excludes tuning defaults, and `web/dist/bundle.min.js` carries a different, decoy value table. The function that calls the consumer is **`ProcessOrderPkg25` (`internal/pkg25/pipeline.go:30`)** — the same upstream-shard pattern as Q2.

**8. The worker registers 43 job types.** `workers/handlers/__init__.py` imports 43 live modules (3 base: `inventory_handler` → `inventory.sync`, `order_handler` → `process_order`, `refund_handler` → `process_refund`, plus 40 shard handlers, each registering exactly one `pkgNN.fulfill` type — no duplicates, verified by uniq). pkg09-vertical orders are fulfilled by **`fulfill_pkg09` in `workers/handlers/pkg09_handler.py`**, registered under `"pkg09.fulfill"` (the decorator-free `pkg09_legacy.fulfill_pkg09` is never imported or registered).
