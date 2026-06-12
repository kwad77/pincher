# v1.7.0 release-prep notes (dev channel)

Date: 2026-06-11
Branch: `release/v1.7.0` (release PR targets `master`)
Release candidate: `origin/master` @ `b3054bc`
Baseline tag for all diffs: `v1.6.0`

This is the release-prep record required by RELEASING.md ("Release-prep
checklist"), same procedure as the v1.4.0–v1.6.0 preps. Executed in a
fresh worktree of `origin/master` at `b3054bc` (`/tmp/pincher-rel17`),
no other worktree touched. Adversarial review run as a Claude-driven
review (stand-in for `/codex:adversarial-review`, unavailable in this
environment), same triage rubric.

## 0. Scope fact-check (what is actually in this release)

The window `v1.6.0..b3054bc` contains exactly six merges: #2013 (Glob
advisory toolset resolution, issue #2011), #2016 (advise-index hook,
issue #2014), #2018 (5.0× fresh indexing + planner-stats guarantee +
deterministic binding edges, issue #2012), #2019 (router detection
ladder + `router` capability), #2021 (conditional `models`/`route`
proxy tools, issue #2020), #2023 (`init --router` + dispatch verse in
the packaged pincher-loop skill). The seven stubs present in
`CHANGELOG.d/` at the RC head (2010, 2011, 2014, 2018, 2019, 2021,
2023) are exactly the v1.7.0 content (2010 is the v1.6.0 release PR's
own stub, riding this release by design) and were assembled into the
1.7.0 entry.

## 1. Pre-flight + frozen-surface review (ADR-0002)

Fresh worktree at `b3054bc`:

- `go vet ./...` clean.
- `go test ./... -count=1` green (all packages).
- `make corpus-test` green ("All corpus snapshots match").
- `go test -race -timeout 1800s ./internal/server` green.
- `go test -race -timeout 1800s ./internal/db ./internal/index` green.

Contract gates run explicitly and green at the RC head:
`TestToolContract_GoldenFile`, `TestToolContract_DefaultSurface`,
`TestToolContract_DefaultSurface_RouterPresent`,
`TestMCPSurface_AllRegisteredToolsAgentCallable`,
`TestOpenAPI_ParityWithRegisteredHandlers`,
`TestHTTPRoutes_AllNonToolEndpointsDocumented`, `TestMakeSymbolID`,
`TestSchemaWeight_CoreLean_UnderBudget`,
`TestSchemaWeight_CoreLean_RouterPresent_UnderBudget`,
`TestRouterDetect_EnvOff_ForcesAbsentWithoutProbing`,
`TestRouterTools_EnvOff_ZeroRoutingActivity`,
`TestRouterDetect_NoConfigNoBinary_NotDetectedAndNoProbe`. (The
reference-MD CLI parity gate ran green inside the full-suite leg.)

Live tools/list comparison, v1.6.0 installed binary vs RC binary
(stdio MCP handshake, programmatic diff, four arms):

| Arm | v1.6.0 binary | RC binary | Verdict |
|---|---|---|---|
| Router ABSENT, default (core/lean) | 10 tools | 10 tools | **byte-identical** (sorted full-entry JSON diff empty) |
| Router ABSENT, full/rich | 34 tools | 34 tools | **byte-identical** |
| Router PRESENT (fixture), default | n/a | 12 tools (+`models`,`route`) | added: exactly `models`,`route`; existing 10 entries unchanged |
| Router PRESENT (fixture), full/rich | n/a | 36 tools | added: exactly `models`,`route`; **zero changes** to the 34 existing entries |

Router-absent was exercised genuinely (scratch `$HOME` with no router
config, `$PATH` without the router binary, `PINCHER_ROUTER` unset —
the default `auto` ladder answering "absent"), not via the kill
switch.

- **Schema:** v41 → v41 (verified via `doctor` on both binaries);
  `schemaMigrations` untouched in the window — zero migrations.
- **Schema weight (golden `internal/server/testdata/schema-weight.md`):**
  absent core+lean 3,094 (= v1.6.0); detected core+lean 3,515, under
  the unchanged 4,000 budget gate; full/rich 18,564 → 19,509 only when
  detected (36 tools).
- **`_meta` envelope:** additive only — `router` capability tag,
  conditional on detection.
- **HTTP:** `/v1/models` + `/v1/route` added (always registered);
  no route removed. OpenAPI parity gate green.
- **CLI:** additive only — `init --router`.
- **Symbol-ID format:** untouched; `TestMakeSymbolID` green.
- **Behavior changes:** none non-additive. The detection ladder runs
  once at startup (≤50ms, absent on any failure); `PINCHER_ROUTER=off`
  forces zero routing surface and zero dials.

**Verdict: every frozen-surface touch is additive or
detection-conditional with an absent default. MINOR bump
(v1.6.0 → v1.7.0) is the correct class.**

## 2. Adversarial review (stand-in for /codex:adversarial-review)

