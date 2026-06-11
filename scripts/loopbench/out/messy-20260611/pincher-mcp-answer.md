All ten questions answered. Evidence below.

**1. `POST /api/v1/orders` → order business logic.** The route is bound in `api.NewServer` (`internal/api/server.go:15`) to `(*Server).handleCreateOrder` (`internal/api/handlers.go:14`). That handler does **not** call the logic directly — it calls `dispatch.Dispatch(ctx, "order.process", body)` (`internal/api/handlers.go:20`). `dispatch.Dispatch` (`internal/dispatch/registry.go:28`) looks up the string `"order.process"` in the `handlers` map, which was populated at program start by `orders.init` (`internal/orders/register.go:12`) calling `dispatch.Register("order.process", processAction)`. `processAction` (`internal/orders/register.go:17`) unmarshals the JSON payload and calls **`orders.ProcessOrder`** (`internal/orders/process.go:25`), which does validate → payment capture → persist → enqueue.

**2. The one that charges the card:** **`billing.ProcessOrder`** in `internal/billing/charge.go:15` — it loops up to `config.OrderRetryLimit()` attempts calling `chargeCard`. (`orders.ProcessOrder` only *delegates* capture to it; the TS and Python namesakes do UI submission and fulfillment respectively; `legacy.py`'s charging path is dead, see Q4.)

**3. `ProcessOrderV1` — dead.** Pincher's inbound trace on `internal/orders/process_v1.go::orders.ProcessOrderV1` returns zero callers; `orders.init` registers only `processAction` and `refundAction` (`internal/orders/register.go:12-15`); and a repo-wide grep for `ProcessOrderV1` / `process_order_v1` outside tests hits only `process_v1.go` itself. It would also publish job type `"process_order_v1"`, which no Python handler registers — doubly dead.

**4. `legacy.process_order` — can never run.** Worker dispatch is decorator-driven: `@register(job_type)` inserts into `HANDLERS` in `workers/lib/registry.py:6-12`, and `worker_main.route` (`workers/worker_main.py:36-42`) only executes functions found in that dict. `workers/handlers/legacy.py:10` defines `process_order` **without** the `@register` decorator, and the package initializer `workers/handlers/__init__.py:8-10` deliberately does not import `legacy` (its docstring says so explicitly). So it is neither imported at startup nor registered — unreachable for any queued job.

**5. `store.SaveOrder` blast radius (non-test, depth-1, per pincher trace):**
- `api.(*Server).handleAdminImport` — `internal/api/admin.go`
- `orders.ProcessOrder` — `internal/orders/process.go`
- `orders.ProcessOrderV1` — `internal/orders/process_v1.go` (dead, but still compiles against the signature)
- `orders.RefundOrder` — `internal/orders/refund.go`

**6. Checkout click → fulfillment, full path:**
`wireCheckout` (`web/src/ui/checkout.ts:5`) → `dispatch("order/submit", {})` (`web/src/registry.ts:11`) → action registered by `registerAction("order/submit", …)` in `web/src/orders/actions.ts:6` → **`processOrder`** (`web/src/orders/processOrder.ts:7`) → `createOrder` (`web/src/api/client.ts:10`) → `fetch POST /api/v1/orders` → Go `handleCreateOrder` → `dispatch.Dispatch(…, "order.process", …)` → `processAction` → `orders.ProcessOrder` (`internal/orders/process.go:25`) → `queue.Publish("process_order", {order_id, skus, email})` → JSON-line queue read by `worker_main.drain_once`/`route` (`workers/worker_main.py:25-42`) → `HANDLERS["process_order"]` → **`workers.handlers.order_handler.process_order`** (`workers/handlers/order_handler.py:7`), which reserves stock, creates the shipment, marks fulfilled, and emails. Connecting string keys: `"order/submit"` (TS action), `"order.process"` (Go action), `"process_order"` (queue job type).

**7. `order_retry_limit` effective default: 7**, defined in the generated `gen/defaults.gen.json:136`. It is consumed by `config.OrderRetryLimit` (`internal/config/config.go:29`), which prefers the `ORDER_RETRY_LIMIT` env var, then the loaded `gen/defaults.gen.json` (via `config.load`, `internal/config/config.go:18`), and only returns the hardcoded `3` if the generated file is missing. Values elsewhere (fixtures/, etc.) are not read by this code path.

**8. Job types registered at worker startup** (via `workers/handlers/__init__.py` side-effect imports):
- `"process_order"` → `workers.handlers.order_handler.process_order`
- `"process_refund"` → `workers.handlers.refund_handler.process_refund`
- `"inventory.sync"` → `workers.handlers.inventory_handler.sync_inventory`

**9. `web/src/orders/processOrderOld.ts` `processOrder` — dead.** Pincher's inbound trace shows zero callers, and the only live registration of the checkout action (`web/src/orders/actions.ts:3,6-8`) imports `processOrder` from `./processOrder` — i.e. the live path uses **`web/src/orders/processOrder.ts:7`** (optimistic cart state + clear/fail marking), not the old fire-and-forget version. (The old code may linger in the minified `web/dist/` bundle, but that's a generated artifact.)

**10. Direct non-test callers of `billing.ProcessOrder`:**
- `orders.ProcessOrder` — `internal/orders/process.go:25` (capture step, line 31)
- `main.reconcileOnce` — `cmd/reconciled/main.go:23` (retries failed invoices)
