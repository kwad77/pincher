All ten answered. (Note: the project CLAUDE.md prefers pincher MCP tools, but no pincher server is connected in this session, so I used targeted grep/read instead.)

**1. POST /api/v1/orders → business logic.** The hops are:
- `s.mux.HandleFunc("POST /api/v1/orders", s.handleCreateOrder)` — `internal/api/server.go:15`
- `(*Server).handleCreateOrder` calls `dispatch.Dispatch(r.Context(), "order.process", body)` — `internal/api/handlers.go:14,20`
- `dispatch.Dispatch` looks the string `"order.process"` up in a handler map (`internal/dispatch/registry.go:28`). The indirection is resolved at program start by the `init()` in `internal/orders/register.go:12-14`, which calls `dispatch.Register("order.process", processAction)`.
- `processAction` (`internal/orders/register.go:17`) unmarshals the JSON and calls **`orders.ProcessOrder`** (`internal/orders/process.go:25`) — the function that validates, captures payment, persists, and enqueues.

**2. Which ProcessOrder charges the card?** `billing.ProcessOrder` in `internal/billing/charge.go:15`. It only calls `chargeCard` in a retry loop — payment capture and nothing else. (`orders.ProcessOrder` is the full pipeline; the Python `process_order` is fulfillment; the TS `processOrder` is browser state + HTTP.)

**3. `ProcessOrderV1` — dead.** A repo-wide grep for `ProcessOrderV1` across all `.go` files finds only its definition in `internal/orders/process_v1.go:13` (plus its own doc comment). Nothing registers it with `dispatch.Register` and nothing calls it; the only registrations in `internal/orders/register.go` are `order.process`/`order.refund`, which bind `processAction`/`refundAction`, not V1.

**4. `legacy.py:process_order` — can never run.** The worker routes jobs via the `HANDLERS` dict in `workers/lib/registry.py`, populated only by `@register(...)` decorator side effects when the `handlers` package is imported (`workers/worker_main.py:13-14`, `route()` at line 35). Two independent reasons it's unreachable: (a) `workers/handlers/__init__.py` deliberately does not import `legacy` (only `inventory_handler`, `order_handler`, `refund_handler`), so the module is never loaded; and (b) even if it were imported, `legacy.process_order` (`workers/handlers/legacy.py:10`) carries no `@register` decorator, so it would never enter `HANDLERS`.

**5. Blast radius of changing `store.SaveOrder`.** Non-test call sites:
- `(*Server).handleAdminImport` — `internal/api/admin.go:20`
- `orders.ProcessOrder` — `internal/orders/process.go:34`
- `orders.ProcessOrderV1` — `internal/orders/process_v1.go:18` (dead, but still compiles against the signature)
- `orders.RefundOrder` — `internal/orders/refund.go:28`

**6. Checkout click → Python fulfillment.** The Python function is **`process_order` in `workers/handlers/order_handler.py:8`**. Full path:
1. `wireCheckout` click handler calls `dispatch("order/submit", {})` — `web/src/ui/checkout.ts:8`
2. The TS action registry (`web/src/registry.ts:11`) resolves the string `"order/submit"`, registered in `web/src/orders/actions.ts:6` (side-effect import from `web/src/main.ts:2`) to call TS `processOrder` (`web/src/orders/processOrder.ts:7`)
3. That calls `createOrder`, which does `fetch("POST /api/v1/orders")` — `web/src/api/client.ts:10-12`
4. Go: `handleCreateOrder` → `dispatch.Dispatch("order.process", …)` → `processAction` → `orders.ProcessOrder` (question 1)
5. `orders.ProcessOrder` calls `queue.Publish("process_order", …)` (`internal/orders/process.go:37`); `queue.Publish` writes the job with `"job_type": "process_order"` to `/tmp/orderflow-queue.jsonl` (`internal/queue/queue.go:13-19`)
6. Python worker reads that file, looks up `job_type` `"process_order"` in `HANDLERS` (`workers/worker_main.py:35-41`), which maps to `@register("process_order") def process_order` in `workers/handlers/order_handler.py:7-8` — reserves stock, ships, emails.

Connecting string keys: `"order/submit"` (TS registry), `"order.process"` (Go dispatch), `"process_order"` (queue job_type / Python registry).

**7. `order_retry_limit` default = 7.** The Go consumer is `config.OrderRetryLimit()` (`internal/config/config.go:29`), called by `billing.ProcessOrder` (`internal/billing/charge.go:16`). Precedence: `ORDER_RETRY_LIMIT` env var, then the generated defaults file **`gen/defaults.gen.json:136` (`"order_retry_limit": 7`)**, then a hardcoded last-ditch fallback of 3 (`config.go:39`). So the effective default is 7. Decoys: `config/app.yaml` explicitly says tuning defaults are *not* there; `workers/lib/config.py:33` has a worker-side mirror defaulting to 3 (not consumed by Go); fixtures contain `retry_limit_at_capture` values of 3/5 that are just data.

**8. Job types registered at worker startup.** Via `handlers/__init__.py` imports:
- `"process_order"` → `workers.handlers.order_handler.process_order` (`order_handler.py:7-8`)
- `"inventory.sync"` → `workers.handlers.inventory_handler.sync_inventory` (`inventory_handler.py:7-8`)
- `"process_refund"` → `workers.handlers.refund_handler.process_refund` (`refund_handler.py:7-8`)

**9. `web/src/orders/processOrderOld.ts` — dead.** Grepping all of `web/src` for `processOrderOld` finds zero imports (only the generated `web/dist/bundle.min.js` exists as an artifact, and the source module's own header says nothing imports it). The live checkout path uses `processOrder` from `web/src/orders/processOrder.ts`, wired through the action registry by `web/src/orders/actions.ts:6-8`.

**10. Direct non-test callers of `billing.ProcessOrder`.** Exactly two:
- `orders.ProcessOrder` — `internal/orders/process.go:30`
- `reconcileOnce` — `cmd/reconciled/main.go:23-26`
