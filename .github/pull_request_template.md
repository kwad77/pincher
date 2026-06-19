## Why

<!-- Why does this change exist? Bug? Feature? Cleanup? Link the issue if there is one. -->

## What

<!-- One-liner describing the change. Larger PRs: bullet list of the moving parts. -->

## Test plan

<!-- - [ ] go test ./... -->
<!-- - [ ] specific behaviour verified -->

## Release alignment

<!-- The PR must be assigned to a milestone at create time. See RELEASING.md > Release-prep checklist. Default to the next-shipping minor on the milestones page: https://github.com/kwad77/pincher/milestones -->

- [ ] Current minor (default) — features, schema migrations, new CLI surface
- [ ] Patch release (`v0.X.Y`) — bug fix only, no features, no schema changes
- [ ] `v1.0` — API freeze + announce (tracked at #638)

## v1.0 frozen surface (ADR-0002)

<!-- Per docs/adr/0002-v1-frozen-surface.md. Tick the first box if this PR touches any tool name, tool input/output schema, _meta envelope field, HTTP route, CLI subcommand or flag, or symbol-ID format. Reviewers verify the claim. CI gates (TestToolContract_GoldenFile, TestMCPSurface_*, TestOpenAPI_ParityWithRegisteredHandlers, TestHTTPRoutes_AllNonToolEndpointsDocumented, TestReferenceMD_EveryCLISubcommandHasSection, TestMakeSymbolID) catch accidental breakage. -->

- [ ] **This PR changes a frozen surface element** per [ADR-0002](../docs/adr/0002-v1-frozen-surface.md). The change is either (a) additive, or (b) targeted for the 2.x branch. CI contract tests verify.
- [ ] **This PR does not touch the frozen surface.** No tool / tool I/O / `_meta` / HTTP route / CLI / symbol-ID change.

## Closes / refs

<!-- Closes #N -->
