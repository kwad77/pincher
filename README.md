<div align="center">
  <img src="assets/banner.png" alt="pincherMCP — pixel-art mascot Pinchy the crab holding a copper penny, wordmark, and tagline" width="900"/>
</div>

<div align="center">

[![CI](https://github.com/kwad77/pincher/actions/workflows/ci.yml/badge.svg)](https://github.com/kwad77/pincher/actions/workflows/ci.yml)
[![Go 1.25](https://img.shields.io/badge/go-1.25-00ADD8?logo=go&logoColor=white)](https://golang.org)
[![License: MIT](https://img.shields.io/badge/license-MIT-22c55e.svg)](LICENSE)
[![Coverage](https://img.shields.io/badge/coverage-85%25-22c55e.svg)](docs/reference/)

**Local graph intelligence for LLM coding agents.**
Stop paying agents to rediscover the same codebase with raw file reads.

Single binary · Local index · MCP stdio · HTTP REST · OpenAPI 3.1

[Quick start](#quick-start) · [Why Pincher exists](#why-pincher-exists) · [Agent loop](#the-agent-loop) · [Savings](#savings-you-can-audit) · [Reference](docs/reference/) · [Tutorials](docs/tutorials/)

</div>

---

## Why Pincher exists

LLM coding agents are good at reasoning once they have the right context. They are bad at obtaining that context cheaply.

The default loop is expensive and noisy:

1. Search broadly.
2. Open whole files.
3. Guess which function matters.
4. Open more files to find callers.
5. Repeat after every context reset.

Pincher replaces that loop with source-grounded graph calls. An agent asks for the exact symbol, the direct context around it, the callers, or the blast radius of a diff. Pincher returns compact evidence with file, line, confidence, token accounting, and suggested next calls.

That matters because the output is not just smaller. It is routeable. Every tool response carries `_meta`: token counts, baseline method, savings, latency, warnings, capabilities, and `complexity_tier`. A host or Pincher Router can use that signal to decide whether the next step belongs on a fast cheap model, a stronger coding model, or a local long-running lane.

Pincher is not a search UI with an MCP wrapper. It is local code-navigation infrastructure for agent edit loops.

---

## Quick start

```bash
# 1. Install one binary.
go install github.com/kwad77/pincher/cmd/pinch@latest
# Or download a release binary:
# https://github.com/kwad77/pincher/releases/latest

# 2. Add Pincher's usage policy to your agent client.
pincher init --target=detect

# 3. Index a project.
pincher index /path/to/project

# 4. Run the MCP/HTTP server.
pincher supervised
# or, for the browser dashboard + HTTP API:
pincher web
```

Minimal Claude Code MCP config:

```json
{
  "mcpServers": {
    "pincher": {
      "type": "stdio",
      "command": "/path/to/pincher",
      "args": ["supervised"]
    }
  }
}
```

`supervised` keeps the provider stable across crashes and binary upgrades. You can rebuild Pincher and the next tool call restarts the inner server instead of forcing a manual MCP reconnect.

Host walkthroughs: [Claude Code](docs/tutorials/claude-code.md), [Cursor](docs/tutorials/cursor.md), [VS Code Copilot Chat](docs/tutorials/vscode-copilot.md), [Codex](docs/tutorials/codex.md), [JetBrains](docs/tutorials/jetbrains.md), [Zed](docs/tutorials/zed.md), and the [HTTP dashboard](docs/tutorials/http-dashboard.md). Managed installs live in [`packaging/README.md`](packaging/README.md).

---

## The agent loop

Pincher gives agents a small set of high-leverage moves:

| Agent question | Pincher call | What comes back |
|---|---|---|
| "Where is this thing?" | `search` | Ranked symbols from code/config/docs corpora, with IDs and confidence. |
| "What does this function need around it?" | `context` | The symbol source plus direct callees/import context. |
| "Who breaks if I change this?" | `trace` | Caller/callee paths with depth and risk labels. |
| "What did my diff touch?" | `changes` | Changed symbols, impacted callers, and tests to run. |
| "Orient me in this repo." | `architecture`, `onboard_module`, `context_for_task` | Entry points, hotspots, graph stats, module boundaries, and recent-change overlap. |
| "What should I do next?" | `guide`, `_meta.next_steps` | Recommended follow-up Pincher calls, not a generic checklist. |

Example shape:

```json
{
  "name": "processPayment",
  "file_path": "internal/payments/service.go",
  "start_line": 84,
  "source": "func processPayment(amount float64) error { ... }",
  "_meta": {
    "tokens_used": 312,
    "tokens_saved": 14500,
    "tokens_saved_pct": 97.9,
    "baseline_method": "full_file_read",
    "complexity_tier": "lite",
    "latency_ms": 2,
    "next_steps": [
      { "tool": "trace", "why": "check inbound callers before changing behavior" }
    ]
  }
}
```

The important part is the envelope. Pincher does not merely answer the immediate lookup. It leaves behind evidence a router can learn from.

---

## What Pincher indexes

One Go binary builds three layers from one extraction pass:

```
Source files
    │
    ▼
ast.Extract()
    │
    ├─► byte-offset symbol store  O(1) source retrieval
    ├─► knowledge graph           CALLS / IMPORTS / READS / WRITES / REFERENCES
    └─► FTS5 search               BM25 over code, config, and docs corpora
```

Current Pincher dogfood index for this repo reports:

- 886 files
- 8,358 symbols
- 15,867 graph edges
- node kinds including functions, methods, modules, sections, settings, and rationale nodes
- edge kinds including `CALLS`, `IMPORTS`, `READS`, `WRITES`, and `REFERENCES`

The index stays current through a per-project watcher. It content-hashes files, re-extracts changed files, and records schema/binary freshness so stale results are visible instead of silently trusted.

Pincher supports MCP stdio and HTTP REST. The HTTP gateway exposes the same tools at `/v1/<tool>` plus OpenAPI 3.1 contracts, dashboard routes, health probes, and request IDs for tracing.

---

## Savings you can audit

Pincher savings are token math, not vibes. Tool responses include:

- `tokens_used`: what Pincher returned
- `tokens_saved`: estimated tokens avoided versus the baseline
- `tokens_saved_pct`: saved / baseline
- `baseline_method`: for example `full_file_read`
- `latency_ms`: measured server-side latency

The ceiling depends on the workflow:

| Workflow | Typical outcome |
|---|---|
| `symbol`, `symbols`, `context` on large files | Often 90-99% fewer tokens than opening whole files. |
| `trace`, `changes`, `context_for_task` | Replaces multi-step grep/read/caller discovery loops. |
| `search` | Cuts broad discovery down to ranked symbol rows before the agent reads source. |
| `architecture`, `health`, `report` | Gives orientation without dumping the tree or hand-reading docs. |

Aggregate sessions on large Go/JS projects usually land around 70-90% token reduction. Smaller repos and stub-tier languages can be closer to break-even. Pincher exposes the raw numbers so you can check the claim for your own project.

For reproducibility, see [`scripts/reproduce-savings.sh`](scripts/reproduce-savings.sh), [`docs/reference/tools.md`](docs/reference/tools.md), and the dashboard at `/v1/dashboard`.

---

## Why this is different from normal code search

Normal search helps a human browse. Pincher helps an agent decide.

- It returns symbol IDs that can be reused across calls.
- It reads source by byte offset instead of reparsing or opening whole files.
- It traverses graph edges instead of hoping text search finds callers.
- It projects fields so the agent can ask for only `id,name,file_path` before reading source.
- It reports empty-result reasons, confidence, stale-index state, and suggested recovery steps.
- It accumulates savings and call evidence across sessions, which makes routing measurable.

The result is an edit loop with fewer blind reads and better handoffs after context resets.

---

## Report artifacts and v1.2 direction

Pincher v1.0 freezes the core tool/schema surface. The v1.2 direction is Pincher-native graph intelligence and routing leverage.

The first slices are already landing:

- `pincher report` generates a source-grounded architecture briefing from the existing index.
- Rationale/design-intent comments are indexed as `Rationale` symbols and grouped in reports by attached symbol or explicit file-level fallback.
- Hotspot risk scoring, surprising-connection triage, falsifiable work-impact reporting, and next-best-call guidance are the next planned pieces.

The boundary is deliberate: no default LLM extraction pipeline, no dashboard-first claims without API/report provenance, and no unsupported savings multipliers. If a report names a symbol or file, it should come with source provenance or say the data is missing.

---

## Tool surface

Pincher currently exposes 29 MCP tools. The high-frequency set:

- `guide` — choose the next Pincher call from a task description
- `search` — BM25 symbol/config/docs search
- `symbol` / `symbols` — O(1) source retrieval by stable symbol ID
- `context` — symbol plus direct callable context
- `trace` — caller/callee graph traversal with risk labels
- `changes` — git diff to impacted symbols and tests to run
- `context_for_task` — composite search/context/trace/changes starter
- `architecture` — entry points, hotspots, graph stats, surprising connections
- `health` / `doctor` / `schema` / `stats` — freshness, coverage, diagnostics, savings

The CLI also includes `pincher report`, a source-grounded architecture report artifact built from the same index.

Full reference: [`docs/reference/tools.md`](docs/reference/tools.md). HTTP shape: [`docs/reference/http-api.md`](docs/reference/http-api.md).

---

## Known limitations

- SQLite is local and single-user. Cross-process indexing is safe, but team-shared indexes need a server mode.
- Some languages have richer graph resolution than others. Go, Python, and TypeScript/JavaScript have cross-file `CALLS`; other languages may have same-file calls or symbol extraction only.
- Haskell has no extractor today.
- Sequence-like YAML/JSON arrays can produce unstable IDs when items are inserted before existing entries. Search by name instead of storing those IDs long term.
- A binary/schema upgrade can require re-indexing. Pincher reports stale indexes rather than pretending old extraction data is current.
- Some MCP clients do not honor live `tools/list_changed`; they need a fresh session after tool-surface changes.

These are reported through `health`, `doctor`, `_meta.empty_reason`, and per-language extraction coverage where possible.

---

## Documentation

- [Reference](docs/reference/) — tools, HTTP API, schema history, language support, empty reasons.
- [Tutorials](docs/tutorials/) — host-specific setup.
- [Packaging](packaging/README.md) — Homebrew, systemd, launchd, Windows service, Docker.
- [Migration guide](docs/migration/v0.4-to-v1.0.md) — v0.4 to v1.0.
- [Changelog](CHANGELOG.md) — release history.

Current stable release: [v1.0.0](https://github.com/kwad77/pincher/releases/tag/v1.0.0).

---

## License

pincherMCP source is released under the [MIT License](LICENSE) — © 2025-2026 Kevin Waddell and pincherMCP contributors. Each Go source file carries an `// SPDX-License-Identifier: MIT` header for machine-readable attribution.

- Third-party attribution: see [NOTICE](NOTICE).
- Trademark: see [TRADEMARK.md](TRADEMARK.md).
- Contributing: every PR commit needs a `Signed-off-by:` trailer. See [CONTRIBUTING.md](CONTRIBUTING.md#developer-certificate-of-origin-dco).

<div align="center">
  <img src="docs/assets/crab.png" width="32" alt="Pinchy"/>
</div>
