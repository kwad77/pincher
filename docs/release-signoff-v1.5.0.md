# v1.5.0 release-prep notes (dev channel)

Date: 2026-06-11
Branch: `integration/loop-wave-2` (release PR targets `master`)
Baseline tag for all diffs: `v1.4.0`

This is the release-prep record required by RELEASING.md ("Release-prep
checklist"). It captures the frozen-surface review (item 0), the
adversarial review (item 1 — run as a Claude-driven adversarial review,
substituting for `/codex:adversarial-review`, which is not available in
this environment), and the checklist walk. Same procedure as the v1.4.0
prep (`docs/release-signoff-v1.4.0.md`).

## 0. Integration record

Four feature branches merged into `integration/loop-wave-2` in order,
each build-verified, with the full suite run after the conflict-bearing
merge and after the final merge:

| # | Branch | PR | Merge | Verification |
|---|---|---|---|---|
| 1 | `feat/loopbench` | #1996 | clean | build + `bash -n` on both scripts + JSON lint on all arm/mcp specs + targeted server/db/cmd suites green |
| 2 | `feat/loop-handoff` | #1997 | clean | build + targeted server/db/cmd suites green |
| 3 | `feat/les-metric` | #1999 | **3 conflicts** (`server.go`, `docs/reference/tools.md`, `tool-contract.json`) | union-resolved (below); full `go test ./... -count=1` green |
| 4 | `feat/precompact-hook` | #1998 | clean (`db_test.go` classification map auto-unioned) | build + vet + full suite green |

**The #1997 × #1999 loop-tool reconciliation** (the known collision):

- `internal/server/server.go` loop `Description`: unioned — carries the
  `handoff`/`export` actions text from #1997 AND the `les_hint` resume
  clause from #1999, plus the dual max_tokens sentence (brief ~800 /
  export ~2000).
- `internal/server/loop_tool.go`: auto-merged; verified by inspection
  that `resume` carries BOTH the handoff hoist (newest handoff
  checkpoint leads the brief, applied after the ascending sort and
  after `omitted_checkpoints` is computed) and the `les_hint` field.
- `docs/reference/tools.md` loop section: unioned the two descriptions
  into one paragraph (handoff/export actions + les_hint).
- `internal/server/testdata/tool-contract.json`: regenerated ONCE via
  `go test ./internal/server/ -update-tool-contract` after the union;
  programmatically verified the loop entry carries both features and
  the tool count stayed 34.
- The loop test files from both branches
  (`loop_handoff_test.go`, `les_test.go`, `db/les_test.go`) are
  byte-identical to their branch versions (`git diff <branch> -- <file>`
  empty) and pass unmodified.

Branch claims verified rather than trusted: #1998's "13 new tests; full
suite green (14/14 packages)" and #1999's "full suite green" reproduced
locally; #1997's "golden + docs + OpenAPI updated" held only for its own
branch — the golden needed the post-union regeneration described above
(expected; that is what the integration pass is for). #1997's release-prep
note about the slug-named changelog fragment was acted on (all four stubs
renamed to the digits convention, `1996–1999.added.md`, before assembly).

## 1. Frozen-surface review (ADR-0002)

All six contract gates run explicitly and green at the release-prep head:

| Gate | Result |
|---|---|
| `TestToolContract_GoldenFile` | PASS |
| `TestMCPSurface_AllRegisteredToolsAgentCallable` | PASS |
| `TestOpenAPI_ParityWithRegisteredHandlers` | PASS |
| `TestHTTPRoutes_AllNonToolEndpointsDocumented` | PASS |
| `TestReferenceMD_EveryCLISubcommandHasSection` | PASS |
| `TestMakeSymbolID` | PASS |

Diff walk `git diff v1.4.0..HEAD --stat` (46 files, +3,488 / −66 before
release-prep commits), plus a programmatic golden-file comparison
old-tag-vs-head:

- **Tools:** 34 → 34. Zero tools added or removed; zero input
  parameters removed; zero new required parameters. The only schema
  movements are on `loop`: `action` enum **widened**
  (`+handoff`, `+export`), new optional args `note` (handoff) and `seq`
  (export). New output fields are additive: `receipt`/`manifest`
  (handoff), `markdown`/`from_seq`/`to_seq` (export), `les_hint`
  (resume), and new text content inside the `stats` box (LES lines) and
  `coach` findings (`les_regression`). OpenAPI output schema for `loop`
  extended additively.
