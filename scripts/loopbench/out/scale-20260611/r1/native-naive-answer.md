All eight answered and verified. (Note: the pincher MCP server from CLAUDE.md isn't connected in this session, so I used direct search per its fallback clause.)

**1. `pkg07/submit` → Python fulfillment path**

| Hop | Function / key | Evidence |
|---|---|---|
| TS action registry | `registerAction("pkg07/submit", …)` → `processPkg07Order` | `web/src/pkg07/actions.ts:5`, `web/src/pkg07/process.ts:7` |
| TS HTTP client | `postShardOrder("pkg07", …)` → `POST /api/v1/pkg/pkg07/orders` | `web/src/api/scaledClient.ts:4` |
| Go route | `handleScaledCreate` on `POST /api/v1/pkg/{pkg}/orders` | `internal/api/scaled_routes.go:14,19` |
| Go dispatch | builds key `"pkg07.process"` (`pathValue + ".process"`) → registry maps it to `processActionPkg07` → `ProcessOrderPkg07` | `internal/api/scaled_routes.go:25`, `internal/pkg07/register.go:14` |
| Queue | `queue.Publish("pkg07.fulfill", …)` writes a JSONL job | `internal/pkg07/pipeline.go:40`, `internal/queue/queue.go:13` |
| Python worker | `worker_main.py` imports `handlers`, `route()` looks up `HANDLERS["pkg07.fulfill"]` → **`fulfill_pkg07`** | `workers/worker_main.py:13,36-42`, `workers/lib/registry.py:3-10`, `workers/handlers/pkg07_handler.py:7-8` |

The connecting strings are `"pkg07/submit"` → URL path `pkg07` → `"pkg07.process"` → `"pkg07.fulfill"`.

**2. pkg21 payment capture**
**`CapturePaymentPkg22` in `internal/pkg22/charge.go:15`.** The live pipeline `ProcessOrderPkg21` captures through the *neighboring* gateway shard: `pkg22.CapturePaymentPkg22(ctx, "inv-"+o.ID, o.Total)` at `internal/pkg21/pipeline.go:30` (registered live via `dispatch.Register("pkg21.process", …)` at `internal/pkg21/register.go:14`). It retries the raw gateway stub `gatewayChargePkg22` (`internal/pkg22/charge.go:27`). The decoy `CapturePaymentPkg21` (`internal/pkg21/charge.go:15`) is called only by *pkg20's* pipeline (`internal/pkg20/pipeline.go:30`), not by any pkg21 order.

**3. `ProcessOrderPkg33V1` — dead.**
Repo-wide search finds no non-test calls or registrations. `internal/pkg33/register.go:13-16` registers only `"pkg33.process"` → `processActionPkg33` (which calls the live `ProcessOrderPkg33`) and `"pkg33.refund"`. The only mentions are its own definition (`internal/pkg33/dead_v1.go:9-21`) and a comment in `internal/pkg33/pipeline.go:25` calling it dead. No references in `cmd/`, `workers/`, `web/`, or via string/reflection lookup.

**4. `fulfill_pkg14` in `workers/handlers/pkg14_legacy.py` — can never run.**
Registration happens only via the `@register` decorator populating the `HANDLERS` dict (`workers/lib/registry.py:3-10`), triggered as an import side effect when `worker_main.py:13` imports `handlers`. Two independent blockers: (a) `workers/handlers/__init__.py` deliberately does not import `pkg14_legacy` (only `pkg14_handler`, line 27 — the comment says the `*_legacy` twins are intentionally excluded), and (b) `fulfill_pkg14` in the legacy module carries no `@register` decorator anyway. So it never enters `HANDLERS`, and `route()` (`workers/worker_main.py:36-42`) can only dispatch through `HANDLERS`.

**5. Blast radius of `SaveRecordPkg12`** — 3 non-test call sites, all inside `internal/pkg12/` (verified repo-wide; no cross-shard or indirect references):
- `ProcessOrderPkg12` — `internal/pkg12/pipeline.go:34`
- `RefundOrderPkg12` — `internal/pkg12/refund.go:24`
- `ProcessOrderPkg12V1` — `internal/pkg12/dead_v1.go:17` (itself dead code, but would still need updating to compile)

**6. Direct callers of `RecordAuditPkg30`** — exactly one, and it's cross-shard:
- `ProcessOrderPkg29` — `internal/pkg29/pipeline.go:37` (`pkg30.RecordAuditPkg30(o.ID, "captured")`)

**7. `pkg26_retry_limit`**
- **Effective default: 5**, defined at `gen/defaults.gen.json:188` — the one file the config loader actually reads (`os.ReadFile("gen/defaults.gen.json")` in `internal/config/config.go:20`). Precedence in `ScaledKnob` (`internal/config/scaled.go:12-23`): env var `PKG26_RETRY_LIMIT` → generated defaults (5) → in-code fallback `2` only if the JSON file is missing. Decoys: the in-code fallback `2`, and `web/dist/bundle.min.js` which embeds a frontend `pkg26_retry_limit` constant never read by the Go service; `config/app.yaml` explicitly states tuning defaults don't live there.
- **Consumer:** `CapturePaymentPkg26` — `internal/pkg26/charge.go:15-16` (`config.ScaledKnob("pkg26_retry_limit", 2)`).
- **Its caller:** `ProcessOrderPkg25` — `internal/pkg25/pipeline.go:30` (the only non-test caller; same neighbor-shard pattern as Q2).

**8. Worker registration count + pkg09 handler**
**43 job types**: 40 shard handlers (`pkg01.fulfill` … `pkg40.fulfill`) plus `process_order`, `process_refund`, and `inventory.sync`. Verified three ways: 43 `@register(` lines in `workers/handlers/*.py`, 43 distinct key strings (no collisions/overwrites), 43 imports in `workers/handlers/__init__.py`. pkg09 orders enqueue job type `"pkg09.fulfill"` (`internal/pkg09/pipeline.go:40`), fulfilled by **`workers/handlers/pkg09_handler.py` : `fulfill_pkg09`** (registered at line 7).
