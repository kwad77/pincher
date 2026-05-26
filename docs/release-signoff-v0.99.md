# v0.99 final hardening sign-off

Date opened: 2026-05-26

Scope tracker: [#676](https://github.com/kwad77/pincher/issues/676)

This is the living sign-off document for the final RC + seven-day hold
before v1.0. It records the evidence that must be true before tagging
`v0.99.0-rc.1`, then the evidence required to advance that exact binary
to v1.0. Until the hold starts, this document is a checklist, not a
release approval.

## Current status

**Not signed off yet.** The v0.99 gate still needs:

- External migration-guide review evidence from at least two distinct
  users, tracked in [#1390](https://github.com/kwad77/pincher/issues/1390).
- Final perf validation against the current committed baseline and, per
  `.x9` policy, a bench-baseline refresh decision.
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
| Migration guide externally tested by >=2 users | #1390 review comments | Pending |
| Full CI green on release-prep commit | Actions run URL | Pending |
| Host conformance green | Actions run URL | Pending |
| govulncheck green | Actions run URL | Pending |
| Pages deploy green | Actions run URL | Pending |
| Migration rehearsal green | Actions run URL | Pending |
| Bench baseline decision recorded | Workflow run or explicit no-refresh rationale | Pending |
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
