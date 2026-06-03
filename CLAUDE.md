# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Pincher Usage Policy

This project ships pincherMCP — its own product — and dogfoods it. Prefer pincher tools over `Read`/`Grep`/`Glob` for any code-navigation task.

**Workflow:** `architecture` (orient) → `search` (find) → `context` or `symbol` (read) → `trace` (impact) → edit → `changes` (verify before push).

**Fall back to `Read`/`Grep` when:**
- Pincher returns no result (rare for code; common for config/text files).
- You need exact-byte inspection (whitespace audits).
- The file isn't indexable (binaries, large lockfiles).
- You're authoring a new file.
- **The pincher freshness check fires** (see below).

If `mcp__pincher__*` tools aren't in the registry at session start, surface a one-line warning before the first response and fall back to `Read`/`Grep`.

### Pincher freshness check (this repo specifically)

This is pincher's own repo, so the running MCP server is frequently stale relative to master. **Once per session, call `health`. If `running_binary_version` differs from the project's `schema_version_at_index` for `sessionRoot`, treat byte-offset tools (`symbol`, `context`, `neighborhood with include_source=true`) as untrusted — bytes may point at the wrong span. Discovery tools (`search`, `query`, `trace`, `changes`) stay reliable.** Use `Read` for the untrusted reads until the binary is rebuilt and `/mcp` reconnects.

## Release process

- **Minor** (`0.X.0`) — features, schema migrations, new CLI surface.
- **Patch** (`0.X.Y`) — bug fixes only. No features, no schema changes.
- **Major** — reserved for 1.0+.

**Every PR must be assigned to a milestone at PR-create time.** Milestones live at https://github.com/kwad77/pincher/milestones. Default to the next milestone; don't leave a PR unassigned. A release ships when its milestone hits 100% closed.

