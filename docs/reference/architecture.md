# Architecture & internals

[Back to reference index](README.md)

## Architecture

### Two-process architecture

```
  Claude Code (IDE)
        │
        │ JSON-RPC 2.0 (stdio)
        ▼
┌───────────────────────┐          ┌───────────────────────────┐
│  pincher (MCP process)│          │  pincher --http 127.0.0.1:8080 │
│                       │          │  (dashboard / REST)       │
│  • 29 MCP tools       │          │                           │
│  • idx.Watch()        │          │  • POST /v1/{tool}        │
│  • SessionFlusher     │          │  • GET /v1/dashboard      │
│    (flush every 10 s) │          │  • GET /v1/openapi.json   │
│                       │          │  • GET /v1/sessions       │
│                       │          │  • GET /v1/tool-calls     │
│                       │          │  • DELETE /v1/projects    │
└──────────┬────────────┘          └───────────┬───────────────┘
           │                                   │
           │     Both share the same SQLite file
           └─────────────┬─────────────────────┘
                         ▼
             ┌─────────────────────┐
             │  SQLite WAL         │
             │  pincher.db         │
             │                     │
             │  • symbols          │
             │  • edges            │
             │  • symbols_fts +    │
             │    per-corpus FTS5  │
             │  • projects         │
             │  • sessions         │
             │  • symbol_moves     │
             │  • adr_entries      │
             │  • schema_version   │
             └─────────────────────┘
```

The HTTP process retries port binding for up to 10 seconds on startup — reconnecting the MCP process (which briefly holds the port) doesn't break the dashboard. `pincher web` discovers the bound URL via the `sessions.http_url` column added in schema v11; PID liveness check covers stale rows.

### Three-layer storage

All three layers populate in **one AST parse pass** from one `symbols` row.

```
                         Source File
                              │
                         ast.Extract()
                              │
               ┌──────────────┴──────────────┐
               │         symbols row         │
               │  id · file_path · name      │
               │  start_byte · end_byte      │
               │  kind · language · parent   │
               │  signature · docstring      │
               │  complexity · is_exported   │
               │  extraction_confidence      │
               └──────┬──────────┬───────────┘
                      │          │
          ┌───────────┘          └──────────────┐
          ▼                                     ▼
  ┌───────────────┐    ┌──────────────┐   ┌────────────────────┐
  │  Layer 1      │    │  Layer 2     │   │  Layer 3 — FTS5    │
  │  Byte-Offset  │    │  Knowledge   │   │  BM25 full-text    │
  │  Symbol Store │    │  Graph       │   │                    │
  │               │    │              │   │  symbols_fts       │
  │  start_byte   │    │  symbols +   │   │   (legacy/all)     │
  │  end_byte     │    │  edges table │   │  symbols_code_fts  │
  │               │    │              │   │  symbols_config_fts│
  │  Retrieval:   │    │  Queries:    │   │  symbols_docs_fts  │
  │  1 SQL +      │    │  node scan   │   │                    │
  │  1 os.Seek +  │    │  JOIN (1-hop)│   │  BM25 across name +│
  │  1 os.Read    │    │  BFS (n-hop) │   │  signature +       │
  │               │    │  via CTE     │   │  docstring; corpus=│
  │  O(1), <1ms   │    │  <2ms        │   │  routes per index  │
  └───────────────┘    └──────────────┘   └────────────────────┘
```

