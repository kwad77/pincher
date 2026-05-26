# v1.0 "What's New" video outline

Draft outline for [#1538](https://github.com/kwad77/pincher/issues/1538).
Record after the landing-page copy is locked.

## Target Shape

Length: 3-5 minutes.

| Section | Time | Content |
|---|---|---|
| Intro | 0:00-0:30 | What Pincher is in one sentence. Why v1.0. The frozen-surface guarantee. |
| Local architecture | 0:30-1:20 | One binary, local MCP server, local SQLite store, project-scoped graph, FTS5 corpora. |
| Core workflow | 1:20-2:10 | `search`, `context`, and `trace` as the read-narrowing loop. |
| Composite workflow | 2:10-3:10 | `plan_change`, `investigate_failure`, `audit_unused`, `onboard_module`, `why_empty`. |
| Stability boundary | 3:10-4:00 | ADR-0002 frozen surface, language tiers, migration guide, host docs. |
| Deferred scope | 4:00-4:30 | Plugin extractors, shared indexes, resources/completions/icons after 1.0. |
| Close | 4:30-5:00 | Install link, demo links, issues link. |

## B-Roll Needed

- Fresh clone to first useful query demo.
- Edit-confidence loop demo.
- Host tutorial page.
- `pincher doctor` output on a healthy DB.
- `docs/reference/languages.md` support-tier table.
- ADR-0002 frozen-surface section.

## Recording Setup

- Screen capture: 1920x1080.
- Audio: USB mic.
- Terminal font must match the short demos.
- Avoid named-product comparisons and dollar figures.

## Publish-Time Checks

- Replace screenshots if the v0.99 RC output differs from v1.0.
- Link the video from the announcement, landing page, and GitHub release body.