Scope: `git diff v1.6.0..b3054bc`, focus areas set by the release
brief: (a) #2018's referrer-skip vs edge-set invariance, (b) #2021's
proxy error paths, (c) #2023's CLAUDE.md managed-block editing,
(d) the advise-index × Glob-advisory interplay under v1.7.0 defaults.

### Sev-1 — none found

- **(a) Referrer-skip / edge invariance** (`internal/index/indexer.go`,
  `internal/db/db.go`): re-verified live on the pincher corpus (1,088
  files). v1.6.0 binary and RC binary each fresh-indexed the same
  tree: identical counts (10,059 symbols / 18,799 edges), identical
  symbol-ID digests, and RC's edge set is **bit-identical across
  repeated runs** (the new determinism). The only cross-version edge
  delta is 81 edges, all `binding_pass` confidence-0.4 ambiguous
  bare-name binds — the class #2018 deliberately made deterministic
  (e.g. `emitEdgesCmd`'s bare `Confidence` now binds to
  `bashExtractor.Confidence`, the lexicographically smallest
  candidate, instead of whichever extractor flushed first). No
  non-binding edge differs. The skip conditions are provably-empty
  cases only (full-reindex-cleared; zero prior symbol rows probed via
  `CountSymbolsInFile`, with fall-through to the full query on probe
  error), and `EnsurePlannerStats` runs at `Index()` tail —
  deliberately not mid-extraction, to keep fresh-index plans stable.
- **(b) Proxy error paths** (`router_tools.go`): exercised live
  against fixture routers — connection-refused, 1s-slow (vs the 250ms
  budget; call returned in ~298ms wall), non-JSON body, HTTP 500, and
  a contract-v1 router. Every arm returned the structured error
  envelope with the proceed-at-originating-model instruction (or, for
  contract-v1: the upgrade hint on `models`, and `mode=execute` +
  warning on an untagged plan). No hang, no handler error, no
  surface change mid-session.
- **(c) Managed-block editing** (`init.go` / `init_router.go`):
  round-tripped live on a CLAUDE.md with user content above and below
  the block (including non-ASCII). First `init --router` appended the
  block + routing subsection, preserving all user lines; second run
  byte-identical (idempotent); a hand-staled block was refreshed
  in-place with user content (before AND after) intact; deleting the
  end marker and re-running recovered to exactly one policy body +
  one marker pair with all user lines intact. The routing subsection
  only renders when rungs 1–2 detect an installation, and `--router`
  errors loudly (writing nothing) when nothing is detected.
