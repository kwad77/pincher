# v1.8.0 release-prep notes (dev channel)

Date: 2026-06-11
Branch: `release/v1.8.0` (release PR targets `master`)
Release candidate: `origin/master` @ `057d0ad`
Baseline tag for all diffs: `v1.7.0`

This is the release-prep record required by RELEASING.md ("Release-prep
checklist"), same procedure as the v1.4.0–v1.7.0 preps. Executed in a
fresh worktree of `origin/master` at `057d0ad` (`/tmp/pincher-rel18`),
no other worktree touched. Adversarial review run as a Claude-driven
review (stand-in for `/codex:adversarial-review`, unavailable in this
environment), same triage rubric.

## 0. Scope fact-check (what is actually in this release)

The window `v1.7.0..057d0ad` contains exactly four merges: #2025
(`advise_route` hook advisory on Task spawns, router-loop §A2/B8),
#2026 (route outcome auto-echo, item B10 pincher half), #2027
(router-gated dashboard Models tab + `docs/reference/routing.md` +
the v1.8.0 what's-new draft, item B12), #2028 (guide/coach routing
integration, §A4/B11). The five stubs present in `CHANGELOG.d/` at
the RC head (2024, 2025, 2026, 2027, 2028) are exactly the v1.8.0
content (2024 is the v1.7.0 release PR's own stub, riding this
release by design) and were assembled into the 1.8.0 entry.

**GA-claim discipline:** the router-loop §C ship gate stood at
**n=2/30 routed Make-stage task units** at release time. Per the
no-unmeasured-claims rule, `docs/launch/v1.8.0-whats-new.md` (renamed
from the in-window draft) publishes with every `[GATE]` slot marked
**measurement in progress** — no GA number is asserted anywhere in the
release surface.

## 1. Pre-flight + frozen-surface review (ADR-0002)

Fresh worktree at `057d0ad`:

- `go vet ./...` clean; `go build ./...` clean.
- `go test ./... -count=1` green (all packages; zero non-`ok` lines).
- `make corpus-test` green ("All corpus snapshots match").
- `go test -race -timeout 1800s ./internal/server` green (844s).

Detection-safety family re-run explicitly and green at the RC head:
`TestRouterDetect_EnvOff_ForcesAbsentWithoutProbing`,
`TestRouterTools_EnvOff_ZeroRoutingActivity`,
`TestRouterDetect_NoConfigNoBinary_NotDetectedAndNoProbe`. (The
contract/golden/schema-weight/OpenAPI-parity gates ran green inside
the full-suite leg.)

Live tools/list comparison, v1.7.0 installed binary vs RC binary
(stdio MCP handshake, programmatic sorted-full-entry JSON diff, four
arms; absent arms exercised genuinely — scratch `$HOME`, `$PATH`
without the router binary, `PINCHER_ROUTER` unset; present arms via
the canonical `PINCHER_ROUTER=on` override, no network):

| Arm | v1.7.0 binary | RC binary | Verdict |
|---|---|---|---|
| Router ABSENT, default (core/lean) | 10 tools | 10 tools | **byte-identical** |
| Router ABSENT, full/rich | 34 tools | 34 tools | **byte-identical** |
| Router PRESENT, default (core/lean) | 12 tools | 12 tools | **byte-identical** (the #2026 description delta is rich-only) |
| Router PRESENT, full/rich | 36 tools | 36 tools | differs ONLY in `route`: one sentence appended to the rich description + one clause on the `outcome` arg description — the documented #2026 additive delta; all 35 other entries byte-identical |

- **Schema:** v41 → v41; zero migrations in the window (new telemetry
  readers are pure SELECTs over existing `hook_invocations` rows; the
  #2028 consult/outcome split is in-memory counters by design).
- **Schema weight (golden `internal/server/testdata/schema-weight.md`):**
  absent core+lean 3,094 (= v1.7.0); detected core+lean 3,515
  (= v1.7.0), under the unchanged 4,000 budget gate
  (`TestSchemaWeight_CoreLean_RouterPresent_UnderBudget` green —
  the #2026 lean surface is byte-identical, asserted by the diff
  above). Full/rich detected 19,509 → 19,606 (the rich-description
  sentence).
- **`_meta` envelope:** untouched.
- **HTTP:** no route added or removed. `coach`'s OpenAPI output schema
  gains the optional `routing` property (additive, HTTP-only).
- **CLI:** no subcommand or flag change. `init --target=claude` now
  migrates an untweaked managed PreToolUse matcher
  (`Read|Grep` / `Read|Grep|Glob` → `Read|Grep|Glob|Task`) in place —
  ownership requires the exact prior managed value AND our command;
  tweaked matchers are left alone (matrix-tested).
- **Symbol-ID format:** untouched.
- **Behavior changes:** all additive or detection-conditional with an
  absent default. The dashboard HTML is byte-identical to v1.7.0 when
  no router is detected (absent-state snapshot fixtures deliberately
  NOT regenerated — their byte-identity is the assertion);
  guide/coach absent-state responses byte-identical (dual-state
  pinned).

**Verdict: every frozen-surface touch is additive or
detection-conditional with an absent default. MINOR bump
(v1.7.0 → v1.8.0) is the correct class.**

## 2. Adversarial review (stand-in for /codex:adversarial-review)

Scope: `git diff v1.7.0..057d0ad`, focus areas set by the release
brief: (a) the #2026 auto-echo cache, (b) the #2025 advise_route
trigger/suppression, (c) the #2027 Models tab proxy, (d) the #2028
guide/coach dual-state byte-identity.

### Sev-1 — none found

- **(a) Auto-echo cache** (`router_tools.go`): the LRU is a true LRU
  (`get` refreshes recency via an aliasing-safe `order[:i:i]` splice;
  eviction at >64 drops the front), mutex-guarded, zero-value-ready.
  `routeEchoFromCall` caches only present, non-empty values — the
  proxy can never fill a key it didn't see; a pre-v2 route response
  without `request_id` caches nothing (no fabricated join key).
  `autofillOutcomeEcho` fills only ABSENT card keys (explicit fields
  win by construction) and returns nil on any miss — fresh session /
  evicted / foreign `request_id` all leave the card untouched, so a
  router 422 surfaces honestly. `echo_autofilled` is attached only to
  a successful router response. Validated live against the real
  router (§3).
- **(b) advise_route trigger/suppression**
  (`hook_check_advise_route.go`, `router_detect.go`): every branch
  returns `Continue: true`; no-session-id and router-absent paths
  never advise; suppression keys on the prior `advise_route` row for
  the session (`file_path` carries the session key); threshold
  accounting (+1 for the unlogged current event) mirrors
  advise_index. Detection at hook time is rungs 1–2 only — the
  refactor shares one ladder core (`routerInstalledRungs12`) with
  `detectRouter`, and the env contract (off + every typo ⇒ absent)
  is preserved in both consumers. No network on any hook path.
- **(c) Models tab proxy** (`dashboard.go`, `server.go`): the tab
  button/pane substitute to empty strings when `!s.routerDetected`
  (tokens flush in the template — zero residue, snapshot-pinned);
  `loadModels` is the only `/v1/models` fetch site, dispatched solely
  from the tab, and `showTab` falls back to Overview for a missing
  pane — a stale `#models` hash on a router-less server is fetch-free.
  Every rendered value passes `esc()`; zero mutation controls render;
  cost renders the raw spec or "free" on null (no price guesses).
- **(d) guide/coach dual-state** (`server.go`, `coach.go`): the
  `route` recommendation is appended exactly when `s.routerDetected`
  and the shape is Make-shaped (`shapeAdd`/`shapeRefactor`), after
  adherence ranking (deterministic last position); the core-toolset
  escape-hatch note skips `routerConditionalTools` (it would be false
  in both router states — the #2013 discipline). Coach's `routing`
  section and verse-skip finding exist only under `s.routerDetected`;
  absent responses byte-identical (dual-state + leak-string tests).
  The verse-skip finding prices at est 0 and the 7d numerator is a
  documented upper bound — the conservative direction.

### Sev-2 — none found

### Sev-3 — noted

1. Coach's session-window verse-skip compares hook-recorded Task
   spawns (keyed by the HOST session id) against this PROCESS's live
   consult counters. A long-lived HTTP daemon serving several host
   sessions can drift the two scopes. The basis strings name both
   sources, so the number is auditable — cosmetic, but worth a
   thought if coach's session window ever feeds a dashboard.
2. guide's appended `route` recommendation ships a literal
   `"session_id":"<session>"` placeholder in its args template; an
   agent that pastes it verbatim sends the placeholder string as the
   session id. The router accepts it (echo fields are opaque), so
   nothing breaks — but a typed example would join better.
3. The advise_route advisory's `SuggestedTool: "route"` is accurate
   in both toolset modes (router-conditional tools ride with core),
   closing the analogous v1.7.0 sev-3 note about `advise_index` — no
   action needed, recorded for symmetry.

### What held up under attack

The bounded-LRU + only-seen-values + explicit-wins + miss-passthrough
auto-echo discipline (validated end-to-end live, §3); the one-ladder
refactor keeping the hook seam dial-free (hit-counter pinned); the
zero-surface-when-absent extension to dashboard HTML and guide/coach
response text (byte-identity pinned in both directions); the
attempt-time counter semantics (miss-path consults still count as
verse adherence); the matcher-migration ownership rule (exact prior
managed value AND our command).

## 3. Live smoke (RC binary, real corpus, REAL router)

RC binary built from the worktree. Scratch data dir; **re-indexed
first** (banked lesson): `/home/kwad77/pincher` → 1,088 files, 10,059
symbols, 18,799 edges, 0 skipped, 6.4s fresh.

Router arm: the RC server ran with `PINCHER_ROUTER_ADDR=127.0.0.1:7879`
pointed at the **real live pincher-router** on this host (v1.3.0,
reconciled registry) — read-only consults + one outcome row; the
router service itself untouched.

| Probe | Result |
|---|---|
| `search` | PASS — `MakeSymbolID` top hit, `_meta.watermark` present |
| `symbols` (batch read, `max_tokens`) | PASS |
| `context` | PASS — symbol + imports + callees |
| `trace` | PASS — inbound MakeSymbolID, exactly the 4 ground-truth depth-1 callers |
| `changes` | PASS — dirty working tree mapped to symbols |
| `batch` | PASS — read-only sub-queries dispatch in default mode |
| `loop` start → checkpoint → resume | PASS — receipt + seq; resume carries claim/decision |
| `verify_change` | PASS |
| `guide` | PASS — Make-shaped task appends `route` as the LAST recommendation (live router detected) |
| `coach` routing section | PASS — `route_consults: 1`, `outcome_reports: 1`, basis strings name the approximations |
| dashboard | PASS — Models tab rendered (router present) |
| **Detection → capability** | PASS — `router` ∈ `_meta.capabilities` against the real :7879 |
| **`models` list (real registry)** | PASS — contract v2 handshake (`weights v2-trained-20260527-125605`, registry v2, caps advise/execute/discovery); 9 workers: `qwen3.6-27b` + `qwen3.6-35b-a3b` (local, enabled), `host-subagent` (enabled), six paid api workers (3 anthropic + 3 gemini) all disabled — the governance line holds |
| **NEW: minimal-card outcome auto-echo, live** | PASS — **first end-to-end validation of the verse's minimal-card contract against a real router.** `route action=route` (envelope: intent + tool_name=Task + tier=lite + role=maker + session_id + tokens_used) → mode=execute plan, `request_id 580d8f45f70f48028d6d4866d4976122`, model qwen3.6-27b, runtime_model qwen3.6-35b-a3b, lane fast-path → `route action=outcome` with ONLY `{request_id, outcome_class: clean, gate}` → proxy auto-filled `echo_autofilled: [complexity_tier, lane, role, routed_model, session_id, tokens_used, tool_name]` → router answered **`ok: true`** (the exact card shape that 422'd five times pre-#2026) |

## 4. Real-MCP validation (claude -p, stdio, default surface, router absent)

`claude -p` sessions, `--mcp-config` pointing at the RC binary +
the scratch data dir, `--strict-mcp-config`, on the pincher corpus.
No router env set: rung 1 passes on this host (router config exists),
rung 3 dials the DEFAULT :7878 — occupied by a pincher HTTP instance,
correctly rejected by the identity probe — so the genuine `auto`
ladder answers "absent".

1. Depth-1 inbound callers of `MakeSymbolID` → **exactly** the ground
   truth set (`indexImpl`, `externalModuleSymbol`,
   `syntheticFileModuleSymbol`, `handleFetch`).
2. Definition probes → correct AND honest: a symbol that exists only
   post-#2026 (`routeEchoCache`) was reported absent from the indexed
   dogfood tree (true — that tree is at the v1.7.0 merge), not
   hallucinated; `MakeSymbolID` correctly identified as a leaf (zero
   indexed outbound callees — it is pure string concatenation).
3. Tools visible in session → **exactly the 10 core tools; `models`
   and `route` NOT advertised** — zero-surface-when-absent holds in a
   real client.

## 5. Checklist walk (RELEASING.md "Release-prep checklist")

| # | Item | Status |
|---|---|---|
| 0 | Frozen-surface review | Done (§1) — additive only, four-arm live diff vs installed v1.7.0, all gates green |
| 1 | Adversarial review | Done (§2) — Claude stand-in; 0 sev-1, 0 sev-2, 3 sev-3 noted |
| 2 | CHANGELOG.md | Done — 5 stubs assembled via `changelog-assemble.sh --apply`, promoted to `[1.8.0] — 2026-06-11` with theme one-liner, link refs added, empty `[Unreleased]` recreated |
| 3 | "What's New" release body | Done — `docs/launch/v1.8.0-whats-new.md` (renamed from the in-window draft); `[GATE]` ship-claim slots kept as **measurement in progress** (§C gate at n=2/30 — no GA claim publishes unmeasured); #2028 section added; every cited number traced to PR bodies #2025–#2028 or re-verified live in this prep |
| 4 | README roadmap | Done — "Where Pincher is now" advanced to v1.8 (v1.7 compressed to history, five v1.8 bullets incl. the GA-status-honestly-held note) |
| 5 | README known limitations | Reviewed — no limitation listed there is fixed by this window; no edit needed |
| 6 | README version-sensitive claims | Reviewed — tool counts (36 registered / 10 advertised / 12 with router) and schema-diet numbers unchanged by this window; no edit needed |
| 7 | docs/reference/README.md metadata line | Verified current — v41 · 36 registered · conditional router pair; unchanged by this window |
| 8 | docs/ Pages audit | Done — drift grep vs v1.7.0; bumped install snippets (`docs/index.html`, `deployment/systemd.md`, `deployment/homebrew.md`) and the docker pin (`deployment/docker.md`); 36-tool claims already current |
| 9 | Bench baseline decision | **Skip** (the RELEASING.md default) — no merge in this window intentionally changes perf shape (#2025 hook decision path, #2026 proxy cache, #2027 dashboard render, #2028 response composition; no indexer/query work). Refreshing would only absorb noise |
| 10 | DOGFOOD changelog section | Done — 4 bullets (the measured five-422 auto-echo origin; the §A2 honest-deviation; the plan-metric vs telemetry-reality approximations; the release-stub fold-forward note) |

## 6. Substitutions / deviations

- `/codex:adversarial-review` unavailable → Claude adversarial review
  over the same diff, same triage rubric (§2), as in v1.4.0–v1.7.0.
- The frozen-surface present arms used the canonical
  `PINCHER_ROUTER=on` override (no network) rather than a fixture
  router — the live-router arm was exercised separately and fully in
  §3 against the real :7879 service, which v1.7.0's prep deliberately
  left untouched and this prep touched only with read-only consults
  plus one labeled outcome row (`session_id rel18-signoff-smoke`).
- This PR adds its own `CHANGELOG.d` stub under the established
  release-stub exemption (stub-check gate); that stub rides the v1.9
  changelog, as #2024's rode this one.
- Milestone handling per precedent: milestone `v1.8.0` created,
  #2025–#2028 and the release PR assigned, closed after tagging.
