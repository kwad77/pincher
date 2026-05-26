# v0.98 external migration-review sign-off

Date opened: 2026-05-26

Scope tracker: [#1716](https://github.com/kwad77/pincher/issues/1716)

This document records the v0.98 gate: the second external migration-review
window before v0.99 hardening. It is not a release approval. v0.98 exists to
make the migration-guide review process concrete enough that v0.99 can close
with evidence instead of assumptions.

## Current Status

**Not signed off yet.** The v0.98 gate still needs at least one additional
external reviewer report linked from #1716 or #1390. Do not tag v0.98 solely
from maintainer dogfood or the hosted rehearsal.

## Evidence Already Green

The v0.98-prep hardening commit,
[`e4df914`](https://github.com/kwad77/pincher/commit/e4df914), had these
green hosted checks when the review packet first landed:

| Gate | Run | Result |
|---|---|---|
| CI | [26446353747](https://github.com/kwad77/pincher/actions/runs/26446353747) | Green |
| Host conformance | [26446353748](https://github.com/kwad77/pincher/actions/runs/26446353748) | Green |
| govulncheck | [26446353750](https://github.com/kwad77/pincher/actions/runs/26446353750) | Green |
| Pages | [26446353207](https://github.com/kwad77/pincher/actions/runs/26446353207) | Green |
| Migration rehearsal | [26446357417](https://github.com/kwad77/pincher/actions/runs/26446357417) | Green |

Current release-prep descendant
[`731329f`](https://github.com/kwad77/pincher/commit/731329f) also has green
hosted validation for the same gate: CI
[`26450244827`](https://github.com/kwad77/pincher/actions/runs/26450244827),
Host conformance
[`26450244846`](https://github.com/kwad77/pincher/actions/runs/26450244846),
govulncheck
[`26450244955`](https://github.com/kwad77/pincher/actions/runs/26450244955),
Pages
[`26450243449`](https://github.com/kwad77/pincher/actions/runs/26450243449),
and Migration rehearsal
[`26450781480`](https://github.com/kwad77/pincher/actions/runs/26450781480).

## Review Materials

- Migration guide:
  [`docs/migration/v0.4-to-v1.0.md`](migration/v0.4-to-v1.0.md)
- External reviewer packet:
  [`docs/migration/external-review-packet.md`](migration/external-review-packet.md)
- Hosted rehearsal methodology:
  [`docs/methodology/migration-rehearsal.md`](methodology/migration-rehearsal.md)

The review packet now uses the current default data-dir path
`${XDG_DATA_HOME:-$HOME/.local/share}/pincherMCP` on Linux and points reviewers
back to the migration guide for macOS, Windows, `--data-dir`, and
`PINCHER_DATA_DIR` overrides.

## Required v0.98 Tag Evidence

Before tagging v0.98, fill every row below with a concrete URL or command
output.

| Requirement | Evidence | Status |
|---|---|---|
| Hosted migration rehearsal green on the release-prep commit | [26446357417](https://github.com/kwad77/pincher/actions/runs/26446357417) | Green for `e4df914` |
| Migration guide covers current data-dir defaults | `docs/migration/v0.4-to-v1.0.md` | Green |
| External review packet has copy/paste-safe backup and probe commands | `docs/migration/external-review-packet.md` | Green |
| At least one additional external reviewer report linked | #1716 / #1390 comment URL | Pending |
| All reviewer findings dispositioned | Issue comments / follow-up commits | Pending |
| Full CI green on the final v0.98 release-prep commit | [`26450244827`](https://github.com/kwad77/pincher/actions/runs/26450244827) on [`731329f`](https://github.com/kwad77/pincher/commit/731329f) | Green |
| Host conformance green on the final v0.98 release-prep commit | [`26450244846`](https://github.com/kwad77/pincher/actions/runs/26450244846) on [`731329f`](https://github.com/kwad77/pincher/commit/731329f) | Green |
| govulncheck green on the final v0.98 release-prep commit | [`26450244955`](https://github.com/kwad77/pincher/actions/runs/26450244955) on [`731329f`](https://github.com/kwad77/pincher/commit/731329f) | Green |

## Handoff To v0.99

v0.99 inherits the migration guide only after #1716 records the second review
window outcome and #1390 has enough reviewer evidence to close or a precise
list of unresolved findings. If the external review finds a migration or
operator-instruction gap, patch the guide first and restart hosted rehearsal
before advancing to the seven-day hold.
