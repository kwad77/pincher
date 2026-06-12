# v1.9.0 release-prep notes (dev channel)

Date: 2026-06-12
Branch: `release/v1.9.0` (release PR targets `master`)
Release candidate: `origin/master` @ `a73069e`
Baseline tag for all diffs: `v1.8.0`

This is the release-prep record required by RELEASING.md ("Release-prep
checklist"), same procedure as the v1.4.0–v1.8.0 preps. Executed in a
**fresh worktree cloned from GitHub** (`git clone
https://github.com/kwad77/pincher /tmp/rl-relA2`, verified `HEAD ==
origin/master == a73069e`), no other worktree touched. Adversarial
review run as a Claude-driven review (stand-in for
`/codex:adversarial-review`, unavailable in this environment), same
triage rubric.

## 0. Scope fact-check (what is actually in this release)

The window `v1.8.0..a73069e` contains exactly two merges:

- **#2031** — `fix(server): add adr to the core toolset — MCP-only
  clients get an ADR path on the default surface` (issue #2020). MERGED
  2026-06-12T08:00:19Z.
- **#2033** — `fix(server): route outcome echo_source observability +
  request_id join hardening` (issue #2032). MERGED 2026-06-12T09:08:54Z.

The three stubs present in `CHANGELOG.d/` at the RC head (`2030.changed`,
`2031.fixed`, `2032.fixed`) are exactly the v1.9.0 content:
`2030.changed` is the v1.8.0 release PR's own stub, riding this release
by design (the release-stub convention — #2024's rode v1.8.0); the two
`.fixed` stubs are #2031 and #2033's. All three were assembled into the
`[1.9.0]` entry via `changelog-assemble.sh --apply`.

No GA `[GATE]` slots are in scope for this release: the v1.8 routing
ship-gate (§C, ≥30 routed Make-stage task units) measurement continues
out-of-band and asserts nothing in this release surface — consistent
with the v1.8.0 posture.

## 1. Pre-flight + frozen-surface review (ADR-0002)

Fresh worktree at `a73069e`:

- `go vet ./...` clean; `go build ./...` clean.
- `go test ./... -count=1` green (all packages; zero non-`ok` lines).
- `go test -race -timeout 1800s ./internal/server` green.

**Frozen-surface diff vs v1.8.0.** The full file diff
`git diff v1.8.0 a73069e` is tight and additive:

| File | Nature |
|---|---|
| `internal/server/schema_diet.go` | `adr` added to `coreToolset` (+ doc comment) — **additive** tool-set membership |
| `internal/server/router_tools.go` | `echo_source` field + `routerRequestID` join normalization + loud miss log — observability/robustness on the #2026 proxy path; no new tool, no I/O-schema change |
| `internal/server/testdata/schema-weight.md` | regenerated for the additive core-membership weight delta |
| `internal/server/adr_core_surface_2020_test.go` | new test (`TestADR_CoreMode_MCPRoundTrip`, `TestCoreToolset_CountAndADRMembership`) |
| `internal/server/issue2032_route_outcome_echo_test.go` | new `TestIssue2032_*` family |
| `README.md`, `docs/reference/tools.md`, `docs/reference/http-api.md`, `cmd/pinch/main.go` | 11-tool enumeration + accurate `batch` wording; `--toolset` flag help |
| `CHANGELOG.md`, `CHANGELOG.d/*` | stubs |

**Tool-contract golden (`internal/server/testdata/tool-contract.json`):
byte-identical to v1.8.0** (`git diff v1.8.0 a73069e --` reports zero
change). The frozen v1.0 contract pins the FULL/rich surface, which core
membership does not touch — no tool added, removed, or renamed; no I/O
schema changed. Verified by PR #2031 running `-update-tool-contract`:
zero diff.

**Core-surface arm counts (the only frozen-surface delta).** `adr` joins
`coreToolset`:

| Arm | v1.8.0 | RC (a73069e) | Verdict |
|---|--:|--:|---|
| Router ABSENT, default (core/lean) | 10 tools | **11 tools** | additive: `adr` joins |
| Router PRESENT, default (core/lean) | 12 tools | **13 tools** | additive: `adr` joins (router pair rides) |
| Router ABSENT, full/rich | 34 tools | 34 tools | unchanged |
| Router PRESENT, full/rich | 36 tools | 36 tools | unchanged |

- **Schema:** v41 → v41; zero migrations in the window (the #2031 change
  is tool-set membership; #2033 is in-memory proxy observability — no DB
  surface).
- **Schema weight (golden `internal/server/testdata/schema-weight.md`):**
  core+lean **3094 → 3236** approx tokens router-absent, **3515 → 3657**
  router-present — both under the unchanged **4000** budget gate
  (`TestSchemaWeight_CoreLean_UnderBudget` + `_RouterPresent_` green).
  Full/rich totals unchanged (18564 rich / 6978 lean).
- **`_meta` envelope:** untouched.
- **HTTP:** no route added or removed.
- **CLI:** no subcommand or flag change; `--toolset` help text updated to
  the 11-tool enumeration (cosmetic).
- **Symbol-ID format:** untouched.
- **Behavior changes:** all additive or detection-conditional with an
  absent default. The #2033 `echo_source` field is new output on an
  already-existing `route action=outcome` response (additive field per
  the tool-contract policy MINOR rule); the honest-422 posture from
  #2026 is preserved (the proxy never invents echo values).

**Substitution note (live tools/list diff).** The v1.4.0–v1.8.0 preps
ran a live binary-vs-binary `tools/list` diff against the installed
previous binary. This prep is a **STOP-AND-REPORT-before-live-install**
run by orchestration policy — the live `~/.local/bin/pincher` and the
:7777/:8787 daemons are NOT touched here (the orchestrator gates the
live upgrade separately because it restarts the MCP backing the human's
session). The frozen-surface assertion is therefore established by the
golden-file gates that run inside the full suite
(`TestToolContract_GoldenFile`, `TestMCPSurface_AllRegisteredToolsAgentCallable`,
`TestOpenAPI_ParityWithRegisteredHandlers`,
`TestHTTPRoutes_AllNonToolEndpointsDocumented`,
`TestReferenceMD_EveryCLISubcommandHasSection`, `TestMakeSymbolID`,
`TestSchemaWeight_*`, `TestCoreToolset_CountAndADRMembership`,
`TestADR_CoreMode_MCPRoundTrip`) plus the static `git diff v1.8.0 RC`
above — every one of which pins exactly the additive shape. The
tool-contract golden's byte-identity to v1.8.0 is the strongest single
assertion that the FULL surface is unchanged.

**Verdict: every frozen-surface touch is additive or
detection-conditional with an absent default. MINOR bump
(v1.8.0 → v1.9.0) is the correct class.**

## 2. Adversarial review (stand-in for /codex:adversarial-review)

Scope: `git diff v1.8.0..a73069e`, focus areas: (a) the #2031 `adr`
core-membership and its escape-hatch/weight implications, (b) the #2033
`echo_source` observability + `routerRequestID` join normalization.

### Sev-1 — none found

- **(a) `adr` → core** (`schema_diet.go`): purely additive membership;
  `coreToolset` is the single source of truth from which every surface
  gate derives, so the count gates (`TestCoreToolset_CountAndADRMembership`:
  11 absent / 13 router-present), the weight gates, and the contract
  goldens all move together. The full/rich tool-contract golden is
  byte-identical (core membership doesn't touch it). `adr` is a writer
  joining a surface whose escape hatch (`batch`) was read-only — exactly
  the gap #2020 reported; no new write path is created beyond making the
  already-registered `adr` tool reachable on the default advertisement.
- **(b) `echo_source` + join hardening** (`router_tools.go`): the new
  field is attached on every `route action=outcome` response and is
  purely descriptive — `cache` (auto-filled from the LRU), `caller`
  (explicit fields present), or `none` (cold miss + no caller
  `session_id`). It does not change what gets forwarded: explicit fields
  still win, a cache miss still passes the card through unchanged, the
  proxy still never invents values (#2026 honest-422 preserved).
  `routerRequestID` normalizes string-or-JSON-number on BOTH sides of the
  join, fixing the silent `.(string)` assertion that previously disabled
  the cache write with no trace — strictly more joins succeed, none
  spuriously. The `pincher.route_outcome.echo_miss` log fires only on the
  exact body a validating router 422s, so the production failure now
  names itself.

### Sev-2 — none found

### Sev-3 — noted

1. #2033 root-causes the live un-echoed 422 as in-memory LRU loss across
   a transparent stdio respawn (auto-restart-on-drift / crash /
   reconnect). The fix makes the failure *observable* and *join-robust*
   but does not *persist* the cache across respawns — by design, since
   the caller can always re-supply `session_id` and the router 422 is now
   self-describing. A persistent echo store is a possible future hardening
   if respawn-straddling pairs prove common in telemetry; recorded, no
   action needed for this release.

### What held up under attack

The single-source-of-truth `coreToolset` keeping every surface gate in
lockstep (count + weight + contract goldens move together, full/rich
contract byte-identical); the additive-only `echo_source` field that
changes observability without changing forwarded bytes; the
both-sides-normalized request_id join that only ever makes more joins
succeed; the preserved #2026 honest-422 posture (proxy invents nothing).

## 3. Live smoke

**Deferred to the gated live-upgrade step.** Per orchestration policy
this release-prep run does NOT touch the live `~/.local/bin/pincher`
binary or restart the :7777/:8787 daemons (that restart backs the
human's MCP session and is a separately-confirmed step). The in-process
equivalents of the §3 smoke probes run inside the test suite as real
in-memory MCP sessions:

- `TestADR_CoreMode_MCPRoundTrip` — real in-memory MCP `initialize` +
  `tools/list` (advertises `adr`, 11 tools) + `set → get → list`
  round-trip over `tools/call`, no env opt-out, no HTTP. This is the
  live-equivalent of the §4 "tools visible in session" check for the new
  core member.
- `TestIssue2032_*` — real-MCP-session replay of the live unit-10
  route→outcome transcript: asserts the forwarded `/v1/outcomes` body
  carries `session_id` on a cache hit (`echo_source=cache`), the caller
  workaround shape reads as `caller`, and the cold-cache minimal card
  pins the exact production failure shape now self-describing as
  `echo_source=none`.

Both families are green in the §1 suite run.

## 4. Checklist walk (RELEASING.md "Release-prep checklist")

| # | Item | Status |
|---|---|---|
| 0 | Frozen-surface review | Done (§1) — additive only; tool-contract golden byte-identical to v1.8.0, core 10→11 / 12→13, schema weight under budget, all gates green |
| 1 | Adversarial review | Done (§2) — Claude stand-in; 0 sev-1, 0 sev-2, 1 sev-3 noted |
| 2 | CHANGELOG.md | Done — 3 stubs assembled via `changelog-assemble.sh --apply`, promoted to `[1.9.0] — 2026-06-12` with theme one-liner + DOGFOOD section, link ref added, empty `[Unreleased]` recreated |
| 3 | "What's New" release body | Done — `docs/launch/v1.9.0-whats-new.md`; every cited number traced to PR bodies #2031/#2033 or the schema-weight golden |
| 4 | README roadmap | Done — v1.9 row added to "Where Pincher is now"; v1.8 compressed to history |
| 5 | README known limitations | Reviewed — the #2020 unreachable-`adr`-on-core limitation is now fixed; no stale limitation text remained that named it |
| 6 | README version-sensitive claims | Done — default-surface tool count updated 10 → 11 (and 12 → 13 with router) where stated |
| 7 | docs/reference/README.md metadata line | Reviewed — schema v41 / 34 registered unchanged; the metadata line tracks registered (not core) counts, so unchanged by this window |
| 8 | docs/ Pages audit | Done — drift grep vs v1.8.0; no version-pinned install snippet or tool-count claim in `docs/` Pages was stale for this window (the 11-tool default count lives in `docs/reference/tools.md`, updated by #2031) |
| 9 | Bench baseline decision | **Skip** (the RELEASING.md default) — neither merge changes perf shape (#2031 is tool-set membership; #2033 is proxy observability + a join fix; no indexer/query work) |
| 10 | DOGFOOD changelog section | Done — both fixes are dogfood-found (the unreachable-`adr` core gap #2020; the live un-echoed 422 #2032); honest-diagnosis-over-silent-green note; the #2030 release-stub fold-forward note |

## 5. Substitutions / deviations

- `/codex:adversarial-review` unavailable → Claude adversarial review
  over the same diff, same triage rubric (§2), as in v1.4.0–v1.8.0.
- Live binary `tools/list` diff and §3 live smoke deferred to the gated
  live-upgrade step (orchestration policy: this run does not touch
  `~/.local/bin/pincher` or restart the :7777/:8787 daemons). The
  frozen-surface assertion stands on the golden-file gates + the static
  `git diff v1.8.0 RC` (tool-contract golden byte-identical) + the
  real-in-memory-MCP-session tests (§3), each of which pins the additive
  shape directly.
- This PR's own `CHANGELOG.d` stub rides the v1.10 changelog under the
  established release-stub exemption, as #2030's rode this one.
- Milestone handling per precedent: milestone `v1.9.0` created, #2031 /
  #2033 / the release PR assigned, closed after tagging.
