# v0.96 release sign-off

Date: 2026-05-26

Scope tracker: [#1714](https://github.com/kwad77/pincher/issues/1714)

This sign-off covers the v0.96 "sign-off + final polish" slice in the live Phase 5 plan. The older [#638](https://github.com/kwad77/pincher/issues/638) checklist is closed and explicitly superseded by [#667](https://github.com/kwad77/pincher/issues/667) plus `.planning-roadmap-to-v1.md`; this document verifies the current live gates, not the stale v0.38-era checklist text.

## v0.96 issue status

All concrete v0.96 correctness and polish items are closed:

- [#1573](https://github.com/kwad77/pincher/issues/1573) — interrupted/OOM index runs now recover by forcing the next pass to re-extract instead of trusting stamped hashes from an incomplete run.
- [#1868](https://github.com/kwad77/pincher/issues/1868) — Markdown inter-doc `REFERENCES` now resolve through source-relative paths plus anchors and persist to target sections.
- [#1869](https://github.com/kwad77/pincher/issues/1869) — Ansible `INCLUDES` and `LOADS` now persist as file-level structural edges and are recognized by query/trace.
- [#1870](https://github.com/kwad77/pincher/issues/1870) — binary-version stamping was verified already fixed on the current release line.
- [#1369](https://github.com/kwad77/pincher/issues/1369) — every MCP tool in `docs/reference/tools.md` now has a stable `#tool-<name>` anchor.

The v0.96 milestone has no remaining implementation issue other than this release-scope tracker.

## Verification

Local verification run during the v0.96 slice:

- `go test ./internal/db`
- `go test ./internal/index`
- `go test ./internal/ast -run Markdown`
- `go test ./internal/ast -run 'AnsibleIncludes|AnsibleLoads'`
- `go test ./internal/cypher`
- `go test ./internal/server`
- `go test ./cmd/pinch -run TestCorpusSnapshot`
- `go test ./cmd/pinch`
- `go test ./...`
- `git diff --check`

Docs-anchor verification:

- Server tool registry count: 29.
- `docs/reference/tools.md` explicit `#tool-<name>` anchor count: 29.
- `comm -3` between registered tool names and documented anchor names: empty.

Schema verification:

- Current schema is v36.
- `pincher doctor --json --data-dir /tmp/pincher-v36-doctor-check` reported `schema_version: 36`.

Dogfood verification:

- Codex dogfood data dir: `/home/kwad77/.local/share/pincherMCP/codex`.
- `pincher stats` continues to open the isolated Codex data dir successfully.
- Observed retry/zero-result metrics remain high enough to keep perf/polish investigation active, but they are not a v0.96 release blocker.

## Remaining v1.0 gates

v0.96 is not the v1.0 tag gate. The live Phase 5 plan still has explicit later gates:

- v0.97: [#1715](https://github.com/kwad77/pincher/issues/1715) and [#1538](https://github.com/kwad77/pincher/issues/1538) for marketing and launch-prep artifacts.
- v0.98: [#1716](https://github.com/kwad77/pincher/issues/1716) for the second external migration review window.
- v0.99: [#1390](https://github.com/kwad77/pincher/issues/1390) and [#676](https://github.com/kwad77/pincher/issues/676) for external migration sign-off and the final seven-day no-changes hold.
- v1.0: [#667](https://github.com/kwad77/pincher/issues/667) remains the umbrella for tag + launch coordination.

## Sign-off

v0.96 is ready to cut once the CI, Host conformance, govulncheck, and Pages runs for the sign-off commit are green.
