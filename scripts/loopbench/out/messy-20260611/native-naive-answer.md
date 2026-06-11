All ten answers are below, from direct inspection of the source (pincher MCP tools weren't available this session, so I used file reads/grep per the fallback policy).

---

**1. `POST /api/v1/orders` → business logic path**
The route is bound to `(*Server).handleCreateOrder` in `internal/api/server.go:15`. That handler doesn't call domain code; it calls `dispatch.Dispatch(ctx, "order.process", body)` (`internal/api/handlers.go:20`). The indirection is a string-keyed action registry (`internal/dispatch/registry.go`): `dispatch.Register("order.process", processAction)` runs in the `init()` of `internal/orders/register.go:13` (pulled in by the blank import in `cmd/orderd/main.go:9`). `processAction` (`register.go:17`) unmarshals the JSON and calls **`orders.ProcessOrder`** (`internal/orders/process.go:25`), which does validate → capture → persist → enqueue. Full chain: `handleCreateOrder` → `dispatch.Dispatch("order.process")` → `processAction` → `orders.ProcessOrder`.

**2. Which `ProcessOrder` captures payment**
**`billing.ProcessOrder` in `internal/billing/charge.go:15`** — it only charges the card, calling `chargeCard` in a retry loop. (`orders.ProcessOrder` is the full pipeline that *calls* it; the TS and Python namesakes do browser state and fulfillment respectively. The dead `legacy.py` `process_order` also charged cards historically, but it's unreachable — see Q4.)

**3. `ProcessOrderV1` — dead**
Grep across all `.go`/`.py`/`.ts` files finds `ProcessOrderV1` only in its own file (`internal/orders/process_v1.go`). It is not registered in `internal/orders/register.go` (only `processAction`/`refundAction` are), and no other code calls it. Additionally, its queue topic `"process_order_v1"` has no registered Python handler, so even its output path is dead. The file's own comment (`process_v1.go:10-12`) confirms it's kept only for rollback. **Dead.**

**4. `workers/handlers/legacy.py:process_order` — can never run**
No. The worker resolves jobs through `lib/registry.HANDLERS`, populated only by `@register(...)` decorator side effects when modules are imported (`workers/worker_main.py:36-42`, `workers/lib/registry.py`). `legacy.py` has no `@register` decorator on `process_order` (`legacy.py:10`), and `workers/handlers/__init__.py` deliberately does not import it (`__init__.py:3-10` imports only `inventory_handler`, `order_handler`, `refund_handler`). So it's never registered and `route()` can never find it. Also, `"process_order"` is already claimed by `order_handler.process_order`.

**5. Blast radius of changing `store.SaveOrder`**
Four non-test call sites:
- `orders.ProcessOrder` — `internal/orders/process.go:34`
- `orders.ProcessOrderV1` — `internal/orders/process_v1.go:18` (dead, but still compiles against the signature)
- `orders.RefundOrder` — `internal/orders/refund.go:28`
- `api.handleAdminImport` — `internal/api/admin.go:20` (the bulk-import "backdoor" that bypasses dispatch)

**6. Checkout click → Python fulfillment**
`wireCheckout` (`web/src/ui/checkout.ts:7`) dispatches the string **`"order/submit"`** through the TS action registry (`web/src/registry.ts`); `web/src/orders/actions.ts:6` registered that name to the TS `processOrder` (`web/src/orders/processOrder.ts:7`), which calls `createOrder` (`web/src/api/client.ts:10`) → `POST /api/v1/orders`. On the Go side: `handleCreateOrder` → `dispatch.Dispatch("order.process")` → `processAction` → `orders.ProcessOrder`, which publishes a job with **`job_type: "process_order"`** (`internal/orders/process.go:37`, `internal/queue/queue.go:14`). The Python worker (`workers/worker_main.py:route`) looks up `"process_order"` in `HANDLERS`, which maps to **`workers/handlers/order_handler.py:process_order`** (registered at `order_handler.py:7`) — the function that actually fulfills the order.

**7. `order_retry_limit` default**
Effective default is **7**, defined in `gen/defaults.gen.json:136` (`"order_retry_limit": 7`, machine-generated). It's consumed by `config.OrderRetryLimit()` (`internal/config/config.go:29`), which is called by `billing.ProcessOrder` (`internal/billing/charge.go:16`). Decoys: the `3` in `config.go:39` is only a last-ditch fallback if the generated file is missing; `workers/lib/config.py:33` has a Python mirror with the same fallback `3`; and `fixtures/orders_fixture_3.json` has historical `retry_limit_at_capture` values of 3/5 that are just test data.

**8. Job types the Python worker registers**
Via `import handlers` in `worker_main.py:13` → `handlers/__init__.py`:
- `"process_order"` → `handlers.order_handler.process_order` (`order_handler.py:7-8`)
- `"inventory.sync"` → `handlers.inventory_handler.sync_inventory` (`inventory_handler.py:7-8`)
- `"process_refund"` → `handlers.refund_handler.process_refund` (`refund_handler.py:7-8`)

`legacy.py` registers nothing.

**9. `web/src/orders/processOrderOld.ts:processOrder` — dead**
No `.ts` file imports `processOrderOld` (grep finds zero references), and the production bundle `web/dist/bundle.min.js` contains zero occurrences of it. The live checkout path uses `processOrder` from `web/src/orders/processOrder.ts`, wired through `registerAction("order/submit", ...)` in `web/src/orders/actions.ts:6`. **Dead.**

**10. Direct non-test callers of `billing.ProcessOrder`**
- `orders.ProcessOrder` — `internal/orders/process.go:30`
- `reconcileOnce` — `cmd/reconciled/main.go:26` (the scheduled failed-capture reconciler)
