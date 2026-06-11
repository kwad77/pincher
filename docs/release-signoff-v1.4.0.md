# v1.4.0 release-prep notes (dev channel)

Date: 2026-06-11
Branch: `integration/loop-substrate-wave` (release PR targets `master`)
Baseline tag for all diffs: `v1.3.0-rc.1`

This is the release-prep record required by RELEASING.md ("Release-prep
checklist"). It captures the frozen-surface review (item 0), the
adversarial review (item 1 — run as a Claude-driven adversarial review,
substituting for `/codex:adversarial-review`, which is not available in
this environment), and the checklist walk.

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

Diff walk `git diff v1.3.0-rc.1..HEAD --stat` (229 files, +24,445 / −557
before release-prep commits):

- **Tools:** 29 → 34. Added: `assert_graph`, `batch`, `coach`, `loop`,
  `verify_change`. Programmatic golden-file comparison confirms **zero
  removed tools and zero removed input parameters** on the 29 pre-existing
  tools; golden-file removals in the diff are description-text rewrites
  only. New args on existing tools (`max_tokens`, `detail`, `count_only`,
  `format`, `compact`, `diff`, …) are all additive.
- **Schema:** v39 → v41, both migrations additive (`CREATE TABLE
  loop_checkpoints`; `ALTER TABLE hook_invocations ADD COLUMN
  est_tokens_served / baseline_tokens`). Both classified
  `invalidatesNothing`; `schemaMigrationInvalidates` slice length matches
  (init-time panic guard). Fresh-DB and v39-upgrade paths converge on an
  identical v41 schema. (See adversarial finding S2-2 for a
  feature-branch-only renumbering caveat.)
- **`_meta` envelope:** additive only — `watermark`, `seed_quality`,
  `skeleton`, `meta_delta`, new `warnings_v2` codes. No key removed or
  redefined. `watermark` deliberately survives `meta=lite` (documented in
  the changelog; flagged as S3 for golden-snapshot consumers).
- **HTTP routes:** no removals; route/doc parity gate green.
- **CLI:** new subcommand `pincher test-impacted`, new `init` target
  `claude-skills`; no flag removals/renames. Reference-MD parity green.
- **Symbol-ID format:** unchanged (`{file_path}::{qualified_name}#{kind}`);
  `TestMakeSymbolID` green. The AST-tier language wave changes some
  individual symbol IDs where tree-sitter scopes more precisely than the
  regex tier — that is data churn on re-index (announced in CHANGELOG
  "Breaking notes"), not a format change.
- **Defaults flipped (MINOR-with-opt-out per the tool-contract policy):**
  diff-encoded context ON (`PINCHER_DIFF_CONTEXT=0` opts out; per-call
  `diff=false` added in this prep — see below), session-delta `_meta` ON
  (`PINCHER_META_DELTA=0` opts out).

**Verdict: every frozen-surface touch is additive. No 2.x-targeted change
leaked in.**

## 2. Adversarial review (stand-in for /codex:adversarial-review)

Scope: full `git diff v1.3.0-rc.1..HEAD`, sev-1 focus on
silent-confidently-wrong responses, canonical-workflow breaks, data loss,
migration ordering (fresh + existing DBs, v39→v40→v41), and the
default-flips for callers that cannot opt out. Run by an independent
adversarial agent; findings triaged below.

### Sev-1 — FIXED in this release-prep commit

1. **Diff-context default-ON served `{unchanged:true}` to consumers that
   never saw the source.** The #655 cache was process-wide, keyed
   `(project, symbol)` only — any second consumer of the same server
   process (HTTP REST clients, a fresh MCP session on a streamable-HTTP
   server, a subagent sharing a parent connection) could receive
   `{unchanged:true}` or a line diff against a baseline it never held,
   with no per-call opt-out. **Fix:** cache is now connection-keyed
   (MCP `Session.ID()` for streamable HTTP, process-singleton key for
   stdio); the HTTP REST dispatch and `batch quiet` sub-queries bypass it
   entirely; an additive `diff=false` arg on `context` bypasses per call;
   the `{unchanged:true}` short-circuit now carries a recovery hint
   naming `diff=false`. Regression tests:
   `TestHandleContext_DiffFalse_BypassesCache`,
   `TestHandleContext_NoDiffCtx_BypassesCache`,
   `TestHandleContext_Unchanged_CarriesRecoveryHint`.
2. **Diff cache recorded "served" before truncation/projection — the full
   body became silently unobtainable.** `context(id, max_tokens=N)` or
   `fields=callees` populated the cache with the full source while the
   caller received a truncated or bodiless response; the natural follow-up
   full fetch then short-circuited to `{unchanged:true}`. Cross-PR
   interaction (PR-4' diff-default × PR-5 budgets × M13 batch quiet) that
   no single branch's tests could catch. **Fix:** the cache only engages
   on full-fidelity serves (`max_tokens` unset, no body-dropping `fields`
   projection, not skeleton, not REST, not quiet). Regression tests:
   `TestHandleContext_MaxTokens_DoesNotPoisonDiffCache`,
   `TestHandleContext_FieldsProjection_DoesNotPoisonDiffCache`.
3. **`ts.wasm` (23.8 MB embedded binary, parses untrusted repo content at
   confidence 1.0) had zero in-repo provenance**, while ADR-0008 (Accepted)
   claims a reproducible build verified in CI. **Fix (interim):** SHA-256
   pin (`internal/tsbridge/ts.wasm.sha256`) + gate test
   (`TestTSWasm_MatchesPinnedChecksum`) make any blob swap an explicit
   two-file reviewable change; ADR-0008 amended with an honesty note that
   the rebuild-in-CI pipeline + SBOM remain open and are a precondition
   for promoting further grammars. The full reproducibility pipeline is
   follow-up work, not shippable in a release-prep window.

### Sev-2 — noted, not fixed here (file as issues before/at tag)

1. **Repeat-read hook hint can claim "identical bytes" against the wrong
   baseline** (`cmd/pinch/hook_check.go` + `HookFileSeenInSession`): after
   an in-session edit + watcher re-index, a verification re-read is told a
   re-read returns identical bytes relative to the *indexer's* hash, not
   what the agent last saw. Advisory-only hook (the Read still returns true
   bytes), but the hint trains agents to skip verification re-reads.
   Recommended fix: store a serve-time content hash, or scope the hint to
   sessions with no intervening index of the file.
2. **The v40→v41 renumbering can brick feature-branch-v40 databases.** A DB
   migrated by the pre-integration `feat/hook-redirect-v2` binary is at
   `schema_version=40` with the hook columns already present and no
   `loop_checkpoints`; this binary then re-runs the non-idempotent v41
   `ALTER` → "duplicate column name" → `db.Open()` hard-fails. Affects
   dogfood/rc DBs only (no public release carried the old numbering), but
   the maintainer dogfoods that branch. Recommended fix: column-exists
   guard on the v41 step (or a one-time repair in the idempotent
   schema-parity step 4.5). **Do this before pointing a
   feature-branch-v40 DB at the release binary.**
3. **WASM-boundary errors are swallowed inside "clean parse"** in all nine
   tree-sitter extractors (errors from `kind`/`IsError`/`StartByte`
   `_`-discarded): a runtime fault mid-walk yields a silently incomplete
   confidence-1.0 extraction instead of regex fallback. Low trigger
   probability, maximal failure direction; fix is cheap (any boundary
   error ⇒ treat file as parse-unclean).

### Sev-3 — noted

1. `verify_change` swallows the orphan-check DB error (`possibly_orphaned:
   []` indistinguishable from "check failed"; `orphanTotal` capped at 11
   while reading as a count).
2. `assert_graph exists` converts a search error into a `pass:false`
   "does not exist" verdict (error discarded in the FTS fallback).
3. Hook suggestion JSON built via `Sprintf` without escaping — a path/QN
   containing `"` or `\` yields invalid `suggested_args` (also persisted
   into telemetry).
4. `_meta.watermark` survives `meta=lite` and embeds a per-call sequence —
   downstream full-envelope golden snapshots break by design (the repo's
   own goldens needed two reconcile commits during integration).
5. JS/TS bare-call unique-name binding can false-bind a shadowed local to
   a same-directory symbol — manufactured-but-plausible CALLS edges feed
   `dead_code`/`trace` on web repos (documented heuristic; cells marked ⚠️
   in languages.md).
6. `LoopLedgerNonEmpty`'s column-mismatch fallback answers from *any*
   project's checkpoints — `guide` can recommend loop-resume in a project
   with no loops.
7. `docs/index.html` savings-stat parenthetical still says "(… v0.94)" —
   left as-is deliberately: the numbers were measured on v0.94 and
   relabeling without re-measuring would be dishonest. Re-measure on
   v1.4.0 as follow-up.

### Waived

- CI coverage gate exclusion of `internal/tsbridge/` (WASM glue is
  exercised by fuzz/no-leak/differential tests rather than line coverage).
  The simultaneous exclusion of `cmd/pinch/` is broader than necessary —
  revisit post-release.

### What held up under attack

`batch` partial-failure semantics (per-entry errors, `upstream_empty`
skips, never a guessed chained call); `loop` checkpoint writes (no
swallowed errors, receipt only post-insert); dispatch-blind capping (can
only downgrade confidence, nil/empty-name safe); hook budget math (floor
400, cannot go non-positive); lite/skeleton truncation always announced;
coach refuses to invent figures on pre-v41 schemas; migration
fresh-vs-upgrade convergence; `wazero` dep (reputable, pinned).

## 3. Checklist walk (RELEASING.md "Release-prep checklist")

| # | Item | Status |
|---|---|---|
| 0 | Frozen-surface review | Done (§1) — all additive |
| 1 | Adversarial review | Done (§2) — Claude stand-in for /codex; 3 sev-1 fixed, 3 sev-2 + 7 sev-3 recorded |
| 2 | CHANGELOG.md | Done — 39 stubs assembled via `changelog-assemble.sh --apply`, promoted to `[1.4.0] — 2026-06-11`, link refs added (incl. the previously-missing `[1.3.0-rc.1]`), empty `[Unreleased]` recreated |
| 3 | "What's New" release body | Done — `docs/launch/v1.4.0-whats-new.md` (drafted, every number verified against the tree; tool count corrected to 34, unchanged-shortcircuit ~100 tokens, skeleton 120-line/<25%, loop resume ~800-token default, nonexistent `scripts/loopbench` reference removed) |
| 4 | README roadmap table | N/A as a table (README no longer carries one); the equivalent "Where Pincher is now (v1.4)" section rewritten from the stale "v1.2 direction" copy |
| 5 | README known limitations | Done — language-tier item rewritten for the AST wave (12 AST-tier languages; cross-file-calls caveats preserved) |
| 6 | README version-sensitive claims | Done — tool count already 34; limitation + direction sections updated |
| 7 | docs/reference/README.md metadata line | Already current (Schema v41 · 34 tools) from the integration pass; verified |
| 8 | docs/ Pages audit | Done — drift grep run; fixed `pincher-v0.94.0` install snippets (index.html, systemd, homebrew, docker), `schema_v38` examples → `schema_v41` (streamable-http, release-channels), languages.md AST-tier wave (3 tables, 8 languages), RELEASING.md "29 MCP tools" → 34. Left: index.html "(… v0.94)" measurement provenance (see sev-3 #7), historical migration-guide rows |
| 9 | Bench baseline decision | **Skip** (default for feature releases): v1.4 changes envelope shapes deliberately but ships no perf-rationale refactor; the committed baseline stays as the gate |
| 10 | DOGFOOD changelog section | Done — 5 bullets (dispatch-blind near-miss, context_for_task mis-seed, dishonest hook savings, Goose lock deadlock, web-code sparse graphs) |

Pre-flight gates at the release-prep head: `go build ./...` clean,
`go test ./... -count=1` green (all packages), `make corpus-test` green
("All corpus snapshots match").

## 4. Substitutions / deviations

- `/codex:adversarial-review` unavailable → independent Claude
  adversarial-review agent over the same diff, same triage rubric (§2).
- Item 1's "file sev-2/3 with `dogfood-found` labels" — findings are
  recorded here and summarized in the release PR body; issue filing is
  left to the maintainer (labels/routing tables are repo-owner workflow).
- This is a **dev-channel** release: tag/publish, install-validation
  follow-through, and Homebrew verification (stable-channel cells skip on
  dev tags) happen post-PR-merge, not in this prep.
