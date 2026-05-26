# v0.99 final hardening sign-off

Date opened: 2026-05-26

Scope tracker: [#676](https://github.com/kwad77/pincher/issues/676)

Recovered hosted Actions blocker:
[#1898](https://github.com/kwad77/pincher/issues/1898)

This is the living sign-off document for the final RC + seven-day hold
before v1.0. It records the evidence that must be true before tagging
`v0.99.0-rc.1`, then the evidence required to advance that exact binary
to v1.0. Until the hold starts, this document is a checklist, not a
release approval.

## Current status

**Not signed off yet.** The v0.99 gate still needs:

- External migration-guide review evidence from at least two distinct
  users, tracked in [#1390](https://github.com/kwad77/pincher/issues/1390).
- Cross-platform release/install smoke against the eventual v0.99 tag.
- The seven-day no-changes hold after `v0.99.0-rc.1`.

## Evidence already green

The latest v0.98-prep hardening commit,
[`e4df914`](https://github.com/kwad77/pincher/commit/e4df914), has these
green hosted checks:

| Gate | Run | Result |
|---|---|---|
| CI | [26446353747](https://github.com/kwad77/pincher/actions/runs/26446353747) | Green |
| Host conformance | [26446353748](https://github.com/kwad77/pincher/actions/runs/26446353748) | Green |
| govulncheck | [26446353750](https://github.com/kwad77/pincher/actions/runs/26446353750) | Green |
| Pages | [26446353207](https://github.com/kwad77/pincher/actions/runs/26446353207) | Green |
| Migration rehearsal | [26446357417](https://github.com/kwad77/pincher/actions/runs/26446357417) | Green |

The latest v0.99 release-evidence commit,
[`b9e298d`](https://github.com/kwad77/pincher/commit/b9e298d), has these
green hosted checks:

| Gate | Run | Result |
|---|---|---|
| CI | [26447393434](https://github.com/kwad77/pincher/actions/runs/26447393434) | Green |
| Host conformance | [26447393456](https://github.com/kwad77/pincher/actions/runs/26447393456) | Green |
| govulncheck | [26447393387](https://github.com/kwad77/pincher/actions/runs/26447393387) | Green |
| Pages | [26447392682](https://github.com/kwad77/pincher/actions/runs/26447392682) | Green |

Advisory perf validation for the v0.99 prep window:

| Gate | Run | Result |
|---|---|---|
| Per-tool latency | [26447225384](https://github.com/kwad77/pincher/actions/runs/26447225384) | Green |
| Multi-project ceiling | [26447225380](https://github.com/kwad77/pincher/actions/runs/26447225380) | Green |
| Resource pressure | [26447225351](https://github.com/kwad77/pincher/actions/runs/26447225351) | Green |
| Time to first success | [26447395269](https://github.com/kwad77/pincher/actions/runs/26447395269) | Green after harness fix |
| Bench baseline refresh | [26447233599](https://github.com/kwad77/pincher/actions/runs/26447233599) | Green |

The earlier time-to-first-success run
[`26447225383`](https://github.com/kwad77/pincher/actions/runs/26447225383)
failed before measurement because the workflow executed a non-executable
checkout script directly. `b9e298d` routes that script through `bash`,
matching the other advisory benchmark workflows.

Additional v0.99 local hardening after the last hosted run:

| Commit | Area | Local evidence | Hosted status |
|---|---|---|---|
| [`1f8007b`](https://github.com/kwad77/pincher/commit/1f8007b) | CI perf harness | Repairs the multi-project ceiling harness so it indexes with the supported subcommand-first CLI form and times `pincher project list --json` instead of swallowing the invalid removed `pincher list --json` path; local `bash -n`, `git diff --check`, changelog preview, CI-tier local run (`10`/`50` projects), dogfood index, and dogfood `pincher doctor` all passed | CI [`26454726188`](https://github.com/kwad77/pincher/actions/runs/26454726188), Host conformance [`26454726290`](https://github.com/kwad77/pincher/actions/runs/26454726290), govulncheck [`26454726184`](https://github.com/kwad77/pincher/actions/runs/26454726184), and Pages [`26454723570`](https://github.com/kwad77/pincher/actions/runs/26454723570) green; manual Multi-project ceiling dispatch [`26455015435`](https://github.com/kwad77/pincher/actions/runs/26455015435) green |
| [`e5ce632`](https://github.com/kwad77/pincher/commit/e5ce632) | CI perf harness | Repairs the per-tool latency harness for default-deny loopback HTTP, HTTP-only background serving, current project-scoped request bodies, and GET-only health/stats endpoints; local `bash -n`, `git diff --check`, changelog preview, full `ITERATIONS=100` per-tool latency run, dogfood index, and dogfood `pincher doctor` all passed | CI [`26454157094`](https://github.com/kwad77/pincher/actions/runs/26454157094), Host conformance [`26454157128`](https://github.com/kwad77/pincher/actions/runs/26454157128), govulncheck [`26454157096`](https://github.com/kwad77/pincher/actions/runs/26454157096), and Pages [`26454155786`](https://github.com/kwad77/pincher/actions/runs/26454155786) green |
| [`3041f60`](https://github.com/kwad77/pincher/commit/3041f60) | CI dependencies | Refreshes GitHub Actions workflows to Node 24-compatible action majors and makes `setup-go` cache invalidation explicit with `cache-dependency-path: go.sum`; local workflow lint, focused workflow-lint tests, YAML parse check, `git diff --check`, changelog preview, and dogfood `pincher doctor` all passed | CI [`26453482315`](https://github.com/kwad77/pincher/actions/runs/26453482315), Host conformance [`26453482254`](https://github.com/kwad77/pincher/actions/runs/26453482254), govulncheck [`26453482316`](https://github.com/kwad77/pincher/actions/runs/26453482316), and Pages [`26453480498`](https://github.com/kwad77/pincher/actions/runs/26453480498) green |
| [`f66909f`](https://github.com/kwad77/pincher/commit/f66909f) | Server resilience | Defers session-stat flushes quietly on `SQLITE_BUSY` and requeues buffered per-call dashboard events during maintenance-index writer contention; focused DB/server tests, full `internal/db` + `internal/server`, full `go test ./... -timeout 240s -parallel 4`, changelog preview, and dogfood `pincher doctor` all passed | CI [`26452681975`](https://github.com/kwad77/pincher/actions/runs/26452681975), Host conformance [`26452681307`](https://github.com/kwad77/pincher/actions/runs/26452681307), govulncheck [`26452681309`](https://github.com/kwad77/pincher/actions/runs/26452681309), and Pages [`26452678398`](https://github.com/kwad77/pincher/actions/runs/26452678398) green |
| [`495af9b`](https://github.com/kwad77/pincher/commit/495af9b) | CI time | Reuses the Ubuntu test pass for coverage; local full coverage flow passed at 85.0% | Covered by release-prep hosted green run [`731329f`](https://github.com/kwad77/pincher/commit/731329f) |
| [`311e16d`](https://github.com/kwad77/pincher/commit/311e16d) | DB diagnostics | `doctor` DB-size attribution now includes durable `pending_edges`; source-built doctor reported `db_bytes_estimate` on the dogfood DB | Covered by release-prep hosted green run [`731329f`](https://github.com/kwad77/pincher/commit/731329f) |
| [`78bdcda`](https://github.com/kwad77/pincher/commit/78bdcda) | Migration review | Adds `docs/release-signoff-v0.98.md`; fixes stale `pincher` data-dir backup examples to `pincherMCP` | Covered by release-prep hosted green run [`731329f`](https://github.com/kwad77/pincher/commit/731329f) |
| [`7e31d7d`](https://github.com/kwad77/pincher/commit/7e31d7d) | Release tracking | Records local v0.99 hardening status and explicitly separates local evidence from hosted release evidence | Covered by release-prep hosted green run [`731329f`](https://github.com/kwad77/pincher/commit/731329f) |
| [`e8cfccc`](https://github.com/kwad77/pincher/commit/e8cfccc) | Release tracking | Files [#1898](https://github.com/kwad77/pincher/issues/1898) and makes hosted Actions enqueue recovery an explicit v0.99 gate | Covered by release-prep hosted green run [`731329f`](https://github.com/kwad77/pincher/commit/731329f) |
| [`083452d`](https://github.com/kwad77/pincher/commit/083452d) | CLI diagnostics | Fixes `pincher doctor --help` `--project` placeholder output and documents DB-size triage fields | Covered by release-prep hosted green run [`731329f`](https://github.com/kwad77/pincher/commit/731329f) |
| [`3301fa0`](https://github.com/kwad77/pincher/commit/3301fa0) | DB cleanup | Adds `pincher project prune-dead` as a CLI fallback for dead-path cleanup when MCP hosts are unavailable | Covered by release-prep hosted green run [`731329f`](https://github.com/kwad77/pincher/commit/731329f) |
| [`ce351b3`](https://github.com/kwad77/pincher/commit/ce351b3) | CI time | Removes redundant checkout/setup-go from the `Coverage` job while preserving the check name; local full coverage flow passed at 85.3% | Covered by release-prep hosted green run [`731329f`](https://github.com/kwad77/pincher/commit/731329f) |
| [`e0aa1ed`](https://github.com/kwad77/pincher/commit/e0aa1ed) | Update/install | Teaches standalone `pincher update` to install the published release `.tar.gz` / `.zip` archives instead of falling back to `go install`; local full coverage flow passed at 85.3% | Covered by release-prep hosted green run [`731329f`](https://github.com/kwad77/pincher/commit/731329f) |
| [`9f29dda`](https://github.com/kwad77/pincher/commit/9f29dda) | Update/install | Prevents standalone `pincher update` from downgrading a prerelease build when GitHub `/releases/latest` reports the older stable channel; local full coverage flow passed at 85.3% | Covered by release-prep hosted green run [`731329f`](https://github.com/kwad77/pincher/commit/731329f) |
| [`452f9e0`](https://github.com/kwad77/pincher/commit/452f9e0) | Update/install | Replaces the temporary update-version comparator with the existing `golang.org/x/mod/semver` implementation; local `go test ./... -timeout 240s` passed | Covered by release-prep hosted green run [`731329f`](https://github.com/kwad77/pincher/commit/731329f) |
| [`87b2e47`](https://github.com/kwad77/pincher/commit/87b2e47) | CI time | Removes accidental auto-indexing from the streamable HTTP load-test warmup and caps CI server-test parallelism; local `go test ./... -timeout 240s -parallel 4` and full coverage flow passed at 85.3% | Covered by release-prep hosted green run [`731329f`](https://github.com/kwad77/pincher/commit/731329f) |
| [`c2c77db`](https://github.com/kwad77/pincher/commit/c2c77db) | CI time | Makes the watcher poll interval test-configurable so watcher tests do not wait on the 5s production cadence; local `internal/index` dropped from ~36s to ~15s under `-parallel 4`; full coverage flow passed at 85.3% | Covered by release-prep hosted green run [`731329f`](https://github.com/kwad77/pincher/commit/731329f) |
| [`45cbc57`](https://github.com/kwad77/pincher/commit/45cbc57) | Server perf | Avoids full graph stats for ghost warning total checks when project metadata already has symbol/edge totals; local `go test ./... -timeout 240s -parallel 4`, focused server tests, full coverage at 85.3%, workflow lint, and dogfood `pincher doctor` all passed | Covered by release-prep hosted green run [`731329f`](https://github.com/kwad77/pincher/commit/731329f) |
| [`6bee752`](https://github.com/kwad77/pincher/commit/6bee752) | Server perf | Uses cached project totals for edge-coverage diagnostics instead of full graph stats; local focused edge/dead-code tests, full `internal/server`, `go test ./... -timeout 240s -parallel 4`, workflow lint, and dogfood `pincher doctor` all passed | Covered by release-prep hosted green run [`731329f`](https://github.com/kwad77/pincher/commit/731329f) |
| [`4166e41`](https://github.com/kwad77/pincher/commit/4166e41) | DB perf | Forces the hotspot edge aggregation onto the existing project-leading edge index so shared DBs do not scan unrelated project edges; local `EXPLAIN QUERY PLAN`, `internal/db`, `internal/server`, full `go test ./... -timeout 240s -parallel 4`, workflow lint, and dogfood `pincher doctor` all passed | Covered by release-prep hosted green run [`731329f`](https://github.com/kwad77/pincher/commit/731329f) |
| [`03a07f6`](https://github.com/kwad77/pincher/commit/03a07f6) | Server perf | Narrows architecture's surprising-connection scan to project-local `CALLS` edges via the existing project/kind edge index instead of loading every edge record; local `EXPLAIN QUERY PLAN`, focused DB/server tests, full `internal/db`, full `internal/server`, full `go test ./... -timeout 240s -parallel 4`, workflow lint, and dogfood `pincher doctor` all passed | Covered by release-prep hosted green run [`731329f`](https://github.com/kwad77/pincher/commit/731329f) |
| [`362a738`](https://github.com/kwad77/pincher/commit/362a738) | Project resolution | Makes same-name project resolution prefer the current session project, then the newest indexed live project, while preserving exact-case and live-over-dead precedence; local focused collision tests, full `internal/server`, full `go test ./... -timeout 240s -parallel 4`, full coverage at 85.2%, workflow lint, and dogfood `pincher doctor` all passed | Covered by release-prep hosted green run [`731329f`](https://github.com/kwad77/pincher/commit/731329f) |
| [`b7439d0`](https://github.com/kwad77/pincher/commit/b7439d0) | Project resolution | Scopes cached project-name resolutions to the session context that produced them so same-name live-project collisions cannot leak across sessions for the cache TTL; local focused cache/collision tests, full `internal/server`, full `go test ./... -timeout 240s -parallel 4`, workflow lint, and dogfood `pincher doctor` all passed | Covered by release-prep hosted green run [`731329f`](https://github.com/kwad77/pincher/commit/731329f) |
| [`cce77f8`](https://github.com/kwad77/pincher/commit/cce77f8) | Project resolution | Makes `symbols(cross_project=true)` read source, staleness state, and file-access savings from each returned symbol's owning project root instead of the session root; focused server tests, focused race tests, full `internal/server`, full `go test ./... -timeout 240s -parallel 4`, and dogfood `pincher doctor` all passed | Covered by release-prep hosted green run [`731329f`](https://github.com/kwad77/pincher/commit/731329f) |
| [`3e54cd9`](https://github.com/kwad77/pincher/commit/3e54cd9) | CI validation | Stabilizes the watcher serialization test by measuring the indexer's actual active map instead of inferring in-flight state from success-only completion events; focused watcher test loop, full `internal/index`, full `go test ./... -timeout 240s -parallel 4`, and dogfood `pincher doctor` all passed | Covered by release-prep hosted green run [`731329f`](https://github.com/kwad77/pincher/commit/731329f) |

These commits are useful v0.99 hardening. Push-triggered hosted validation
passed on current hardening head
[`1f8007b`](https://github.com/kwad77/pincher/commit/1f8007b): CI
[`26454726188`](https://github.com/kwad77/pincher/actions/runs/26454726188),
Host conformance
[`26454726290`](https://github.com/kwad77/pincher/actions/runs/26454726290),
govulncheck
[`26454726184`](https://github.com/kwad77/pincher/actions/runs/26454726184),
and Pages
[`26454723570`](https://github.com/kwad77/pincher/actions/runs/26454723570)
are green. The Pages run still shows a GitHub-generated workflow Node 20
annotation for `.github#2`; repo-owned workflow files no longer reference the
old action majors.

Hosted reruns on 2026-05-26 for
[`45cbc57`](https://github.com/kwad77/pincher/commit/45cbc57) confirm that run
enqueue recovered, but setup still failed before release validation could run:

| Workflow | Run | Attempt | Result |
|---|---|---:|---|
| CI | [26447506595](https://github.com/kwad77/pincher/actions/runs/26447506595) | 2 | Failed during setup. Most jobs failed downloading `actions/setup-go@v5` from GitHub codeload; the shell-contract job reached checkout and GitHub returned HTTP 403 with `Your account is suspended.` |
| Host conformance | [26447506597](https://github.com/kwad77/pincher/actions/runs/26447506597) | 2 | Failed during setup downloading `actions/setup-go@v5` from GitHub codeload |
| govulncheck | [26447506598](https://github.com/kwad77/pincher/actions/runs/26447506598) | 2 | Failed during setup downloading `actions/setup-go@v5` from GitHub codeload |
| Pages | [26447505809](https://github.com/kwad77/pincher/actions/runs/26447505809) | 1 | Failed during setup downloading `actions/upload-pages-artifact@v3` from GitHub codeload |

The same setup blocker reproduced on the current local-evidence head
[`66dc3ea`](https://github.com/kwad77/pincher/commit/66dc3ea): Host
conformance
[`26448603431`](https://github.com/kwad77/pincher/actions/runs/26448603431)
and govulncheck
[`26448603429`](https://github.com/kwad77/pincher/actions/runs/26448603429)
failed before checkout/build/test while downloading `actions/setup-go@v5` from
GitHub codeload.

Hosted setup recovered on
[`621631f`](https://github.com/kwad77/pincher/commit/621631f): Host
conformance
[`26448978426`](https://github.com/kwad77/pincher/actions/runs/26448978426),
govulncheck
[`26448978424`](https://github.com/kwad77/pincher/actions/runs/26448978424),
and Pages
[`26448977847`](https://github.com/kwad77/pincher/actions/runs/26448977847)
passed. CI
[`26448978425`](https://github.com/kwad77/pincher/actions/runs/26448978425)
reached project tests and failed on Windows `index-db` in
`TestWatch_SerializesPerProjectIndex`; the local stabilization fix is
[`3e54cd9`](https://github.com/kwad77/pincher/commit/3e54cd9). Descendant
[`604c721`](https://github.com/kwad77/pincher/commit/604c721) passed the
Windows `index-db` shard and full CI in
[`26449531612`](https://github.com/kwad77/pincher/actions/runs/26449531612).

Manual dispatch rechecks on 2026-05-26 still fail before run creation:

| Workflow | Result | GitHub request id |
|---|---|---|
| `ci.yml` | HTTP 500 `Failed to run workflow dispatch` | `E828:3208B5:1BCAE1A:1BF0249:6A157F79` |
| `time-to-first-success.yml` | HTTP 500 `Failed to run workflow dispatch` | `E818:1BCBFC:19FD4A9:1A215B8:6A157F79` |

Earlier pushes through
[`c2c77db`](https://github.com/kwad77/pincher/commit/c2c77db) failed to create
new hosted runs, and manual dispatch returned GitHub API HTTP 500. Pushes now
create hosted runs again; the current hardening head
[`1f8007b`](https://github.com/kwad77/pincher/commit/1f8007b) has green CI,
Host conformance, govulncheck, and Pages evidence above.

The migration rehearsal now exercises the intended path:

1. Download `v0.4.1`.
2. Index a synthetic corpus with the old binary using the old
   subcommand-first CLI syntax.
3. Open the same DB with the current binary and migrate to schema v36.
4. Re-index the corpus with the current binary.
5. Probe `health`, `stats`, `schema`, and `search` through loopback HTTP.

## Required v0.99 tag evidence

Before tagging `v0.99.0-rc.1`, fill every row below with a concrete run,
commit, issue, or artifact URL.

| Requirement | Evidence | Status |
|---|---|---|
| All open v0.98/v0.99 release-scope issues dispositioned | #1716, #1390, #676 | Pending |
| Hosted Actions setup + validation for current release-prep commits | [`1f8007b`](https://github.com/kwad77/pincher/commit/1f8007b): CI [`26454726188`](https://github.com/kwad77/pincher/actions/runs/26454726188), Host conformance [`26454726290`](https://github.com/kwad77/pincher/actions/runs/26454726290), govulncheck [`26454726184`](https://github.com/kwad77/pincher/actions/runs/26454726184), Pages [`26454723570`](https://github.com/kwad77/pincher/actions/runs/26454723570) | Green |
| Migration guide externally tested by >=2 users | #1390 review comments | Pending |
| Full CI green on release-prep commit | [`26454726188`](https://github.com/kwad77/pincher/actions/runs/26454726188) on [`1f8007b`](https://github.com/kwad77/pincher/commit/1f8007b) | Green |
| Host conformance green | [`26454726290`](https://github.com/kwad77/pincher/actions/runs/26454726290) on [`1f8007b`](https://github.com/kwad77/pincher/commit/1f8007b) | Green |
| govulncheck green | [`26454726184`](https://github.com/kwad77/pincher/actions/runs/26454726184) on [`1f8007b`](https://github.com/kwad77/pincher/commit/1f8007b) | Green |
| Pages deploy green | [`26454723570`](https://github.com/kwad77/pincher/actions/runs/26454723570) on [`1f8007b`](https://github.com/kwad77/pincher/commit/1f8007b) | Green |
| Migration rehearsal green | [`26450781480`](https://github.com/kwad77/pincher/actions/runs/26450781480) on [`731329f`](https://github.com/kwad77/pincher/commit/731329f) | Green |
| Bench baseline decision recorded | [`26450267561`](https://github.com/kwad77/pincher/actions/runs/26450267561) on [`731329f`](https://github.com/kwad77/pincher/commit/731329f); artifact `bench-baseline-26450267561` | Green |
| Cross-platform install smoke ready | `install-validation.yml` after tag | Pending tag |
| Launch artifacts have no stale placeholders | `docs/launch/placeholder-audit.md` | Green |
| Post-v1.0 backlog umbrella filed | [#1897](https://github.com/kwad77/pincher/issues/1897) | Green |

## Seven-day hold protocol

The hold starts only after `v0.99.0-rc.1` is tagged and release/install
validation is green. During the hold:

- No PRs land except critical security or release-blocking corruption
  fixes.
- Any severity-1 / canonical-workflow regression slips v1.0.
- Any migration failure slips v1.0 unless it is proven to be an operator
  environment issue and documented in the migration guide.
- Dogfood findings are filed immediately and explicitly routed to
  `v1.0-blocking` or `v1.1+`; nothing discovered during the hold is left
  implicit.

## Advancement to v1.0

v1.0 is a channel retag of the held v0.99 binary. Advance only when:

- The seven-day hold completed with zero material changes.
- #1390 has at least two external review reports and every finding has a
  disposition.
- #676 has this document linked with final evidence filled in.
- #667 has the launch-day coordination items ready.

If any code change lands during the hold, restart the seven-day clock
from the replacement RC tag.