The full **10-item release-prep checklist** — codex adversarial review, CHANGELOG assembly, GitHub release notes, README roadmap/limitations/leading-paragraph updates, `docs/reference/README.md` metadata, `docs/` Pages-site audit, bench baseline decision, DOGFOOD subsection, plus post-tag install validation — lives in [`RELEASING.md` → Release-prep checklist](RELEASING.md#release-prep-checklist). Don't skip.

## Dogfood routing

When a probe surfaces net-new work mid-flight, route by **type**, not just severity. Sloppy routing either silently bloats planned scope or loses the finding entirely. Long-form context lives in `.planning-roadmap-to-v1.md`; the rules below are the autonomous-loop quick-reference.

### Routing table

| Discovery shape | Lands in |
|---|---|
| **Regression in shipped code** — canonical workflow broken | Next patch (v0.81.x / v0.82.x). Ships within days, doesn't wait for the next minor. |
| **Bug in current in-flight PR** | Same PR or a sibling PR before merge. Never punted forward. |
| **Bug in shipped feature, silent-wrong / misleading** | Next minor's dogfood reserve slot. |
| **Net-new capability gap** — missing composite / doctor advisory / tool | Triage: v1.0 blocker → insert into nearest minor with reserve room. Otherwise → file with `v1.x` milestone. |
| **Perf gap crossing a published claim** | v1.0 blocker. Next `.x9` hardening minor. |
| **Schema / API surface issue** | **Before v0.84 API-freeze checkpoint:** next minor. **After v0.84:** slips v1.0 unless it's a corruption bug. |
| **Docs / UX polish** | Next reserve slot, or v0.95 final dogfood reserve, whichever closer. |

### Decision authority (no over-asking)

- **Severity-1 / canonical workflow break** → file + ship as patch without asking.
- **Bug in in-flight PR** → fix in same/sibling PR, mention in PR body.
- **Net-new capability surface** (new tool / composite / advisory) → file with recommended milestone, then ask before assigning.
- **API-surface change after v0.84 freeze** → ALWAYS ask, never assume.

### Buffer + overflow rules

- **Soft overflow:** when a minor's dogfood reserve fills, the next 2–3 items roll forward one minor. Planned items keep their slot; the polish scope of the receiving minor absorbs the overflow.
- **Hard overflow signal:** reserve overflows by >50% in two consecutive minors → planned-vs-discovery ratio is wrong. Pause planned work, drain dogfood first, update `.planning-roadmap-to-v1.md`.
- **Dogfood beats planned:** dogfood-found work that compromises a v1.0 surface guarantee takes priority over any `FILE-X` item from the roadmap.

### Volume-based axis escalation

If the same axis (e.g., `axis-extractor-bash`, `axis-trace-bidirectional`) generates >3 `dogfood-found` issues in a release window, that axis gets a dedicated hardening slot in the next `.x9`, not patched piecemeal. Precedent: v0.56 resolver bug family (4 related fixes that should have been one batched session).

### Tagging discipline (makes routing auditable)

Every probe-surfaced issue gets:
- `dogfood-found` — distinct from user-reported, so we can audit "how much v1.0 work came from dogfood vs planning"
- `axis-{ast,doctor,cli,trace,fts,extractor-go,extractor-bash,...}` — feeds the >3-in-a-release escalation trigger
- `severity-{1,2,3}` — drives the routing table above

## CI conventions

- **Required gates:** `Test (mac/ubuntu/windows)`, `Coverage`, `Corpus snapshot`, `Benchmark smoke`, `Release channel rule`, `Workflow isolation lint`, `CHANGELOG stub check`, `PR title/body issue refs`, `Host conformance`. Merge requires all green (the stub check is skipped on doc-only PRs; the PR title/body check runs on pull_request only and validates that any `(#N)` in the title matches the body's `Closes #M` / `Refs #M` references via `scripts/pr-issue-consistency.sh`; `Host conformance` replays the per-host canonical-workflow corpus and was promoted from advisory to required in v0.91 per #1536).
- **Removed in v0.55:** `Benchmark regression (advisory)` (#692) — failed on most PRs from runner variance and signal-to-noise hit zero. `make corpus-bench` survives for local perf validation.
- **Wakeup timing:** Windows test queues 4–7 min behind ubuntu/mac. When polling CI, schedule a 270s wakeup (not 60s) — fits inside the 5-min cache TTL twice.
- **Stub-file convention for CHANGELOG (#694):** instead of editing `CHANGELOG.md` `[Unreleased]` directly, drop a `CHANGELOG.d/<num>.<type>.md` file with one bullet (no leading dash; assembler adds it). `<type>` ∈ {added, changed, fixed, removed}. Eliminates the rebase-conflict source that bit every concurrent-PR pair pre-v0.55.

## Repo-specific test gates

These fail when changes elsewhere don't update them in lockstep:

- **New exported `*Store` method (`db.go`):** classify in `readerRoutedStoreMethods` or `writerRoutedStoreMethods` (`db_test.go`), or `TestStore_AllExportedMethodsClassified` fails.
- **Schema migration changes:** bump `schema_version` in 5 corpus snapshot files. `make corpus-snapshot-update` regenerates them; on Windows where `make` may be unavailable, `sed -i 's/"schema_version": N/"schema_version": N+1/' testdata/corpus/*.snapshot.json`.
- **Tool-contract changes (descriptions, InputSchema):** regenerate via `go test ./internal/server -run TestToolContract -update-tool-contract`.
- **Dashboard HTML/CSS changes (`dashboard.go`):** regenerate via `go test ./internal/server -run 'TestDashboardHTMLSnapshot|TestDashboardCSS' -count=1 -update-dashboard-snapshot -update-dashboard-css-snapshot`.
- **New language extractor:** update `ast/registry.go` self-registration AND `db/corpus.go` `ClassifyCorpus` AND the v9 SQL trigger WHERE clauses. `TestClassifyCorpus_MatchesSQLTriggerRouting` is the gate.
- **Bounded-duplication advisories (CLI ↔ MCP doctor):** when adding a doctor advisory, ship the helper in BOTH `internal/server/admin.go` and `cmd/pinch/doctor.go` with a "mirrors X / must stay identical" comment. The CLI lives in package main and can't import the server package.

## JSON response invariants

- **All slice fields in tool responses must be allocated as `[]T{}`, never `var x []T`.** A nil slice marshals to `null`; consumers iterating without a null-check break. Six bugs of this class fixed in v0.9.0 (#328/#330/#332/#334/#338/#330). The pattern keeps recurring.
- Map fields are fine — `make(map[K]V)` is non-nil.
- Informal lint: `grep -n "var \w\+ \[\]map\[string\]" internal/server/` should return nothing once a handler is response-stable.

## Idioms

- **Logging:** `slog` everywhere. `log.Printf` will silence under bench `TestMain` and corrupt baselines.
- **Reader pool:** pure SELECT methods use `s.ro.Query`/`s.ro.QueryContext`; writes use `s.db.Exec`. Routing is enforced by the classification gate.
- **Symbol IDs:** always build via `db.MakeSymbolID(file, qn, kind)`. Never string-concat.
- **`InputSchema: json.RawMessage(\`...\`)` raw-string gotcha:** backticks inside the description terminate the string. Use plain text or rewrite without — bit #293 and #302.

## Build & Test

Build, test, snapshot, and bench runbook lives in [`CONTRIBUTING.md` → Build & Test reference](CONTRIBUTING.md#build--test-reference). Includes the `make build` / `make install` swap-binary flow, single-test syntax, `make corpus-test` + `make corpus-bench` policies, and the bench-baseline refresh procedure.

**After any schema change** rebuild `pincher` (or `pincher.exe`) and reconnect via `/mcp` so the running MCP picks up the new schema.

## Architecture

### Data flow

```
cmd/pinch/main.go          ← sole entry point (MCP server + optional HTTP + `pincher index` CLI)
  → db.Open()              open/migrate SQLite
  → index.New()            create indexer (holds *db.Store)
  → server.New()           create MCP server (holds *db.Store + *index.Indexer)
  → srv.StartSessionFlusher()  flush session stats to DB every 10s
  → idx.Watch()            poll projects for file changes
  → [--http :PORT]         optional REST gateway
  → mcp.StdioTransport     JSON-RPC 2.0 over stdin/stdout
```

### Three-layer storage (single `symbols` table serves all three)

| Layer | Mechanism | Query path |
|---|---|---|
| 1 — Byte-offset retrieval | `start_byte` / `end_byte` per symbol | `GetSymbol` → `ReadSymbolSource` = 1 SQL + 1 `os.File.Seek` + 1 `Read` |
| 2 — Knowledge graph | `symbols` rows + `edges` table | pinchQL → SQL via `cypher/engine.go` |
| 3 — FTS5 full-text search | `symbols_fts` virtual table + 3 triggers | `SearchSymbols` via BM25 |

All three populated in a single `ast.Extract()` call per file during indexing.

### Package responsibilities

- **`internal/db/db.go`** — SQLite store. Schema lives here as a `schema` const. Migrations in `schemaMigrations` slice — append to add. Current schema: **v38**. `symSelectFrom` is the canonical SELECT column list — update it and all scan functions together when adding columns. See `docs/reference/architecture.md` for the per-version migration history; this line was 7 versions stale before #998/#999 caught it (and 2 versions stale again before #1418 added a parity test).

- **`internal/db/corpus.go`** — `ClassifyCorpus(language, kind)` returns `code` / `config` / `docs`. **PARITY INVARIANT:** Go function and v9 SQL trigger WHERE clauses encode the same routing. `TestClassifyCorpus_MatchesSQLTriggerRouting` is the gate.

- **`internal/ast/extractor.go`** — Multi-language symbol extraction. Parser-backed AST (1.0): Go, YAML/JSON, HCL/Terraform, TOML, Bash, Markdown, Jinja2, Python (dispatcher upgrades from 0.85 → 1.0 when `python` is on PATH), JavaScript/JSX (dispatcher upgrades from 0.85 → 1.0 when the AST extractor parses successfully; default-on since v0.20.0 per #266, label-drift in registry/health/docs fixed in #1328). Stable regex (0.85): TS/TSX, Rust, Java, Swift, Kotlin, C#, PHP, C, C++ (Swift/Kotlin/C#/PHP/C/C++ all promoted in v0.73 — #1450/#1457/#1459/#1461/#1463 — covering modern type kinds, attribute/annotation prefix tolerance, modifier coverage). Approximate regex (0.70): Ruby, Scala, Lua, Zig, Elixir, Dart, R (the last six promoted from stub in v0.63 per #1161/#1186/#1187). Stub (0.0): Haskell (indentation-sensitive layout requires harder regex representation; tracked separately). The shared `regexExtractor` framework supports `scopeRE` — a syntactic-grouping container that scopes inner symbols' Parent without emitting its own Class symbol (Rust `impl` / Swift `extension`, v0.67 #1183 partial).

- **`internal/ast/registry.go`** — `Extractor` interface + per-language registry. Each extractor self-registers in `init()`.

- **`internal/ast/blocklist.go`** — `ShouldSkip(path)` filters lockfiles, minified bundles, source maps before extraction. Belt-and-suspenders relative to `gocodewalker`'s `.gitignore` respect.

- **`internal/cypher/engine.go`** — pinchQL-to-SQL translation. `tokenize` → `parseQuery` → `run`. Three paths: `runNodeScan` (no edge), `runJoinQuery` (single-hop SQL JOIN), `runBFS` (variable-length Go BFS). `symRow` and SELECT queries must stay in sync with `db.go`'s `Symbol`.

- **`internal/index/indexer.go`** — Indexing pipeline. Concurrent per-file goroutines, xxh3 hash skip, batch flush. Per-file `DeleteSymbolsForFile` before re-extraction. Per-project mutex + cross-process `acquireProjectLock`. Tail GC pass (#326): files no longer on disk get their symbols + file_hash row pruned. After `wg.Wait`, `resolveImports` / `resolveCalls` / `resolveReads` run project-wide for cross-file Go edges. `Watch()` polls 2s active / 30s idle.

- **`internal/index/lockfile.go`** — Cross-process project lockfile with PID liveness + 24h stale reclaim.

- **`internal/index/bloat_trap.go`** — `IsBloatTrap(absPath, hookMode)` refuses fs root and `$HOME`; in hook mode also requires a project marker (`.git`, `go.mod`, `package.json`, etc.). Lives in `internal/index` (moved from `cmd/pinch` in #790) so both the CLI entry point AND the MCP `index` tool handler share the guard.

- **`internal/server/server.go`** — MCP server + HTTP REST gateway. All tools registered in `registerTools()`. Every handler calls `jsonResultWithMeta()` which wraps result in `_meta` and atomically increments session stats. `StartSessionFlusher` flushes every 10s. `cypher.Executor` is initialised with `ProjectID` so all query paths are scoped.

### Symbol ID format

```
"{file_path}::{qualified_name}#{kind}"
e.g. "internal/db/db.go::db.Open#Function"
```

IDs are stable across re-indexing as long as file path and qualified name don't change. Built by `db.MakeSymbolID()`. File moves resolve via `symbol_moves` table.

### Schema migration pattern

1. Append a SQL string to `schemaMigrations` in `db.go`.
2. Update the `Symbol` struct field, `symSelectFrom` const, and all scan functions (`scanOneSymbol`, `scanSymbolRowsRow`, `scanSymbolRow`) together.
3. Update `cypher/engine.go`'s `symRow` struct and SELECT queries.
4. Update `ast/extractor.go`'s `ExtractedSymbol` and `indexer.go`'s symbol construction if the field originates in extraction.
5. Bump `schema_version` in 5 corpus snapshot files.

### Key invariants

- `db.SetMaxOpenConns(1)` — SQLite is single-writer; writes serialize at the connection pool level.
- WAL mode + `_busy_timeout=5000` — readers don't block writers.
- WAL bounding: `journal_size_limit=256 MiB` plus `CheckpointTruncate()` at every `Index()` tail. (`wal_autocheckpoint=100` was tried and reverted — 14.5× slowdown on heavy single-writer indexing.)
- Cross-process project lock serializes concurrent indexers.
- Stale-symbol cleanup on every per-file goroutine; tail-pass GC for files removed from disk (#326).
- Go cross-file resolution scoped to confidence-1.0 extractors; regex-extracted languages keep per-file resolution.
- FTS5 triggers auto-sync the virtual tables; never sync manually.
- `flushBuffers` fires at 500 symbols or 1000 edges to bound memory.
- Symlink safety relies on `gocodewalker`'s default (v1.5.1, audited #41 item 3): symlinks are reported as paths, NOT recursed. Pinned by `internal/index/symlink_safety_test.go`.

## Dependencies

- `github.com/modelcontextprotocol/go-sdk v1.4.0` — MCP framework
- `modernc.org/sqlite` — pure-Go SQLite (no CGO)
- `github.com/boyter/gocodewalker` — `.gitignore`-respecting walker
- `github.com/zeebo/xxh3` — fast content hashing
- `gopkg.in/yaml.v3`, `github.com/BurntSushi/toml`, `github.com/hashicorp/hcl/v2`, `mvdan.cc/sh/v3`, `github.com/yuin/goldmark`, `github.com/nikolalohinski/gonja` — language parsers
- `github.com/tiktoken-go/tokenizer` — cl100k_base BPE for token-savings accounting

## Known Architectural Limitations

- **Regex gap:** ~13 non-Go languages still regex-extract (~80% accuracy). Tracked in #266 (JS AST), #268 (multi-language AST roadmap).
- **YAML/JSON sequence-rename ID instability** (#205, won't-fix for v0.7.0): inserting at index 0 renames every downstream symbol's QN. Workaround: search by name rather than id. Full content-hash-ID fix is v0.8/v1.1+ territory.
- **Single-user SQLite:** symbols + sessions are local-only. Team mode would need a server with shared DB or PostgreSQL backend.
- **HTTP auth:** `--http` supports optional `--http-key <token>` bearer auth. Without it, the API is open — front behind a reverse proxy or set the key for production.
- **`symbols` batch cap:** `maxBatchSymbols = 100`.
