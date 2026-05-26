# v1.0 internal Slack announcement draft

Draft for [#1538](https://github.com/kwad77/pincher/issues/1538). Post after
the v1.0 release exists and the public announcement link is live.

## Draft

Pincher 1.0 is live: `<<release_url>>`

The short version: Pincher is now a stable local code-intelligence server for
agent workflows. It gives agents targeted code-search, source context, graph
trace, and composite tools before they fall back to broad file reads.

Useful links:

- Announcement: `<<blog_url>>`
- Fresh-clone demo: `<<fresh_clone_demo_url>>`
- Edit-confidence demo: `<<edit_confidence_demo_url>>`
- Migration guide: `<<migration_guide_url>>`
- Issues: `<<issues_url>>`

What is stable in 1.0:

- MCP tool schemas and `_meta` response conventions.
- Local SQLite schema migration path.
- CLI setup, doctor, stats, verify, and project-management workflows.
- Host setup docs for the supported editor/agent integrations.

What is deferred:

- Plugin extractors.
- Shared team indexes.
- Additional MCP protocol capabilities like resources, completions, and icons.

Please route upgrade issues to GitHub issues with the `pincher doctor --json`
output attached when possible.

## Publish-Time Checks

- Replace every `<<...>>` slot.
- Do not paste private channel names into the public repo.
- Confirm the release link points at `v1.0.0`, not the previous RC.
