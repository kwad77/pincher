<div align="center">
  <img src="assets/banner.png" alt="pincherMCP — pixel-art mascot Pinchy the crab holding a copper penny, wordmark, and tagline" width="900"/>
</div>

<div align="center">

[![CI](https://github.com/kwad77/pincher/actions/workflows/ci.yml/badge.svg)](https://github.com/kwad77/pincher/actions/workflows/ci.yml)
[![Go 1.25](https://img.shields.io/badge/go-1.25-00ADD8?logo=go&logoColor=white)](https://golang.org)
[![License: MIT](https://img.shields.io/badge/license-MIT-22c55e.svg)](LICENSE)
[![Coverage](https://img.shields.io/badge/coverage-85%25-22c55e.svg)](docs/reference/)

**Codebase intelligence server for LLM agents.**
Single binary · No cloud dependencies · Any LLM · MCP stdio or HTTP REST

[Quick Start](#quick-start) · [How it works](#how-it-works) · [What you get](#what-you-get) · 📖 **[Reference](docs/reference/)** · [Tutorials](docs/tutorials/) · [CHANGELOG](CHANGELOG.md)

</div>

---

## What pincher is

An agent asks "what does `processPayment` do?" Pincher returns the symbol's source plus its direct callers and imports in ~300 tokens. The same answer from `Read` over the containing file is ~12 KB. Multiply by every navigation step in a long session and the cost collapses an order of magnitude — measured savings on real codebases routinely sit at 80×+ versus the agent's default read-then-grep loop.

Conventional code-search tools index a codebase for humans browsing a UI. Pincher indexes the same codebase for an LLM agent calling tools: responses sized for a context window, runtime interception of `Read`/`Grep` before the agent opens a file, and a local-only binary so neither the index nor the code leaves the machine.

Under the hood, one Go binary indexes the codebase into three co-located layers — byte-offset symbol store, knowledge graph, and FTS5 full-text search — populated in a single AST parse pass from one shared `symbols` table. All three are exposed through **29 agent-callable MCP tools**, each also reachable over an **HTTP REST gateway** at `/v1/<tool>` with full OpenAPI 3.1 contracts.

Every response carries a `_meta` envelope: real BPE token counts, the savings number (totaled across sessions in SQLite), a `complexity_tier`, `capabilities`, `next_steps`, and an `X-Request-ID` for distributed tracing. Pincher is the foundation; the `_meta` envelope makes consequences legible; the LLM in your host's loop does the routing.

```json
{
  "name": "processPayment",
  "source": "func processPayment(amount float64) error { ... }",
  "_meta": { "tokens_used": 312, "tokens_saved": 14500, "tokens_saved_pct": 97.9,
             "complexity_tier": "lite", "latency_ms": 2 }
}
```

---

## Quick Start

```bash
# 1. Install — pick one
go install github.com/kwad77/pincher/cmd/pinch@latest   # Go 1.25+ on PATH
#   or download a binary:  https://github.com/kwad77/pincher/releases/latest
#   or build from source:  git clone … && go build -o pincher ./cmd/pinch/

# 2. Drop the usage policy into your client's config (one-time, idempotent)
pincher init                       # ./CLAUDE.md (Claude Code, current dir)
pincher init --target=detect       # auto-detect the host from marker files
#   other targets: cursor · codex · vscode · vscode-mcp · jetbrains ·
#   antigravity · antigravity-mcp · windsurf — see the tutorials below

# 3. Index your project
pincher index /path/to/your/project

# 4. Point your MCP client at the binary, then open the dashboard
pincher web                        # prints http://localhost:7777/v1/dashboard
```

Pincher speaks standard JSON-RPC 2.0 MCP over stdio. A minimal Claude Code config (`~/.claude/mcp.json`):

```json
{
  "mcpServers": {
    "pincher": { "type": "stdio", "command": "/path/to/pincher", "args": ["supervised"] }
  }
}
```

`args: ["supervised"]` runs pincher behind a thin supervisor that auto-respawns across crashes and binary upgrades, so a `go build` hot-swaps the running server without a manual `/mcp` reconnect. Drop the `args` to run bare.

**Per-host setup** — end-to-end walkthroughs (~10 min each) for [Claude Code](docs/tutorials/claude-code.md), [Cursor](docs/tutorials/cursor.md), [VS Code Copilot Chat](docs/tutorials/vscode-copilot.md), [Codex](docs/tutorials/codex.md), [JetBrains](docs/tutorials/jetbrains.md), [Zed](docs/tutorials/zed.md), and the [HTTP dashboard](docs/tutorials/http-dashboard.md). Managed installs (Homebrew, systemd, launchd, Windows service, Docker): [`packaging/README.md`](packaging/README.md).

---

## How it works

**Three indexes, one AST pass.** A single `ast.Extract()` call per file populates all three layers — no background sync, no drift between graph and search.

```
   Source File                 ┌───────────────┐    ┌──────────────┐    ┌─────────────────┐
        │                      │  Layer 1      │    │  Layer 2     │    │  Layer 3 — FTS5 │
   ast.Extract()  ─────────►   │  Byte-offset  │    │  Knowledge   │    │  BM25 search    │
        │                      │  symbol store │    │  graph       │    │  per-corpus     │
        ▼                      │  O(1), <1 ms  │    │  <2 ms       │    │  routing        │
   one symbols row             └───────────────┘    └──────────────┘    └─────────────────┘
```

- **Per-corpus FTS5** — code identifiers, config keys, and Markdown sections live in three separate BM25 indexes, so `search` (defaulting to `corpus=code`) isn't diluted by lockfile keys.
- **Per-symbol confidence** — lockfile keys score low, real config high; `search` defaults to `min_confidence=0.7` so noise drops out.
- **Reader pool** — a separate read-only SQLite connection pool means a busy MCP session never blocks the writer.
- **Self-healing** — `pincher supervised` + `PINCHER_AUTO_RESTART_ON_DRIFT=1` hot-swap the running binary on the next tool call; `pincher health-check` is a non-interactive liveness probe for cron / launchd / k8s.

The index stays current on its own: a per-project watcher polls (2 s active / 30 s idle), content-hashes every file, and re-extracts on change. Run `index force=true` after a pincher binary upgrade; otherwise you rarely call `index` by hand. Full mechanics in [Reference → Architecture](docs/reference/architecture.md).

---

## What you get

The `stats` tool renders a session summary directly in chat:

```
┌────────────────────────────────────────────┐
│                  SESSION                   │
│  Tool calls:          5                    │
│  Without pincher:   ~45,200 tokens         │
│  With pincher:        1,200 tokens         │
│  Saved:             ~44,000 tokens  (97%)  │
└────────────────────────────────────────────┘
```

Savings persist in SQLite across reconnects, restarts, and binary upgrades — the dashboard at `/v1/dashboard` shows the all-time total. What pincher actually replaces sets the ceiling:

| Workflow / tool | Typical saved % |
|---|---|
| Reading a function in a large file — `context`, `symbol`, `symbols` | 95-99% |
| Tracing callers or call-graph traversal — `trace`, `query` | 80-95% |
| Conceptual / BM25 search — `search` | 60-90% |
| Project orientation — `architecture`, `health`, `schema`, `list` | not measured |

Aggregate session savings land around **70-90%** on large Go/JS projects, **40-70%** on mixed-language mid-size repos, and **break-even to ~30%** on small or stub-tier-language projects where Read/Grep is genuinely competitive. Match the tool to the workflow you actually have.

**Speed** — measured on a 221-file / 3,769-symbol codebase: cold index ~900 ms, single-hop pinchQL 2 ms, BFS depth 3 <5 ms, FTS5 search 1 ms, incremental re-index <50 ms. Full benchmark methodology in [Reference → Architecture](docs/reference/architecture.md).

---

## Documentation

- 📖 **[Reference](docs/reference/)** — every tool, flag, and endpoint; start with the [`search` runbook](docs/reference/tools.md#tool-search), then branch into pinchQL, language support, HTTP API, and schema history.
- **[Tutorials](docs/tutorials/)** — per-host end-to-end walkthroughs.
- **[CHANGELOG](CHANGELOG.md)** — release-by-release history. Milestone burndown: <https://github.com/kwad77/pincher/milestones>.
- **[Migration guide](docs/migration/v0.4-to-v1.0.md)** — v0.4 → v1.0.

Current release: **v0.97.3** — Python collision and CI-wall-clock patch for the v1.0 path: repeated framework handlers such as `def _()` keep distinct qualified names without doctor collision rows, and Windows index/database tests run on separate CI shards instead of one serialized tail. Latest stable: **v0.90**. v1.0 freezes tool schemas and ships schema attestation + a public launch.

---

## Known limitations

- **Sequence-rename ID instability in YAML / JSON arrays** — inserting at index 0 renames downstream qualified names. Won't-fix in v0.7.0 ([#205](https://github.com/kwad77/pincher/issues/205)); prefer searching by name over storing the id.
- **Single-user SQLite** — cross-process indexing is safe; team / enterprise shared indexes need a server mode, out of v1.0 scope.
- **Haskell has no extractor** — indentation-sensitive layout with no `{`/`def` anchor makes regex-tier representation hard ([#1161](https://github.com/kwad77/pincher/issues/1161)). Every other detected language extracts symbols + same-file CALLS.
- **Cross-file CALLS resolution covers Go, Python, and TS/JS** — every other language has same-file CALLS only; the graph tools emit a per-language honesty warning. Rust/Java cross-file is gated on a tree-sitter decision ([#1182](https://github.com/kwad77/pincher/issues/1182) / [#1183](https://github.com/kwad77/pincher/issues/1183)).
- **Binary-version bump triggers re-extract** — a schema migration invalidates the last-indexed stamp; v0.84 language-scoped invalidation limits the blast radius to affected languages.
- **`tools/list_changed` requires client support** ([#429](https://github.com/kwad77/pincher/issues/429)) — Cursor / Codex / Zed honor it live; Claude Code needs a fresh session to discover newly-added tools.

---

## License

MIT

<div align="center">
  <img src="docs/assets/crab.png" width="32" alt="Pinchy"/>
</div>