- **(d) Hook interplay under v1.7.0 defaults** (`hook_check.go`,
  `hook_check_advise_index.go`): exercised live. Reads 1–2 of code
  files in an unindexed repo pass silently; Read 3 fires the one-time
  `advise_index` advisory recommending the **CLI** (`pincher index
  <root>`) — callable regardless of toolset — and Read 4 is
  suppressed (once per root per session). Glob on an indexed project
  under the core default hints `search` and names the `batch`/full
  escape hatches (the #2013 fix, closing v1.6.0's sev-2);
  `PINCHER_TOOLSET=full` restores the `onboard_module`
  recommendation. The two advisories live on disjoint branches (Read
  vs Glob) and cannot double-fire on one event; the toolset-divergence
  case is asymmetric-safe (the core fallback is callable in both
  modes).

### Sev-2 — none found

### Sev-3 — noted

1. The `advise_index` decision row carries `SuggestedTool: "index"`
   (telemetry field). The systemMessage correctly recommends the CLI,
   but a consumer reading only the telemetry column could interpret
   the suggestion as the MCP `index` tool, which a default core
   session cannot `tools/call`. Cosmetic — nothing in-tree consumes
   the field that way; worth a comment if telemetry dashboards ever
   join on it.
2. `models` mutation actions (`enable`/`disable`/`test`) are declared
   in the input schema but permanently error under contract v2 —
   intentional shape-stability, but the lean description does not
   mention it (the rich one does). An agent on the lean surface
   discovers it only on call. Loud + structured, so not a defect.

### What held up under attack

The identity-validated detection ladder (both real-world false
positives — pincher-on-7878, redirecting dashboard — rejected, and
verified live against the actual pincher instance answering on this
host's :7878); canonical-value-only env parsing with fail-direction
ABSENT; off-mode zero-dial guarantee (verified live with a live
fixture router installed and configured: zero requests logged);
the dual-state goldens pinning both advertisement states; the
never-block 250ms budget; the managed-block marker recovery; the
advise-index bloat-trap guard.

## 3. Live smoke (RC binary, real corpus)

RC binary built from the worktree (`go build ./cmd/pinch`). Scratch
data dir; **re-indexed first** (banked lesson): `/home/kwad77/pincher`
→ 1,088 files, 10,059 symbols, 18,799 edges, 0 skipped (RC fresh
index 6.2s vs v1.6.0 binary 11.3s on the same tree — the #2018 effect
at small scale).

| Probe | Result |
|---|---|
| `search` | PASS — `MakeSymbolID` top hit, `_meta.watermark` present |
| `symbols` (batch read, `max_tokens`) | PASS |
| `context` | PASS — source + imports |
| `trace` | PASS — inbound MakeSymbolID, exactly the 4 ground-truth depth-1 callers |
| `changes` | PASS — dirty working tree mapped to symbols |
| `batch` | PASS — read-only sub-queries dispatch in default mode |
| `loop` checkpoint → resume | PASS — checkpoint receipt; resume carries claim/decision |
| `verify_change` | PASS |
| `guide` | PASS |
| `_meta` watermark / budgets | PASS — watermark on every envelope |
| **NEW: router round-trip** | PASS — fixture contract-v2 router (httptest-style, scratch ports; the host's live :7879 router untouched): auto-ladder detection → `router` ∈ `_meta.capabilities` → `models` handshake `{contract_version: 2, weights_version, registry_version}` + registry render → `route` returns mode-tagged plan (`advise`) + `request_id` → `route action=outcome` posts to `/v1/outcomes`, accepted with the same `request_id` |

## 4. Real-MCP validation (claude -p, stdio, default surface, router absent)

`claude -p` sessions, `--mcp-config` pointing at the RC binary +
scratch data dir, `--strict-mcp-config`, on the pincher corpus. This
host's default router address (:7878) is occupied by a pincher HTTP
instance — the identity-validated probe correctly rejects it, so the
genuine `auto` ladder answers "absent" (no env override needed).

1. Depth-1 inbound callers of `MakeSymbolID` → **exactly** the ground
   truth set (`indexImpl`, `externalModuleSymbol`,
   `syntheticFileModuleSymbol`, `handleFetch`).
2. Definition lookups + `changes` dirty-set question → correct.
3. Tools visible in session → **exactly the 10 core tools; `models`
   and `route` NOT advertised** — zero-surface-when-absent holds in a
   real client.

## 5. Checklist walk (RELEASING.md "Release-prep checklist")

| # | Item | Status |
|---|---|---|
| 0 | Frozen-surface review | Done (§1) — additive only, dual-state surface verified live, all gates green |
| 1 | Adversarial review | Done (§2) — Claude stand-in; 0 sev-1, 0 sev-2, 2 sev-3 noted |
| 2 | CHANGELOG.md | Done — 7 stubs assembled via `changelog-assemble.sh --apply`, promoted to `[1.7.0] — 2026-06-11` with theme one-liner, link refs added, empty `[Unreleased]` recreated |
| 3 | "What's New" release body | Done — `docs/launch/v1.7.0-whats-new.md`; every cited number traced to PR bodies #2013–#2023 or re-verified live in this prep |
| 4 | README roadmap | Done — "Where Pincher is now" advanced to v1.7 (v1.6 compressed to history, four v1.7 bullets) |
| 5 | README known limitations | Reviewed — no limitation listed there is fixed by this window; no edit needed |
| 6 | README version-sensitive claims | Done — tool count 34 → 36 registered (10 advertised; 12 with router), schema-diet note updated |
| 7 | docs/reference/README.md metadata line | Verified current — v41 · 36 registered · conditional router pair; updated by #2021 itself |
| 8 | docs/ Pages audit | Done — drift grep vs v1.6.0; bumped install snippets (`docs/index.html`, `deployment/systemd.md`, `deployment/homebrew.md`), docker pin (`deployment/docker.md`), and 34-tool claims in `index.html` meta/hero, `reference/architecture.md`, `reference/http-api.md`, `integrations/loop-leverage-layers.md` |
| 9 | Bench baseline decision | **Refresh** — #2018 is a deliberate perf-affecting refactor whose numbers ARE the rationale (the RELEASING.md refresh case). `bench-baseline.yml` dispatched on master (run 27392598282); artifact folded into this PR if green in-window, else committed as an immediate follow-up |
| 10 | DOGFOOD changelog section | Done — 4 bullets (v1.6.0 sev-2 fixed in-window as #2013; #2014's telemetry origin; the #2012 dogfood-found perf cliff + the release-prep edge-invariance re-verification; the release-stub fold-forward note) |

## 6. Substitutions / deviations

- `/codex:adversarial-review` unavailable → Claude adversarial review
  over the same diff, same triage rubric (§2), as in v1.4.0–v1.6.0.
- The fixture router is a local HTTP server implementing the
  contract-v2 surface (healthz identity, `/v1/models` handshake,
  mode-tagged `/v1/route`, plural `/v1/outcomes`) on scratch ports —
  a real `pincher-router-serve` was live on this host (:7879) but
  deliberately left untouched per the release brief.
- This PR adds its own `CHANGELOG.d` stub under the established
  release-stub exemption (stub-check gate); that stub rides the v1.8
  changelog, as #2010's rode this one.
