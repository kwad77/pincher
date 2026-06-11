Answer the following ten questions about THIS repository (the orderflow polyglot
codebase you are currently in: a Go API service, Python fulfillment workers under
workers/, and a TypeScript storefront under web/). For each answer cite file paths
(line numbers where useful) as evidence. Be concise — a short list or paragraph per
question. Do not modify any files.

Beware: this repo contains generated artifacts (gen/, fixtures/, web/dist/) and
several different functions that share a name. Be precise about WHICH function or
value you mean.

1. When a client sends `POST /api/v1/orders`, which function ends up executing the
   order business logic (validate / capture payment / persist / enqueue)? Name every
   hop from the HTTP route to that function, including how the indirection in the
   middle is resolved.

2. Several functions are named `ProcessOrder` / `process_order` / `processOrder`
   across the codebase. Exactly one of them performs payment capture (charging the
   card). Which one, in which file?

3. Is `ProcessOrderV1` (internal/orders/process_v1.go) called or registered anywhere
   in non-test code? Answer dead or alive, with the evidence you checked.

4. The Python module workers/handlers/legacy.py defines a `process_order` function.
   Can the worker ever execute it for a queued job? Explain exactly why or why not,
   citing the registration mechanism.

5. Blast radius: if the signature of `store.SaveOrder` (internal/store/store.go)
   changed, list every non-test call site (function + file) that would need updating.

6. A user clicks the checkout button in the web UI. Which Python function ultimately
   fulfills the order? Trace the full cross-language path (TS → Go → queue → Python),
   naming each function and the string keys that connect the layers.

7. What is the effective default value of the `order_retry_limit` configuration knob,
   in which file is that default defined, and which Go function consumes the knob?
   (Careful: several files contain plausible-looking values for it.)

8. Which job types does the Python worker actually register at startup, and which
   handler function (module + name) does each job type map to?

9. Is the `processOrder` function in web/src/orders/processOrderOld.ts reachable from
   any live frontend code? Answer dead or alive, and name what the live checkout path
   uses instead.

10. List the direct (depth-1) non-test callers of `billing.ProcessOrder`
    (internal/billing/charge.go) — the function names and their files.
