# Contributing to pincher

Pincher is a local code-intelligence MCP server. Contributions of all sizes are welcome — bug fixes, new language extractors, dashboard polish, documentation improvements. This doc covers the dev loop for human contributors. For Claude-Code-facing guidance see [`CLAUDE.md`](CLAUDE.md).

## Developer Certificate of Origin (DCO)

Every commit in a PR must include a `Signed-off-by:` trailer attesting to the [Developer Certificate of Origin](https://developercertificate.org). Pincher uses DCO instead of a CLA — there's no document to sign, just a one-line attestation per commit that you have the right to submit the work under the project's MIT license.

```bash
git commit -s -m "your message"            # sign the trailer at commit time
git commit --amend -s                      # sign the most recent commit if you forgot
git config --global format.signoff true    # sign every commit globally
```

> **Note:** `format.signoff` only covers commands that honour it — plain `git commit` is NOT one of them. The reliable, set-and-forget option is the repo-local hook: `pincher init --dco-hook` installs a `prepare-commit-msg` hook that appends your `Signed-off-by:` trailer automatically whenever it's missing (skips merge/squash messages, never duplicates, never touches existing trailers, and refuses to overwrite a hand-written hook). See [`docs/reference/cli.md`](docs/reference/cli.md#pincher-init---dco-hook).

The `DCO sign-off` GitHub Actions check runs on every PR and fails if any commit in the diff lacks the trailer. Bulk-fix an unsigned series with:

```bash
git rebase -i <base> --exec 'git commit --amend --no-edit -s'
```

## Dev loop in three commands

```bash
git clone https://github.com/kwad77/pincher.git
cd pincher
go test ./...        # full suite, ~90s on a developer laptop
make build PINCHER_BIN=./pincher.exe   # or `go build -o pincher ./cmd/pinch/` on Linux/macOS
```

If the test sweep is green, you can iterate.

## Build & Test reference

```bash
# Build (recommended — stamps version from `git describe`)
make build PINCHER_BIN=./pincher.exe     # Windows
make build                               # Linux/macOS

# Build + swap the on-PATH binary (autonomous dogfood path).
# Uses scripts/swap-active-binary.sh's rename-out trick on Windows where
# `cp` over a running .exe fails with "Device or resource busy" (#705).
#
# PREREQUISITES (per #1151 — fresh clones miss these silently):
#   1. An on-PATH `pincher` (Linux/macOS) or `pincher.exe` (Windows) must
#      exist. The script swaps the binary at that path; no on-PATH binary
#      → script exits 1. One-time bootstrap:
#        cp ./pincher $HOME/.local/bin/pincher   # Linux/macOS
#        copy pincher.exe %USERPROFILE%\bin\pincher.exe   # Windows
#   2. For "no manual /mcp" auto-pickup, EITHER (a) the running MCP
#      child must have been launched as `pincher supervised`, OR
#      (b) PINCHER_AUTO_RESTART_ON_DRIFT=1 must be set in the MCP
#      child's env. Without one of these, the swap lands on disk but
#      the running process keeps serving the old binary until manual
#      /mcp reconnect. `mcp__pincher__health` reports
#      `binary_stale: true` when the swap landed but the running
#      process didn't pick it up.
make install PINCHER_BIN=./pincher.exe   # Windows
make install                             # Linux/macOS

# Bare go build (skips version stamping — `pincher --version` reports "dev")
go build -o pincher.exe ./cmd/pinch/     # Windows, dev-stamped
go build -o pincher ./cmd/pinch/         # Linux/macOS, dev-stamped

# Manual stamp without make:
go build -ldflags="-s -w -X main.version=$(git describe --tags --dirty --always | sed 's/^v//')" -o pincher.exe ./cmd/pinch/

# Test
go test ./...
go test ./... -coverprofile=cover.out && go tool cover -func=cover.out | grep "^total"
go test ./internal/db/ -run TestGraphStats_WithData -v   # single test
go tool cover -func=cover.out | grep -v "100.0%" | sort -t'%' -k1 -n   # coverage gaps

# Pinned-corpus snapshots (#33)
make corpus-test                  # verify; runs in CI as Corpus snapshot
make corpus-snapshot-update       # regenerate after intentional changes

# Performance benchmarks (#50)
make bench                        # local feedback
make bench-index | make bench-server   # narrow scope
make corpus-bench                 # gate vs committed baseline (local-only since #692; not gated in CI)
make corpus-bench-update          # regen baselines (intentional perf changes only)

# Diagnostics & admin
pincher doctor [--json]
pincher rebuild-fts [--quiet]
pincher stats [--json] [--reset]
```

**After any schema change** rebuild `pincher` (or `pincher.exe`) and reconnect via `/mcp` so the running MCP picks up the new schema.

### Pinned-corpus snapshot policy (#33)

`testdata/corpus/<name>/` holds small hand-crafted corpora. `<name>.snapshot.json` is the committed expected output of `pincher index --json-summary`. Counts (symbols, edges, files, kinds, average confidence) are exact-match. Noisy fields (`db_size_kb`, `duration_ms`) are stripped.

Two redundant gates: `make corpus-test` (jq) and `TestCorpusSnapshot_*` (pure Go). The JSON diff IS the rationale; review it in PRs.

**`extraction_failures_by_reason` cross-cutting gate:** every snapshot pins a per-corpus map of failure reasons → counts. Healthy corpora show `{}`. A PR that bumps any count from 0 to N is a regression by default — fix the bug, don't update the baseline. Caught #69, #74, #79, #80 before they reached real corpora.

### Bench gating (#50)

`testdata/bench/<package>.bench.txt` holds committed `go test -bench` output captured at `-benchtime=2s -benchmem`. Comparator (`cmd/benchcmp/`) gates on `ns/op +20%` and `allocs/op +30%`. Phase 1: `continue-on-error: true` — see [CI gates](#what-ci-gates-require) below.

### Bench baseline refresh (#672 v0.79 workstream 2)

The committed baseline lives in `testdata/bench/{index,server}.bench.txt` and is pinned to **CI hardware** (Linux AMD EPYC 7763, `-4` GOMAXPROCS). Running `make corpus-bench` locally on different hardware (Windows i9, macOS arm64, etc.) produces meaningless deltas and false-positive "regressions" — the gate is only valid against the CI runner pool that produced the baseline.

To refresh the baseline on current CI hardware: trigger `.github/workflows/bench-baseline.yml` via the Actions UI `workflow_dispatch` button. Pick the tag or branch you want to baseline (typically the most recent release tag). The workflow runs `go test -bench` at `-benchtime=10s` against `internal/index` + `internal/server`, uploads the regenerated `*.bench.txt` files as an artifact, and prints a diff against the committed baseline for visibility. Download the artifact, sanity-check the deltas, copy into the repo, commit. The next `make corpus-bench` run gates against the fresh numbers.

When to refresh: (a) the v0.79 / v0.89 / v0.99 hardening releases as part of #672-shape workstream 2, (b) after a deliberate perf-affecting refactor whose new numbers ARE the rationale (then `make corpus-bench-update` locally on CI-matching hardware is also valid), (c) when CI runner pool changes (rare; rolls a new EPYC SKU or similar).

When NOT to refresh: any PR that doesn't intentionally change perf shape — re-baselining absorbs regressions silently and defeats the gate's whole purpose.

## Branch + PR shape

- **Branch from `master`.** Always cut new branches from `master`, never from another in-flight branch — tangled ancestry causes phantom conflicts on GitHub.
- **One PR per logical change.** Small PRs merge faster and review better than mega-PRs.
- **Assign to a milestone.** Every PR is assigned to a milestone at create-time. Pick the current target from [`/milestones`](https://github.com/kwad77/pincher/milestones); default to the next minor if uncertain.
- **CHANGELOG stub.** Drop a `CHANGELOG.d/<num>.<type>.md` file with one bullet (no leading dash). `<type>` is one of `added`, `changed`, `fixed`, `removed`. Stubs get assembled into `CHANGELOG.md` at release time by `bash scripts/changelog-assemble.sh --apply`.

## What CI gates require

Required checks on every PR (skipped on doc-only PRs where noted):

| Gate | What it checks |
|---|---|
| `Test (mac/ubuntu/windows)` | Full `go test ./...` on three platforms. |
| `Coverage` | Combined coverage doesn't drop. |
| `Corpus snapshot` | Per-corpus snapshot in `testdata/corpus/*.snapshot.json` matches. Bump via `make corpus-snapshot-update` if you intentionally changed extraction. |
| `Benchmark smoke` | Bench targets compile + run a short pass. |
| `Release channel rule` | Release-PR titles follow the convention. |
| `Workflow isolation lint` | GitHub workflows don't duplicate inline logic that has a canonical script. |
| `CHANGELOG stub check` | A `CHANGELOG.d/<num>.<type>.md` stub is present (skipped on doc-only PRs). |

## Test conventions

Every fix ships with **positive + negative + control + cross-check** assertions. Pattern:

```go
// Positive: feature behaves as designed on the happy path.
// Negative: feature correctly rejects / clamps / warns on edge inputs.
// Control: unrelated paths are unaffected by the change.
// Cross-check: an adjacent invariant the change could have broken still holds.
```

Specific gates that fail when changes elsewhere don't update them in lockstep:

- **New exported `*Store` method (`internal/db/db.go`):** classify in `readerRoutedStoreMethods` or `writerRoutedStoreMethods` (`internal/db/db_test.go`), or `TestStore_AllExportedMethodsClassified` fails.
- **Schema migration changes:** bump `schema_version` in 5 corpus snapshot files. `make corpus-snapshot-update` regenerates them; on Windows where `make` may be unavailable, `sed -i 's/"schema_version": N/"schema_version": N+1/' testdata/corpus/*.snapshot.json`.
- **Tool-contract changes (descriptions, InputSchema):** regenerate via `go test ./internal/server -run TestToolContract -update-tool-contract`.
- **Dashboard HTML/CSS changes:** regenerate via `go test ./internal/server -run 'TestDashboardHTMLSnapshot|TestDashboardCSS' -count=1 -update-dashboard-snapshot -update-dashboard-css-snapshot`.
- **New language extractor:** update `internal/ast/registry.go` self-registration AND `internal/db/corpus.go` `ClassifyCorpus` AND the v9 SQL trigger WHERE clauses. `TestClassifyCorpus_MatchesSQLTriggerRouting` is the gate.
- **Bounded-duplication advisories (CLI ↔ MCP doctor):** when adding a doctor advisory, ship the helper in BOTH `internal/server/admin.go` and `cmd/pinch/doctor.go` with a "mirrors X / must stay identical" comment. The CLI lives in package main and can't import the server package.

## JSON response invariants

Two invariants that recur:

- **All slice fields in tool responses must be allocated as `[]T{}`, never `var x []T`.** A nil slice marshals to `null`; consumers iterating without a null-check break. The grep canary: `grep -n "var \w\+ \[\]map\[string\]" internal/server/` should return nothing once a handler is response-stable.
- **Empty-response branches stamp `_meta.empty_reason`** (a stable enum from `internal/server/empty_reason.go`) alongside the prose `_meta.diagnosis`. The enum is the machine-readable signal; diagnosis is the human-readable one.

## Idioms

- **Logging:** `slog` everywhere. `log.Printf` silences under bench `TestMain` and corrupts baselines.
- **Reader pool:** pure SELECT methods use `s.ro.Query` / `s.ro.QueryContext`; writes use `s.db.Exec`. Routing is enforced by the classification gate above.
- **Symbol IDs:** always build via `db.MakeSymbolID(file, qn, kind)`. Never string-concat.
- **`InputSchema: json.RawMessage(` ... `)` raw-string gotcha:** backticks inside the description terminate the Go raw-string literal. Use plain double-quoted text or rewrite without — bit #293 and #302.

## Release process

- **Minor (`0.X.0`):** features, schema migrations, new CLI surface.
- **Patch (`0.X.Y`):** bug fixes only. No features, no schema changes.

## Semver in pincher 1.x ([ADR-0002](docs/adr/0002-v1-frozen-surface.md))

Starting at v1.0, pincher promises a specific surface. The rules below say exactly what triggers each release type during 1.x. Pre-1.0 these are aspirational; v0.84.0 is the freeze checkpoint after which they bind.

### What is a breaking change?

A change that breaks something in [ADR-0002's frozen surface](docs/adr/0002-v1-frozen-surface.md):

- Renaming or removing an MCP tool listed in `internal/server/mcp_surface_split_test.go expectedMCPTools`.
- Changing a tool's input/output JSON Schema in a way that's not strictly additive (removing a field, renaming a field, changing a field's type, making an optional field required, changing an enum value).
- Removing or renaming a `_meta` envelope field (additive `_v2` / `_v3` extension points are NOT breaking — they ship alongside the original).
- Renaming or removing an HTTP gateway route, or changing an existing route's response shape.
- Renaming or removing a CLI subcommand listed in `internal/server/reference_md_cli_subcommand_parity_test.go expectedCLISubcommands`, or removing a flag from one.
- Changing the symbol ID format produced by `internal/db/db.go MakeSymbolID`.

A breaking change requires a **2.0 release**. There is no in-1.x deprecation cycle for breakage of frozen surface elements — the deprecation cycle is the one-minor warning window required before *removal* of a flag or subcommand, but the removal itself is what bumps to 2.0.

### What is a minor (1.X.0)?

Any non-breaking, non-trivial change:

- Adding a new MCP tool, CLI subcommand, or HTTP route (additive — non-breaking by ADR-0002 rules).
- Adding an optional input field, or a new output field, to an existing tool.
- Adding a `_v2` / `_v3` extension to the `_meta` envelope.
- A schema migration (forward-only — pincher never migrates back).
- A new language extractor or a tier promotion (0.85 → 1.0).
- A perf or memory characterization that crosses a published claim threshold.

### What is a patch (1.X.Y)?

Bug fixes that don't change the published surface:

- Wrong behavior in an existing tool that doesn't change the contract (e.g. ranking-order bug, off-by-one in a heuristic).
- Internal performance fix.
- A wrong / misleading advisory message.
- A bug in the dashboard rendering or CSS.
- A fix to a CHANGELOG / README typo or stale claim.

Patch releases NEVER introduce schema migrations, new MCP tools, new CLI subcommands, or new HTTP routes. If a fix requires any of those, ship it in the next minor.

### Deprecation cycle for removal

Removing a CLI flag, an HTTP route's response field, or an MCP tool output field — anything not already a breaking change but still user-visible — requires:

1. One full minor of `Deprecated:` doc warnings + runtime `slog.Warn` on the deprecated path.
2. Removal in the next minor (or later).

The deprecation window gives users a chance to migrate before the field disappears. Skipping the window is a breaking change requiring a 2.0 release.

### PR-template rule

Every PR touching the frozen surface (per ADR-0002) checks the PR-template box:

> [ ] This PR changes a frozen surface element per [ADR-0002](docs/adr/0002-v1-frozen-surface.md). If yes, the change is either (a) additive, or (b) targeted for the 2.x branch.

Reviewers verify the checkbox claim before merging. CI gates (`TestToolContract_GoldenFile`, the contract tests on every frozen surface element) catch accidental breakage.

Release-prep PR (the one before tagging) MUST touch every item in the canonical [`RELEASING.md` → Release-prep checklist](RELEASING.md#release-prep-checklist) — codex adversarial review, CHANGELOG assembly, GitHub release notes, README roadmap/limitations/leading-paragraph, `docs/reference/README.md` metadata line, the `docs/` Pages-site grep audit, bench baseline decision, and the `DOGFOOD:` subsection. Don't skip.

Tag pushes trigger the auto-bump workflow for the Homebrew formula and Docker image — those don't go in the release-prep PR.

## Where to look next

- [`CLAUDE.md`](CLAUDE.md) — full dev guidance + architecture notes (longer than this file).
- [`docs/reference/`](docs/reference/README.md) — every tool, every flag, every endpoint, schema history, performance numbers.
- [`docs/troubleshooting.md`](docs/troubleshooting.md) — top recurring friction items with remediation.
- [`internal/server/empty_reason.go`](internal/server/empty_reason.go) — the empty-response taxonomy enum.

## Reporting bugs

File at https://github.com/kwad77/pincher/issues with:

- pincher version (`pincher --version`).
- Schema version (`pincher health` → `schema_version` field).
- Output of `pincher doctor` (sanitize project paths if sensitive).
- Minimum repro: the tool call + args + the unexpected behaviour.

For confirmed bugs, an `*_test.go` repro alongside the report makes the fix much faster.
