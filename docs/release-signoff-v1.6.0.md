# v1.6.0 release-prep notes (dev channel)

Date: 2026-06-11
Branch: `release/v1.6.0` (release PR targets `master`)
Release candidate: `origin/master` @ `ea94376`
Baseline tag for all diffs: `v1.5.0`

This is the release-prep record required by RELEASING.md ("Release-prep
checklist"). Same procedure as the v1.4.0/v1.5.0 preps
(`docs/release-signoff-v1.4.0.md`, `docs/release-signoff-v1.5.0.md`):
frozen-surface review (item 0), adversarial review (item 1 — run as a
Claude-driven adversarial review, substituting for
`/codex:adversarial-review`, which is not available in this
environment), and the checklist walk. Executed in a fresh worktree of
`origin/master` at `ea94376` (`/tmp/pincher-rel`), no other worktree
touched.

## 0. Scope fact-check (what is actually in this release)

The window `v1.5.0..ea94376` contains: #2002 (messy-corpus benchmark),
#2003 (schema diet, opt-in), #2004 (startup DB discipline,
#1974/#1975), #2005 (scale + repetition round), #2006 (hook Glob
advisory + data-dir baking), #2007 (core/lean default flip), #2008
(`init --dco-hook`), #2009 (graph-authority descriptions).

