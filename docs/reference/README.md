# pincherMCP Reference

The long-form reference. The [README](../../README.md) is the pitch + quickstart; this folder is the manual. For 10-minute end-to-end walkthroughs, see [`tutorials/`](../tutorials/) — [Claude Code](../tutorials/claude-code.md), [Cursor](../tutorials/cursor.md), [HTTP dashboard](../tutorials/http-dashboard.md).

**Schema version:** v41 · **MCP tools:** 36 registered (10 advertised over `tools/list` by default since v1.6 — schema diet; +2 conditional router proxies, `models`/`route`, advertised only when a live pincher-router is detected; `PINCHER_TOOLSET=full` restores the full surface, every tool always reachable over HTTP `/v1/<tool>`) · **MCP prompts:** 1 (`guide`) · **Languages detected:** ~25 (AST/parser-tier + regex-tier, plus 1 stub-tier — Haskell; see [Language support](languages.md) for the per-tier breakdown)

## Contents

- [Architecture & internals](architecture.md) — two-process architecture, three-layer storage, pinchQL routing, data flow, schema + migration history, key invariants, project layout, performance, test coverage, dependencies.
- [The 36 MCP tools](tools.md) — full tool catalogue, stable symbol IDs, field projection, empty-response taxonomy, extraction confidence.
- [pinchQL query reference](pinchql.md) — the graph-query language grammar and supported operators/clauses/kinds.
- [Language support](languages.md) — per-language extraction table, capability matrix, v1.0 fitness declaration, skip rules, bloat traps, cross-process safety.
- [HTTP REST API](http-api.md) — `POST /v1/{tool}`, additional endpoints, server-side env knobs, observability.
- [CLI subcommands & flags](cli.md) — every `pincher` subcommand, CLI flags, environment variables, data directory.
- For release themes, roadmap, and known limitations, see the [repo README](../../README.md).