**Per-corpus FTS5** (#32 ✅): one symbol → one corpus. Routing rules: `language IN ('YAML','JSON','HCL','TOML')` → config; `Markdown` or `kind=Document` → docs; everything else → code. The `search` tool's `corpus` parameter routes to the right index. **Default is `code`** — the most common search is for an identifier. Pass `corpus=config` for YAML/JSON/HCL/TOML settings, `corpus=docs` for Markdown / fetched Documents, or `corpus=all` to hit the legacy mixed index (deprecated, slated for removal).

**Per-symbol confidence** (#34 ✅): `extraction_confidence` is composed from BaseExtractor + KindBaseline + PathPenalty + IdentBonus + GeneratedPen, clamped to `[0, 1]`. Lockfile keys score ~0.4–0.6, vendored Go ~0.7, real config ~0.95–1.0. `search` accepts `min_confidence` and **defaults to 0.7**. Every search response carries `_meta.confidence_distribution` (4-bucket histogram).

### pinchQL query routing

```
  MATCH (n) WHERE ...              →  runNodeScan
  (no edge pattern)                   Simple SELECT + WHERE
                                       Sub-ms on indexed columns

  MATCH (a)-[:CALLS]->(b) WHERE   →  runJoinQuery
  (single-hop, fixed edge kind)       Single SQL JOIN
                                       Sub-ms via idx_edge_from/to

  MATCH (a)-[:CALLS*1..3]->(b)    →  runBFS
  (variable-length path)              Go BFS loop over CTE
                                       Bounded by depth + MaxRows
                                       <5ms at depth 3
```

Project-scoped paths — `search`, `symbol`/`symbols` when `project=` is passed, `query`, `trace`, `changes` — apply a `project_id` filter at lookup and BFS traversal time, so cross-project data is structurally inaccessible from those paths.

### Data flow: index to query

```
  pincher index path="/my/repo"
        │
        ▼
  index.Index()
   ├── Walk files (gocodewalker, respects .gitignore)
   ├── Hash each file (xxh3, skip if unchanged)
   ├── ast.Extract(source, language, relPath)
   │    ├── Go:    go/ast → exact byte offsets, confidence=1.0
   │    └── Other: regex  → approximate offsets, confidence=0.70–0.85
   ├── Batch upsert symbols (500/batch)
   ├── Batch upsert edges (1000/batch)
   └── FTS5 triggers auto-sync symbols_fts + per-corpus

  idx.Watch() polls every 2 s (active) or 30 s (idle)
  and re-runs Index() on changed files incrementally.
  No manual re-index required during a session.

  On file move: (qualified_name, kind) match detected →
  symbol_moves redirect recorded → handleSymbol resolves
  stale IDs transparently via store.ResolveStaleID()
```

---

## Schema

Schema is versioned via the `schema_version` table. Current version: **v38**. Migrations apply automatically on startup — no data loss, no manual steps. To add a migration: append a SQL string to `schemaMigrations` in `db.go`; the version number is auto-derived from the slice length.

Migration history:

| Version | Summary |
|---|---|
| v1 | Baseline: projects, symbols, edges, symbols_fts |
| v1→v2 | `extraction_confidence` column on symbols |
| v2→v3 | `symbol_moves` + `idx_sym_qnkind` (file rename detection) |
| v3→v4 | `sessions` table for ROI tracking |
| v4→v5 | (slot reserved during refactor; no DDL) |
| v5→v6 | Generated `symbol_id` column for FTS5 external-content lookups |
| v6→v7 | `extraction_failures` table for `pincher doctor` |
| v7→v8 | `slow_queries` table (`--slow-query-ms` capture) |
| v8→v9 | Per-corpus FTS5 split — `symbols_{code,config,docs}_fts` + routing triggers |
| v9→v10 | TOML routed to the config corpus (DROP/CREATE triggers) |
| v10→v11 | `http_url` + `http_pid` columns on sessions for `pincher web` discovery |
| v11→v12 | Remove the legacy `symbols_fts` virtual table and pre-corpus triggers |
| v12→v13 | Route HTML to the docs corpus alongside Markdown |
| v13→v14 | Route XML to the config corpus alongside YAML/JSON/HCL/TOML |
| v14→v15 | `projects.schema_version_at_index` (#236) — drift detection |
| v15→v16 | Per-language call counts on sessions (#240) |
| v16→v17 | Query-failure / retry-rate counters on sessions (#241) |
| v17→v18 | `projects.binary_version` — captures producing binary identity |
| v18→v19 | `pending_edges` — persisted per-file deferred edge resolution |
| v19→v20 | `edges.source` — tag each row with its origin (resolver / extractor / closure) |
| v20→v21 | `celebrations` — one-shot record of cumulative milestones |
| v21→v22 | Receiver-type tracking for Go method calls (#423) |
| v22→v23 | `interface_methods` table — Go interface method names |
| v23→v24 | `hook_invocations` telemetry (#626) |
| v24→v25 | Closure table — materialized transitive closure of the call graph |
| v25→v26 | `pending_edges.base_type` — Go READS candidate disambiguation |
| v26→v27 | `session_tool_calls` — per-call event log feeding the dashboard's per-tool panels (#1191) |
| v27→v28 | Composite PRIMARY KEY `(project_id, id)` on `symbols` — prep for cross-project ID collision safety |
| v28→v29 | `bench_runs` + `bench_results` tables — persistence for `pincher bench --persist` (#1263) |
| v29→v30 | `closure.via_kind` — record the last-hop edge kind to support kind-aware closure traversals (#685 phase 2) |
| v30→v31 | `branch` column on `symbols` / `edges` / `files` / `pending_edges` — multi-branch coexistence foundation (#1303 Phase 1) |
| v31→v32 | `projects.current_branch` — git branch the project was last indexed against (#1303 Phase 2a). Doctor surfaces a branch-drift advisory when the on-disk branch differs. Wire format JSON tag is `last_indexed_branch` (#1388). |
| v32→v33 | `extraction_failures.binary_version_at_failure` — pincher binary version that recorded the row. Doctor surfaces the value so readers can distinguish "fixed-since-this-binary" rows from "still recurring on the running binary" without cross-referencing CHANGELOG by hand (#1421). |
| v33→v34 | `sessions.queries_zero_expected` + `queries_zero_unexpected` — split `queries_zero_result` into audit-shape (pinchQL with a property predicate, empty rows are healthy) vs caller-surprised (search / trace / neighborhood, empty rows are usage-killers). New `zero_unexpected_rate` is the actionable metric — the rate at which pincher returns empty when the agent expected results. Closes #1494 half 1 / #1632. |
| v34→v35 | `idx_edge_from_project_kind_to` + `idx_edge_to_project_kind_from` — covering edge traversal indexes for outbound/inbound pinchQL BFS. Keeps project and kind filtering inside the endpoint lookup so multi-project stores with colliding symbol IDs do less read amplification. |
| v35→v36 | `projects.index_state` + `index_started_at` — project-level crash/OOM recovery marker. The indexer marks a project `running` before file mutation starts and clears it only after a successful pass; the next index forces re-extraction if a prior run died mid-pass. |
| v36→v37 | `idx_edge_project_to` — project-scoped inbound edge grouping index for `hotspot`, avoiding a temp grouping B-tree when ranking the most-called symbols. |
| v37→v38 | Trace endpoint planner hardening — supplemental project-scoped endpoint indexes plus endpoint-first `INDEXED BY` hints so recursive trace CTEs seek by the current frontier endpoint instead of scanning project/kind edge buckets on large graphs. |

---

## Key invariants

- `SetMaxOpenConns(1)` — SQLite is single-writer; all writes serialize at the pool.
- WAL mode — readers never block writers; 5 s busy timeout prevents immediate failure during indexing.
- `journal_size_limit=256 MiB` + `wal_checkpoint(TRUNCATE)` at every `Index()` tail — keeps the WAL bounded under heavy churn.
- Cross-process project lockfile — multiple pincher binaries on one data directory serialize safely; stale-holder reclaim covers crashed processes.
- File re-parse always deletes the file's prior symbols before re-extraction — no stale rows leak; cascades to edges with either endpoint in the file.
- FTS5 triggers (`sym_fts_insert`, `sym_fts_delete`, `sym_fts_update`, plus the v9 corpus-routed variants) auto-sync — never manually sync.
- Generated `symbol_id` column on `symbols` mirrors `id` so FTS5 content lookups against the FTS column name work; never write to `symbol_id` directly.
- `symSelectFrom` and `symRow` (in `cypher/engine.go`) must stay in sync when adding columns.
- Batch flush at 500 symbols or 1,000 edges to bound memory on large repos.
- ClassifyCorpus parity gate — the Go classifier and the v9 SQL trigger WHERE clauses encode the same rules; `TestClassifyCorpus_MatchesSQLTriggerRouting` is the regression test that catches drift.

---

## Project layout

```
pincherMCP/
├── cmd/pinch/
│   ├── main.go                   # Sole entry point: MCP server + subcommand dispatch
│   ├── doctor.go                 # `pincher doctor` subcommand
│   ├── rebuild_fts.go            # `pincher rebuild-fts` subcommand
│   ├── selftest.go               # `pincher self-test` subcommand
│   ├── update.go                 # `pincher update` subcommand
│   ├── web.go                    # `pincher web` subcommand
│   ├── web_unix.go / web_windows.go  # detached-spawn helpers per OS
│   ├── init.go                   # `pincher init` subcommand
│   └── policy.md                 # Embedded policy block written by `pincher init`
├── internal/
│   ├── db/db.go                  # SQLite store: schema, migrations, all CRUD,
│   │                             # FTS5 ops (legacy + per-corpus), graph ops,
│   │                             # BPE token counting, WAL guardrails
│   ├── db/corpus.go              # ClassifyCorpus(language, kind) → code/config/docs
│   ├── ast/                      # Multi-language extraction
│   │   ├── extractor.go          # Per-language registry, byte offsets, confidence
│   │   ├── yaml.go / hcl.go / bash.go / markdown.go / toml.go / jinja2.go / sql.go / makefile.go
│   │   ├── blocklist.go          # Lockfile / minified / source-map filter
│   │   └── confidence.go         # Per-symbol confidence composition
│   ├── cypher/engine.go          # Cypher → SQL: tokenizer → parser → 3 query paths
│   ├── index/
│   │   ├── indexer.go            # Walk → hash → extract → resolve → store → watch
│   │   ├── bloat_trap.go         # IsBloatTrap: refuse filesystem root + $HOME;
│   │   │                         # hook mode also requires a project marker
│   │   └── lockfile.go           # Cross-process project lockfile w/ stale reclaim
│   └── server/server.go          # 29 MCP tools, HTTP REST, gzip, OpenAPI 3.1, bearer auth,
│                                 # basepath / reverse-proxy support, sessions persistence
└── go.mod
```

---

## Performance

Measured on this codebase (13 files, 618 symbols, 5,785 edges, Windows 11, SQLite WAL):

| Operation | Measured time | Notes |
|---|---|---|
| Cold index (13 files) | ~190 ms | Concurrent goroutines, xxh3 hash |
| Incremental re-index (0 changes) | <5 ms | All files skipped via hash |
| `architecture` | 12 ms server / 69 ms HTTP | Was 10 s+ before savings-calc fix |
| `search` | 1 ms | BM25 via FTS5 |
| `health` | 1 ms | |
| `stats` | 8 ms | |
| `symbol` (byte-offset seek) | <1 ms | 1 SQL + 1 seek + 1 read |
| Single-hop pinchQL query | 2 ms | SQL JOIN |
| BFS depth 3 | <5 ms | Go BFS over CTE |
| Session stats flush | every 10 s | Background goroutine |

**SQLite configuration:** WAL mode, `busy_timeout=5000ms`, `SetMaxOpenConns(1)` (serialized single-writer). Readers never block writers in WAL mode. Reader pool (`--db-readers`, default 4, capped at 32) fans concurrent reads across `mode=ro` connections.

**WAL bounding:** `journal_size_limit=256 MiB` caps the WAL; `PRAGMA wal_checkpoint(TRUNCATE)` runs at the tail of each `Index()` run to fold the WAL back into the main DB at the natural quiet point. `PRAGMA optimize` runs on the same cadence. These are the WAL guardrails added after the 70 GB WAL incident produced by an unbounded multi-writer storm — the bound holds even under heavy churn.

**Watch backoff:** the file-change watcher's 5-second tick body short-circuits when any `Index()` is in flight for any project. During large catch-up phases the watcher idles at near-zero CPU instead of bouncing repeatedly off the per-project mutex.

**Pinned-corpus benchmarks:** `make bench` runs per-corpus benchmarks at `-benchtime=2s -benchmem` against `testdata/corpus/{go-project,k8s-ops,node-monorepo,docs-site}`. CI gate compares against committed baselines and fails on `ns/op +20%` or `allocs/op +30%` regressions.

---

## Test coverage

```bash
go test ./...                                              # run all tests
go test ./... -coverprofile=cover.out                      # with coverage
go tool cover -func=cover.out | grep "^total"              # total: 84.3%
go test ./internal/db/ -run TestGraphStats_WithData -v     # single test
go test ./internal/server/ -v                              # server package
```

Current coverage by package:

| Package | Coverage |
|---|---|
| `internal/cypher` | 94.2% |
| `internal/ast` | 89.9% |
| `internal/server` | 89.1% |
| `internal/index` | 84.1% |
| `internal/db` | 84.1% |
| **total** | **84.3%** |

`internal/db` and `internal/index` set the floor — both have OS / SQLite / network code that resists pure unit testing (`ListenAndServeHTTP`, `handleFetch`, `extractTextFromHTML`, MCP `onInit`/`onRoots`/`detectRoot` callbacks, file-system race paths in the watcher). The CI gate is set to **84%**.

---

## Dependencies

| Dependency | Purpose |
|---|---|
| `github.com/modelcontextprotocol/go-sdk v1.4.0` | MCP server (JSON-RPC 2.0 over stdio) |
| `modernc.org/sqlite` | Pure-Go SQLite (no CGO) |
| `github.com/tiktoken-go/tokenizer` | cl100k_base BPE tokenizer — real token counts |
| `github.com/boyter/gocodewalker` | File walker that respects `.gitignore` |
| `github.com/zeebo/xxh3` | Fast content hashing for incremental indexing |
| `gopkg.in/yaml.v3` | YAML/JSON Node tree parsing |
| `github.com/BurntSushi/toml` | TOML parseability gate |
| `github.com/hashicorp/hcl/v2` | HCL/Terraform parser |
| `mvdan.cc/sh/v3` | Bash parser (the `shfmt` parser) |
| `github.com/yuin/goldmark` | Markdown CommonMark parser |
| `github.com/nikolalohinski/gonja` | Jinja2 parser |