**Correction made during prep:** the pre-merge "What's New" draft
attributed the nine-language tree-sitter rollout (ADR-0008,
#1957/#1958 waves) to v1.6.0. Verified against the tree: those commits
are ancestors of the `v1.4.0` tag and their CHANGELOG.d stubs were
folded into the v1.4.0 changelog entry by the v1.4.0 prep commit
(`b085915`). The v1.6.0 release docs were corrected accordingly. The
ten stubs present in `CHANGELOG.d/` at the RC head (1974, 2000, 2001,
2003, 2005, 2006×2, 2007, 2008, 2009) are exactly the v1.6.0 content
and were assembled into the 1.6.0 entry.

## 1. Pre-flight + frozen-surface review (ADR-0002)

Fresh worktree at `ea94376`: `go vet ./...` clean,
`go test ./... -count=1` green (all packages, including
`internal/server` 88s and `internal/ast` 47s legs),
`make corpus-test` green ("All corpus snapshots match").

All six contract gates run explicitly and green at the RC head:

| Gate | Result |
|---|---|
| `TestToolContract_GoldenFile` | PASS |
| `TestMCPSurface_AllRegisteredToolsAgentCallable` | PASS |
| `TestOpenAPI_ParityWithRegisteredHandlers` | PASS |
| `TestHTTPRoutes_AllNonToolEndpointsDocumented` | PASS |
| `TestReferenceMD_EveryCLISubcommandHasSection` | PASS |
| `TestMakeSymbolID` | PASS |

Live tools/list comparison, v1.5.0 installed binary vs RC binary
(stdio MCP handshake, programmatic diff):

- **Tools (full/rich):** 34 → 34. Zero added, zero removed, **zero
  `inputSchema` changes** on any of the 34. Descriptions changed on
  exactly three tools — `trace`, `changes`, `verify_change` — the
  intentional #2009 graph-authority additions.
- **Default surface:** exactly the 10 core tools
  (`batch, changes, context, guide, loop, search, symbol, symbols,
  trace, verify_change`), with lean descriptions. All 34 lean
  descriptions verified non-empty and well-formed under
  `PINCHER_TOOLSET=full` + lean (shortest: 49 chars; the three
  authority-note tools carry the note in lean form).
- **HTTP:** all 34 `/v1/<tool>` routes answer (non-404) on a
  default-mode (core/lean) daemon, probed individually; the
  unknown-tool error envelope enumerates all 34.
- **Schema:** v41 → v41, zero migrations in the window (the only
  `internal/db` schema-adjacent change is the #1974 read-only/loud-
  migration gate, pinned by `migration_gate_1974_test.go`).
- **`_meta` envelope:** no key removed or redefined.
- **CLI:** additive only — `--toolset` flag (alias of
  `$PINCHER_TOOLSET`), `init --dco-hook`. Reference-MD parity green.
- **Symbol-ID format:** untouched; `TestMakeSymbolID` green.
- **Behavior changes:** the core/lean default flip (#2007) — opt-out
  via env/flag, all tools reachable underneath; MINOR per the
  RELEASING.md rule ("default value flips … are MINOR if the change is
  opt-out"). Verified live: `PINCHER_TOOLSET=full
  PINCHER_SCHEMA_STYLE=rich` restores a surface byte-equivalent (names,
  schemas) to v1.5.0 modulo the three #2009 descriptions.

**Verdict: every frozen-surface touch is additive or opt-out. MINOR
bump (v1.5.0 → v1.6.0) is the correct class.**

## 2. Adversarial review (stand-in for /codex:adversarial-review)

Scope: `git diff v1.5.0..ea94376`, focus areas set by the release
brief: (a) the default flip's blast radius on existing installs,
(b) the DCO hook installer writing into `.git/hooks`, (c) the lean
description transform + authority-note map.

### Sev-1 — none found

- **(a) Default flip blast radius** (`schema_diet.go`, `server.go`,
  `cmd/pinch/main.go`): env opt-out verified live (full/rich restores
  all 34). `pincher supervised` passes `os.Environ()` to the inner
  process (`cmd/pinch/supervised.go:82`), so the opt-out propagates.
  Long-running HTTP daemons are unaffected: every `/v1/<tool>` route
  answers in default mode (probed all 34). `batch` dispatches its
  documented read-only sub-tool set in default mode (verified live).
  `guide` names the escape hatches when recommending a non-advertised
  tool (verified live: "restart the server with PINCHER_TOOLSET=full
  or call HTTP POST /v1/architecture"). The parse rule is the
  established #1088 unknown-value rule — only canonical `full`/`rich`
  switch; typos land on the default, never a third state, and core
  never makes a tool unreachable over HTTP/batch. The CLI hook path
  (`pincher hook-check`) does not consult the toolset.
- **(b) DCO hook installer** (`cmd/pinch/init_dco_hook.go`): non-git
  dirs skip with a notice and exit 0 (linked worktrees, where `.git`
  is a file, are conservatively treated the same); an existing
  non-pincher `prepare-commit-msg` is **never** overwritten — no
  `--force` escape by design; pincher-managed hooks are refreshed
  idempotently (byte-compare); the hook body fails open on every path
  (unset identity exits 0; merge/squash sources pass through;
  `interpret-trailers` failure falls back to plain append); explicit
  `chmod 0o755` covers the WriteFile-mode-only-on-create gotcha.
- **(c) Lean transform** (`schema_diet.go`): all 34 lean descriptions
  verified live — none empty or mangled; `firstSentence` handles
  decimals/filenames/abbreviations; `leanAuthorityNote` covers exactly
  the three #2009 tools and is appended after first-sentence cutting,
  so the authority clause survives lean mode (verified in the live
  tools/list); arg-description truncation is marked with "…";
  `leanInputSchema` recurses without rewriting non-string
  `description` members and returns input unchanged on invalid JSON.

### Sev-2 — found, recorded, NOT fixed in-window

1. **Glob advisory × core default (#2006 × #2007 cross-PR emergent
   shape).** `decideGlobHook` suggests `onboard_module`; under the new
   default `PINCHER_TOOLSET=core`, MCP `tools/call onboard_module`
   fails loud (`-32602 unknown tool` — verified live against the RC
   binary), and `onboard_module` is not on `batch`'s read-only
   sub-tool list, so the only escape hatches (HTTP `/v1`, full-toolset
   restart) are not named in the hint. Not sev-1: advisory-only (Glob
   always passes through, `continue:true`), failure is loud not
   silent, and the same hint names `search` (core). **Suggested fix:**
   make the hint toolset-aware (lead with `search` in core mode and
   name the HTTP/full escape hatch, mirroring `computeGuide`'s note).
   **Filing as a GitHub issue was permission-denied in the release
   environment** — this entry is the canonical record; needs manual
   filing with `dogfood-found` + `severity-2` + `axis-hooks`.

### Sev-3 — noted

1. `context`'s `max_tokens` floor: a budget below the primary symbol
   body's size returns the body untrimmed with no truncation marker
   (observed live: `max_tokens=80`, `tokens_used=121`). Pre-existing
   v1.4.0 budget semantics (body floor, callee/import loops trimmed
   first), not a regression in this window; worth a doc sentence.
2. The #2007 changelog stub says "`batch` sub-query dispatch keep[s]
   the full 34-tool surface" — `batch` dispatches its documented
   read-only query subset, not all 34 (HTTP does carry all 34). The
   1.6.0 entry intro was worded to avoid over-claiming.

### What held up under attack

The unknown-value parse rule (no third state, HTTP/batch reachability
invariant under any env value); supervised env propagation; the DCO
hook's refusal ladder (non-git → notice; non-pincher hook → hard
refusal; managed-stale → refresh; identical → no-op) and fail-open
body; lean-mode authority notes surviving the first-sentence cut; the
default-surface contract gate (`TestToolContract_DefaultSurface`)
pinning the 10-tool advertisement independently of the full/rich
golden; #1974's migration gate (release builds migrate loud +
snapshot, dev builds refuse without consent).

## 3. Live smoke (RC binary, real corpus)

RC binary built from the worktree (`go build ./cmd/pinch`). Scratch
data dir; **re-indexed first** (banked lesson): `/home/kwad77/pincher`
→ 985 files, 9,079 symbols, 17,224 edges, 0 skipped.

| Probe | Result |
|---|---|
| `search` | PASS — `MakeSymbolID` top hit, `_meta.watermark` present (`g0.c8`) |
| `symbols` (batch read, `max_tokens`) | PASS — 2 symbols, `tokens_used` reported |
| `context` | PASS — source + imports |
| diff-context (repeat `context`, same stdio session) | PASS — second call returns `unchanged: true` + `since_hash`, no payload resend |
| `trace` | PASS — inbound MakeSymbolID: 4 depth-1 callers, 10 total, risk labels |
| `changes` | PASS — dirty working tree mapped to section symbols |
| `batch` | PASS — read-only sub-queries dispatch in default mode; non-batchable sub-tools error honestly with the documented list |
| `loop` checkpoint → resume | PASS — checkpoint receipt; resume brief carries claim/decision |
| `verify_change` | PASS — changed_symbols/orphans/tests_to_run |
| `guide` | PASS — recommends `architecture` with the core-mode escape-hatch note |
| `_meta` watermark / budgets | PASS — watermark on every envelope; budgets accepted (see sev-3 #1 for the floor note) |

## 4. Real-MCP validation (claude -p, stdio, default core/lean surface)

One `claude -p` session, `--mcp-config` pointing at the RC binary +
scratch data dir, `--strict-mcp-config`. Four graph questions on the
pincher corpus, 5 turns, no file reads:

1. Depth-1 inbound callers of `MakeSymbolID` → **exactly** the ground
   truth set (`indexImpl`, `externalModuleSymbol`,
   `syntheticFileModuleSymbol`, `handleFetch`) with files/lines/risk.
2. Definition site/kind → `internal/db/db.go`, Function, correct
   signature.
3. Working-tree changes via `changes` → correct 3-file dirty set.
4. Tools visible in session → **exactly the 10 core tools.**

Default lean/core surface works end-to-end in a real client.

## 5. Checklist walk (RELEASING.md "Release-prep checklist")

| # | Item | Status |
|---|---|---|
| 0 | Frozen-surface review | Done (§1) — additive/opt-out only, six gates green |
| 1 | Adversarial review | Done (§2) — Claude stand-in for /codex; 0 sev-1, 1 sev-2 recorded (issue filing permission-denied — needs manual filing), 2 sev-3 |
| 2 | CHANGELOG.md | Done — 10 stubs assembled via `changelog-assemble.sh --apply`, promoted to `[1.6.0] — 2026-06-11` with theme one-liner, link refs added, empty `[Unreleased]` recreated |
| 3 | "What's New" release body | Done — `docs/launch/v1.6.0-whats-new.md`; pre-merge draft fact-checked against the tree (tree-sitter attribution corrected, §0); every cited number traced to PR bodies #2002–#2009 or re-verified live |
| 4 | README roadmap | Done — "Where Pincher is now" advanced to v1.6 (v1.5 compressed to history, four v1.6 bullets) |
| 5 | README known limitations | Done — single-writer-contention clause updated for #1974 (read-only surfaces no longer contend) |
| 6 | README version-sensitive claims | Verified — tool count (34 registered / 10 advertised) already current from #2007's own README edit |
| 7 | docs/reference/README.md metadata line | Verified current — v41 · 34 registered (10 advertised since v1.6) · ~25 languages; updated by #2007 itself, no further edit |
| 8 | docs/ Pages audit | Done — drift grep vs v1.5.0; bumped `pincher-v1.5.0` install snippets (`docs/index.html`, `deployment/systemd.md`, `homebrew.md`) and the docker pin (`deployment/docker.md`). Left alone: `go-sdk v1.4.0` dependency strings (not release versions), `packaging/` v1.0.0 stable-channel artifacts (auto-bump territory) |
| 9 | Bench baseline decision | **Skip** (default for feature releases): no perf-rationale refactor; #2004 removes a startup write-txn (less work, not a baseline-moving claim) |
| 10 | DOGFOOD changelog section | Done — 3 bullets (whats-new draft fact-check, the sev-2 Glob×core finding + denied issue filing, the release-stub fold-forward note) |

## 6. Substitutions / deviations

- `/codex:adversarial-review` unavailable → Claude adversarial review
  over the same diff, same triage rubric (§2), same as v1.4.0/v1.5.0.
- GitHub issue creation for the sev-2 finding was denied by the
  release environment's permission system; recorded in §2 and the
  DOGFOOD section instead of worked around.
- Release-prep lands via PR (the v1.4.0/v1.5.0 pattern); this PR adds
  its own `CHANGELOG.d` stub under the established release-stub
  exemption so the stub-check gate stays green; that stub rides the
  v1.7 changelog, as #2000's rode this one.
