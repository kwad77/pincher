# messy-scale-8q — verified answer key

Ground truth for `tasks/messy-scale-8q.md`, verified against the corpus built by
`fixtures/build-messy-corpus.sh --scale 40` (deterministic; 542 tracked files, 486
live source files, ~13.0k live LOC) using manual reading plus pincher v1.5.0
`search`/`trace` on the indexed corpus (527 files indexed, 4,945 symbols, 2,384
edges, 13 oversized generated files blocked by design). Graders: an arm's answer is
RIGHT only if it names the bolded facts; naming the wrong shard's function (off-by-one
on the pkgNN suffix) is WRONG — the cross-shard wiring is the whole point.

Shard wiring (for the grader's orientation): pkgK's `ProcessOrderPkgK` captures
payment via pkg(K+1)'s `CapturePaymentPkg(K+1)` and writes its audit row via
pkg(K+1)'s `RecordAuditPkg(K+1)`; pkg40 terminates on base `billing.ProcessOrder`.
Consequently every shard's own `CapturePaymentPkgK`/`RecordAuditPkgK` serves the
UPSTREAM shard (K-1), and pkg01's are uncalled. Knob ground truth:
`pkgNN_retry_limit = 3 + (K*7 % 9)` in gen/defaults.gen.json only.

This file lives beside the task set in the *pincher* repo and is never visible to
benchmark arms (they run with cwd = the built corpus, e.g. /tmp/messy-scale-repo).

## Q1 — "pkg07/submit" → which Python function fulfills

**`fulfill_pkg07` in `workers/handlers/pkg07_handler.py`.** Path:

1. action registered in `web/src/pkg07/actions.ts` (`registerAction("pkg07/submit", …)`) → **`processPkg07Order`** (`web/src/pkg07/process.ts`)
2. → `postShardOrder("pkg07", …)` (`web/src/api/scaledClient.ts`) → `POST /api/v1/pkg/pkg07/orders`
3. → Go `handleScaledCreate` (`internal/api/scaled_routes.go`) → `dispatch.Dispatch(ctx, "pkg07.process", body)` — the action name is built from the URL path value
4. → binding created in `internal/pkg07/register.go` `init()` → `processActionPkg07` → **`ProcessOrderPkg07`** (`internal/pkg07/pipeline.go`)
5. → `queue.Publish("pkg07.fulfill", …)` (`internal/queue/queue.go`)
6. → Python `worker_main.route` looks up `HANDLERS["pkg07.fulfill"]` → **`workers/handlers/pkg07_handler.py::fulfill_pkg07`**.

Credit requires the right Python function + the connecting string keys
("pkg07/submit" or actions.ts; "pkg07.process" or the register.go init; job type
"pkg07.fulfill"). Naming `fulfill_pkg07` from pkg14_legacy-style reasoning (the
pkg07_legacy twin) or any other shard's handler is WRONG.

## Q2 — which function charges the card for a pkg21 order

**`CapturePaymentPkg22` in `internal/pkg22/charge.go`.** The pkg21 pipeline
(`ProcessOrderPkg21`, internal/pkg21/pipeline.go) captures through the NEXT shard's
gateway. Wrong answers: `CapturePaymentPkg21` (that one serves pkg20's orders),
`billing.ProcessOrder` (only the pkg40 terminal shard uses it), any
`CapturePaymentPkg22Request` type from gen/scaledpb (generated chaff), the Python/TS
twins. (Pincher: `trace CapturePaymentPkg22 inbound` returns exactly
ProcessOrderPkg21.)

## Q3 — is ProcessOrderPkg33V1 dead?

**Dead.** No callers, no `dispatch.Register` of it, not referenced from any cmd/.
(Pincher: 0 inbound edges.) The only textual occurrences are its own definition in
`internal/pkg33/dead_v1.go`; `"pkg33.v1"` inside it is a queue job-type string (which
no worker registers), not a caller. The chaff in gen/scaledpb/pkg33.pb.go matches the
`ProcessOrderPkg33*` prefix but is generated types, not calls.

## Q4 — can workers/handlers/pkg14_legacy.py fulfill_pkg14 run?

**No — it is never registered.** Handlers enter `lib.registry.HANDLERS` only via the
`@register("job_type")` decorator at import time; `workers/handlers/__init__.py`
imports the live `pkg14_handler` (and the other `*_handler` modules) and explicitly
NOT the `*_legacy` modules. `pkg14_legacy.fulfill_pkg14` has no decorator and its
module is never imported, so `route()` in `worker_main.py` can never resolve to it.
The job type `"pkg14.fulfill"` maps to the SAME-NAMED live `fulfill_pkg14` in
`pkg14_handler.py`.

## Q5 — blast radius of SaveRecordPkg12 signature change

Exactly **three** non-test call sites, all in internal/pkg12/:

1. `ProcessOrderPkg12` — `internal/pkg12/pipeline.go`
2. `RefundOrderPkg12` — `internal/pkg12/refund.go`
3. `ProcessOrderPkg12V1` — `internal/pkg12/dead_v1.go` (dead but still compiles against the signature)

Missing dead_v1.go = wrong. Listing `RecordAuditPkg12` callers (that's a different
function) or pkg11/pkg13 sites = wrong.

## Q6 — direct callers of RecordAuditPkg30

Exactly **one**: **`ProcessOrderPkg29` in `internal/pkg29/pipeline.go`** — the
cross-shard audit mirror (pkgK audits through pkg(K+1)). Pincher trace inbound
depth-1 returns exactly this. Naming anything in pkg30 itself, the
`AuditPkg30Request/Response` chaff types in gen/scaledpb/pkg30.pb.go, or fixture
strings = wrong.

## Q7 — pkg26_retry_limit default

**5 (= 3 + 26·7 mod 9), defined in `gen/defaults.gen.json`.** Consumed by
**`CapturePaymentPkg26` (internal/pkg26/charge.go)** via
`config.ScaledKnob("pkg26_retry_limit", 2)` (internal/config/scaled.go), and that
consumer is called by **`ProcessOrderPkg25` (internal/pkg25/pipeline.go)** — the
cross-shard hop again.

Decoys an arm must NOT report as the default: the `2` fallback argument in
charge.go/ScaledKnob (applies only if the generated file is missing), the
`retry_limit_at_capture` values 2/4 in fixtures/scaled_fixture_*.json (historical
snapshots), and the bundle's `pkg26_retry_limit:5` (consistent but generated output,
not the defining location). Accept "5 from gen/defaults.gen.json (env
PKG26_RETRY_LIMIT overrides; falls back to 2 only if the generated file is
missing)". Wrong consumer (e.g. CapturePaymentPkg27 or ProcessOrderPkg26) = wrong.

## Q8 — registered job types + pkg09 fulfiller

**43 job types** (3 base: `process_order`, `process_refund`, `inventory.sync` + 40
shard `pkgNN.fulfill` types — the `*_legacy` modules are never imported, and the Go
side's `pkgNN.refund`/`pkgNN.v1` strings have no registered handler). pkg09-vertical
orders are fulfilled by **`fulfill_pkg09` in `workers/handlers/pkg09_handler.py`**
(job type `"pkg09.fulfill"`). Verified by importing the package and dumping
HANDLERS. Accept 43 or "3 + N where N=40"; counting legacy modules or the Go-side
refund job types as registered = wrong.

## Scoring

1 point per question, right/wrong only (criteria above), 8 total. Score each arm's
`<arm>-answer.md` against this key by hand; record per-question verdicts in the
round's RESULTS file alongside the token scoreboard from score.sh.
