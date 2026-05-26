# v1.0 r/golang post draft

Draft for [#1538](https://github.com/kwad77/pincher/issues/1538). Post after
the v1.0 announcement and demo links are public.

## Title Options

- Pincher 1.0: local code intelligence for Go-heavy agent workflows
- Pincher 1.0: a local MCP code-intelligence server for agents
- Pincher 1.0: SQLite-backed symbol search, call graphs, and context for agents

## Draft

Pincher 1.0 is out: a local code-intelligence server for coding agents. It
indexes a repository into symbols, edges, byte ranges, and FTS5 corpora, then
exposes that through MCP tools and a CLI.

The Go-specific part that may be interesting here is the shape of the system:

- Go gets AST-backed extraction.
- Other languages are tiered explicitly instead of pretending every extractor
  has the same confidence.
- SQLite is the local store, with project-scoped symbol/edge tables and FTS5
  routing for code, config, and docs corpora.
- The v1.0 surface is deliberately local and single-user: one binary, local MCP
  server, local SQLite DB.

The main workflow is not "replace reading code." It is "ask narrower questions
before reading code":

- find this symbol;
- show the focused source and dependencies;
- show inbound callers before a change;
- rank likely suspects from a failing test output;
- summarize a module boundary for onboarding.

For 1.0, the compatibility contract is the point. The frozen surface is written
down in an ADR, and deferred surfaces are explicit: plugin extractors, shared
team indexes, and additional MCP protocol capabilities are 1.x work rather than
being frozen early.

Links:

- Announcement: `<<blog_url>>`
- Release: `<<release_url>>`
- Frozen surface ADR: `<<adr_0002_url>>`
- Migration guide: `<<migration_guide_url>>`
- Demo: `<<fresh_clone_demo_url>>`

Questions and bug reports are welcome in GitHub issues: `<<issues_url>>`

## Publish-Time Checks

- Replace every `<<...>>` slot.
- Keep the technical angle concrete; do not turn it into launch-copy adjectives.
- Do not include dollar figures or named-product comparisons.
- Mention deferred plugin/team-index scope if the comments ask about it.