- **Schema:** v41 → v41. Zero migrations this release — #1998 rides
  existing `hook_invocations` columns (`tool_name="compact"`), #1999 is
  in-memory counters + reader-routed queries over existing tables. The
  `internal/db` diff adds read-only methods only (`CountADRs`, the LES
  readers, `LoopLedgerStats`), all added to the db_test classification
  maps (unioned across #1998/#1999 in the merge).
- **`_meta` envelope:** no key removed or redefined; no new keys (les
  data rides response bodies, not `_meta`).
- **HTTP routes:** no additions or removals; parity gates green.
- **CLI:** no new subcommands or flags; `hook-check` gains PreCompact
  event routing inside the existing command (payload-discriminated via
  `hook_event_name`), `init --target=claude` registers the PreCompact
  leg additively and idempotently. Reference-MD parity green.
- **Symbol-ID format:** untouched; `TestMakeSymbolID` green.
- **Behavior changes:** `resume` leads the brief with the newest
  handoff checkpoint when one exists. Effectively **opt-in** — the
  reorder can only trigger after the caller has used the new `handoff`
  action; ledgers without handoff checkpoints render exactly as in
  v1.4.0. Pinned by test on the #1997 branch, rerun unmodified here.

**Verdict: every frozen-surface touch is additive. No 2.x-targeted
change leaked in. MINOR bump (v1.4.0 → v1.5.0) is the correct class.**

## 2. Adversarial review (stand-in for /codex:adversarial-review)

Scope: full `git diff v1.4.0..HEAD`, sev-1 focus areas set by the
release brief: (a) the resume-hoist behavior change, (b) LES counter
hot-path cost, (c) PreCompact fail-open completeness, (d) handoff
manifest leaking sensitive paths.

### Sev-1 — none found

The four named risk areas were each walked to ground:

- **(a) Resume hoist** (`loop_tool.go`): applied only when the newest
  included checkpoint is a handoff (`n > 1 && isLoopHandoff`), after
  `omitted_checkpoints` and the budget walk are computed — no count or
  budget skew; `entries[0]` (the newest checkpoint) is always included,
  so the hoisted manifest can never be silently dropped by the budget.
  Opt-in by construction (see §1). Order-pinned by test.
- **(b) LES hot path** (`recordLESSignals` in `jsonResultWithMeta` +
  the `IsError` tally in `withRequestID`): atomic adds plus one type
  assertion and a slice scan only when `warnings_v2` is present; no
  locks, no allocation, no DB I/O on the envelope path. The DB-touching
  snapshots run only inside `stats`, `coach`, and `loop resume`
  (2 point queries). Nested batch sub-calls are excluded from recording
  (no double-count). `applyLiteMeta` does not strip `warnings_v2`, so
  lite-mode responses still count.
- **(c) PreCompact fail-open**: every miss path emits
  `{"continue": true}` — stdin read error, malformed JSON, data-dir
  resolution failure, and DB-open failure all `emitPassThrough` before
  event routing; inside `decidePreCompact`, store errors, no-cwd,
  cwd-not-in-project, and empty ledger all route through `debugPass`,
  which explicitly sets `Continue = true` (the zero-value
  `hookDecision{}` is never emitted raw). Advisory responses set
  `Continue: true` explicitly. The ≤3-read budget is structurally
  enforced by the 3-method `precompactStore` interface + counting test.
- **(d) Manifest content**: the working-tree summary embeds
  **repo-relative** file names (from the `git diff` name parse in
  `AnalyzeChanges`), never absolute paths, env values, or file
  contents; ADR keys and checkpoint receipts are already
  ledger-resident. Nothing leaves the local store unless the user
  exports it (see sev-3 #1).

### Sev-2 — noted, not fixed here (file as issues before/at tag)

1. **Handoff checkpoints count as LES iterations** (cross-PR #1997 ×
   #1999 — exactly the emergent shape this review exists for). The
   handoff manifest is stored in the checkpoint's `decision` field, so
   `CountLoopCheckpointsBetween` and `LoopIterationSpan` count every
   handoff toward the non-empty-decision `iteration_cost` denominator —
   each handoff mildly deflates the apparent tokens-per-iteration, and
   the ADR's anti-gaming rule ("empty decisions never count") cannot
   catch it because the decision is maximally non-empty. Diagnostic-only
   metric, never a gate, so not sev-1. Recommended fix: exclude
   `HANDOFF`-prefixed claims from the counted set in both queries (one
   `AND claim NOT LIKE 'HANDOFF%'` per query + test-pin updates).
2. **`les_hint` attributes globally-recorded tokens to one loop.**
   `lesHintForLoop` divides `TokensUsedBetween(firstCheckpoint, now)` —
   which sums `session_tool_calls` across **all projects and all
   loops** — by one loop's checkpoint count. On a multi-project or
   multi-loop install the hint materially overstates that loop's
   iteration cost. The hint text says "across all sessions" but not
   "across all projects/loops". Recommended fix: per-loop attribution is
   not recordable today (events don't carry loop names) — either widen
   the basis wording to name the approximation, or gate the hint to
   single-project installs.

### Sev-3 — noted

1. Handoff manifests and `loop export` markdown embed repo-relative
   dirty-file names and ADR keys; users sharing exported markdown
   outside the team share their working-tree state. Inherent to the
   feature; worth one line in the tools doc eventually.
2. `recordLESSignals` type-asserts `meta["warnings_v2"]` as
   `[]map[string]any` with no test pinning that producer contract — a
   future producer storing `[]any` would silently zero the warning
   tallies (undercount, never a wrong number).
3. The loopbench pincher arms can inject the init-managed CLAUDE.md
   block into the repo under test (documented in the loopbench README;
   carried into the CHANGELOG DOGFOOD section).
4. 7d LES `waste_rate` numerator/denominator scopes differ
   (whole-session zero-unexpected counters keyed by `last_seen` vs
   per-call rows keyed by `ts`) — documented in the basis string itself;
   listed here so the follow-up that persists warning codes (v2, named
   in code comments) also reconciles the windows.

### What held up under attack

PreCompact's fail-open chain (every path checked emits
`continue:true`); the manifest budget loop (converges, hard floor, trim
announced in-text); `export` never writing files and erroring honestly
on unknown `seq`; LES's refusal to fake numbers (`-` rendering, basis
strings naming every omission, `coach` returning nil when the prior
window has no calls); `parseLoopSeedIDs` shape-gating (a false seed
still lands on `symbols`' rich error, never a guessed call);
`matchProjectForDir` longest-prefix (nested projects win, prefix
boundary respects the path separator — `/repo2` does not match
`/repo`); the db_test classification-map union (gate test enumerates
every store method; both branches' additions present).

## 3. Checklist walk (RELEASING.md "Release-prep checklist")

| # | Item | Status |
|---|---|---|
| 0 | Frozen-surface review | Done (§1) — all additive, six gates green |
| 1 | Adversarial review | Done (§2) — Claude stand-in for /codex; 0 sev-1, 2 sev-2 + 4 sev-3 recorded |
| 2 | CHANGELOG.md | Done — 4 stubs renamed to the digits convention (`1996–1999.added.md`) then assembled via `changelog-assemble.sh --apply`, promoted to `[1.5.0] — 2026-06-11` with the theme one-liner, link refs added, empty `[Unreleased]` recreated |
| 3 | "What's New" release body | Done — `docs/launch/v1.5.0-whats-new.md`; every cited number traced to the PR bodies #1996–#1999 or re-verified against the tree |
| 4 | README roadmap | Done — "Where Pincher is now" section advanced to v1.5 (v1.4 content compressed to history, four v1.5 bullets added) |
| 5 | README known limitations | Reviewed — no limitation listed there is changed by this release; no edit |
| 6 | README version-sensitive claims | Done — tool count (34) and savings claims unchanged and verified |
| 7 | docs/reference/README.md metadata line | Verified current — Schema v41 · 34 tools · ~25 languages; **no number moved this release**, no edit per the "bump when moved" rule |
| 8 | docs/ Pages audit | Done — drift grep run vs v1.4.0; fixed `pincher-v1.4.0` install snippets (`docs/index.html`, `docs/deployment/systemd.md`, `homebrew.md`, `docker.md`). `schema_v41` references still correct. Left: `go-sdk v1.4.0` dependency-version strings (not release versions) |
| 9 | Bench baseline decision | **Skip** (default for feature releases): no perf-rationale refactor; LES adds atomics to the envelope path (measured shape: no locks/allocs), not a baseline-moving change |
| 10 | DOGFOOD changelog section | Done — 4 bullets (CLI variadic `--allowedTools` workaround, loopbench CLAUDE.md injection, slug-named stubs renamed, the LES×handoff cross-PR finding) |

Pre-flight gates at the release-prep head: `go build ./...` clean,
`go vet ./...` clean, `go test ./... -count=1` green (14/14 packages),
`make corpus-test` green, release binary built and self-tested (see the
release PR body for the transcript).

## 4. Substitutions / deviations

- `/codex:adversarial-review` unavailable → Claude adversarial review
  over the same diff, same triage rubric (§2), same as the v1.4.0 prep.
- This prep was executed in the `/tmp/pincher-integ2` worktree; no tag
  is cut and nothing is merged to master by it — the release PR is the
  hand-off point, per the release brief.
