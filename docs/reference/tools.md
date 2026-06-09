# The 29 MCP tools

[Back to reference index](README.md)

All latencies measured on this codebase. Token counts use cl100k_base BPE — the same tokenizer family as Claude.

Tool sections use stable explicit anchors: `#tool-<name>`, where `<name>` is the lowercase MCP tool name. These anchors are the pre-staged target for future `pincher://tools/<name>/runbook` Resource URIs.

## Starter

### `guide` {#tool-guide}

Free-form task description (`"fix login retry bug"`, `"refactor auth middleware"`) returns 2–3 recommended pincher tool calls with reasoning. Removes decision friction at session start. Keyword classifier; no model.

Tested latency: <1 ms.

## Indexing & discovery

### `index` {#tool-index}

Index or re-index a repo. One AST pass populates all three layers. xxh3 content-hash skips unchanged files. Concurrent per-file goroutines.

Tested latency: 190 ms (3 changed, 10 skipped).

### `list` {#tool-list}

All indexed projects with file/symbol/edge counts and last-indexed timestamp.

Tested latency: <1 ms.

### `changes` {#tool-changes}

`git diff` → affected symbols → BFS blast radius. Returns changed symbols + impacted callers with CRITICAL/HIGH/MEDIUM/LOW risk labels. Scope: `unstaged` (default), `staged`, `all`.

Tested latency: ~5 ms.

## Symbol retrieval

### `symbol` {#tool-symbol}

Source for one symbol by stable ID. O(1): 1 SQL + 1 `os.Seek` + 1 `os.Read`. No re-parse. Supports `fields` projection.

Token savings: file size minus symbol size (real BPE).

### `symbols` {#tool-symbols}

Batch retrieve up to **100** symbols in one call. Hard cap: requests >100 IDs are rejected. Always prefer this over calling `symbol` in a loop.

Token savings: same per symbol.

### `context` {#tool-context}

Symbol + all direct callees in one call. The preferred tool for understanding a function.

Token savings: ~90% vs reading files.

## Search & graph

### `search` {#tool-search}

FTS5 BM25 across names, signatures, docstrings. Wildcards (`auth*`), phrases (`"process order"`), AND/OR. `kind`/`language`/`corpus` filters. `corpus` defaults to `code`; pass `config` for YAML/JSON/HCL settings, `docs` for Markdown / Documents. The legacy `all` value was removed in v0.5; older callers passing it get soft-redirected to `code` with a deprecation log line. `fields` projects columns. `project=*` searches all repos.

Tested latency: 1 ms.

### `query` {#tool-query}

pinchQL graph queries — Cypher-shaped subset. Three SQL paths: node scan, single-hop JOIN, variable-length BFS. `max_rows` defaults to 200 and caps at 10000. Parameter: `pinchql`; legacy alias `cypher` is accepted for one release.

Tested latency: 2 ms (single-hop).

### `trace` {#tool-trace}

BFS call-path trace — who calls this, or what does it call. Grouped by depth. Risk labels: CRITICAL (depth 1) → LOW (depth 4+).

Tested latency: <5 ms (depth 3).

### `context_for_task` {#tool-context_for_task}

Composite: one call replaces 5-10 atomic calls during investigation. Takes either `task` (free-form) or `seed_id`. Composes `search` → `context` → `trace direction=both` → `changes` overlap into one envelope `{seeds, neighbors, callers, callees, recent_changes}`. `max_seeds` defaults to 3 and caps at 10; `trace_depth` defaults to 2 and caps at 4; `include_changes` defaults true. Use when starting an investigation; use the atomic tools for follow-up. (#1259 v0.71)

Tested latency: ~20-80 ms (≈ Σ atomic-call latencies).

### `investigate_failure` {#tool-investigate_failure}

**Composite #1 of Phase 4 (#1391 v0.81).** The bug-hunt loop in one call. Takes `error_text` (raw stack trace / panic / exception), parses identifier-shaped frame tokens via a stopword-aware heuristic, BM25-searches each across the code corpus, and ranks suspects by a weighted sum of evidence: stack-frame match (+0.45), multi-frame match (+0.10), file-appears-in-trace (+0.20), modified-in-working-tree (+0.20), caller fan-in (log-scaled +0.05). Returns `{implicated_symbols, callers, recent_changes, rank, frames_parsed}` with per-suspect `evidence` enumerating which signals fired. Replaces the typical 5-call bug-hunt sequence. Stamps `_meta.empty_reason` when no frames parse or no suspects resolve.

Tested latency: ~40-150 ms (BM25 × N frames + trace × maxSuspects).

### `plan_change` {#tool-plan_change}

**Composite #2 of Phase 4 (#1391 v0.82).** Pre-edit blast-radius composite. Takes `target` (file path, symbol id, or free-form name). Resolves to one or more affected callable symbols, traces inbound callers at depth 1 (CRITICAL) and depth 2 (HIGH), partitions them by package boundary + test-file status, and looks up ADRs whose key/value mentions the target's package, directory, or symbol name. Returns `{target, blast_radius, related_adrs}` with `blast_radius.summary` counts and `blast_radius.test_files_intersecting` (the test files you should run before pushing). Emits `_meta.warnings_v2.blast_radius_high` when depth-1 caller count exceeds 14 (suggests staged refactor). Replaces the typical 4-call pre-edit sequence (`changes` → `trace direction=in` → `context` → `adr list`).

Tested latency: ~20-100 ms (resolve + trace × affected symbols + ADR overlap).

## Architecture & knowledge

### `architecture` {#tool-architecture}

Language breakdown, entry points, hotspot functions, graph stats, surprising connections (rarest cross-package CALLS pairs). Start here on any unfamiliar project.

Tested latency: 12 ms.

### `branch_overlap` {#tool-branch_overlap}

Diffs two in-flight branches against their shared merge-base, maps changed files to symbols, intersects the sets. Returns `overlapping_files`, `overlapping_symbols`, and a merge-order-risk `verdict`.

Tested latency: ~10 ms.

### `schema` {#tool-schema}

Node kind counts, edge kind counts, totals. Use before `query` to see what's indexed.

Tested latency: 1 ms.

### `adr` {#tool-adr}

Persistent key/value store per project. Survives context resets and binary upgrades. Actions: `get`, `set`, `list`, `delete`.

Tested latency: <1 ms.

### `health` {#tool-health}

Schema version, index staleness, per-language extraction coverage. Detects stale indexes.

Tested latency: 1 ms.

### `stats` {#tool-stats}

Session savings as a formatted CLI summary. Persists across reconnects.

Tested latency: 8 ms.

### `fetch` {#tool-fetch}

Fetch a URL, extract its text, store as a searchable `Document` symbol in the project knowledge base. Body cap: 512 KB fetched, 32 KB stored. Retrieve via `search kind:Document` or `symbol`.

Tested latency: ~200 ms (network).

## Code audit & admin

These tools were restored to MCP in v0.52 (reversal of the v0.42 #624 split). All read-only except `init` (writes per-target config), `rebuild_fts` (rebuilds the FTS5 virtual tables), and `index` (listed above).

### `dead_code` {#tool-dead_code}

Symbols with zero inbound CALLS / READS / WRITES / REFERENCES / IMPORTS edges. Defaults bias toward precision: `language=Go`, `kinds=Function,Method`, `min_confidence=0.95`. Test fixtures filtered.

Notes: the inverse of `architecture` hotspots.

### `audit_unused` {#tool-audit_unused}

**Composite #3 of Phase 4 (#1391 v0.83).** Dead-code composite with deep-trace confirmation. Runs the existing `dead_code` SQL path then, per candidate, fires a scoped inbound CALLS trace at `confirm_depth` (default 2) and classifies the result: `high` (zero callers — safe to delete), `medium` (deeper callers — likely dynamic path the static graph missed, read before deleting), `low` (depth-1 caller — almost always a resolver bug, file an issue rather than delete). Returns `{candidates, summary}` with classification counts. Replaces the N+1 round trips of `dead_code` + per-candidate `trace direction=in`.

Notes: read-only. ~50-300 ms (dead_code + trace × candidates).

### `onboard_module` {#tool-onboard_module}

**Composite #4 of Phase 4 (#1391 v0.84).** New-contributor orientation. Takes `directory` (relative path inside the project, e.g. `internal/auth/`). Enumerates every symbol in scope, identifies entry points + the exported surface, computes language breakdown + test-to-code ratio, partitions CALLS edges into `external_dependencies` (outbound boundary — what the module depends on) and `external_consumers` (inbound boundary — what depends on it). Returns `{scope, entry_points_local_to_scope, external_dependencies, external_consumers, module_summary}`. Replaces the typical 5-10 call orientation sequence (`architecture` + `search file_pattern=path/**` + `trace direction=out` × N + `context` × N).

Notes: read-only. ~30-150 ms (scope scan + edges scan, both indexed).

### `why_empty` {#tool-why_empty}

**Composite #5 of Phase 4 (#1391 v0.85).** Empty-result recovery composite. Takes `prior_empty_reason` (the `_meta.empty_reason` value from a previous empty response). Returns the structured catalog entry: `{title, when_it_fires, recovery_action, recovery_steps}`. Stateless catalog lookup — no DB query, no project scope. Replaces the read-the-docs + try-each-probe loop with one round-trip. Source of truth: [`docs/empty-reasons.md`](../empty-reasons.md).

Notes: read-only. Sub-ms (in-memory map lookup).

### `neighborhood` {#tool-neighborhood}

Same-file siblings of a seed symbol, paginated. **NOT graph adjacency** — name is preserved for compat (#498); use `trace direction=both` for graph adjacency.

Notes: useful for in-file refactor planning.

### `init` {#tool-init}

Write CLAUDE.md / `.claude/config.json` / Cursor rules / Codex AGENTS.md / Goose Open Plugins hooks / etc. — preflight (diff_preview) or `apply=true`. Supports multiple targets via `target=<name>` or `target=all`, including `goose` for a project-scoped `.agents/plugins/pincher/` hook extension. Codex AGENTS.md always lives in `~/.codex/AGENTS.md` and emits a `skipped_always_global` entry when `target=all` is used in a project context.

Notes: per-target `{target, path, action, diff_preview, bytes_in, bytes_out}`. Codex emits `{target, action: "skipped_always_global", reason}`.

### `doctor` {#tool-doctor}

Schema version, DB + WAL sizes, per-project staleness, recent extraction failures, recent slow queries, advisories (ghost-extraction, DB bloat).

Notes: same data as `pincher doctor --json`.

### `rebuild_fts` {#tool-rebuild_fts}

Drop + repopulate the three FTS5 virtual tables (`symbols_code_fts`, `symbols_config_fts`, `symbols_docs_fts`). Use after schema-level FTS5 trigger changes.

Notes: safe but slow on large indexes.

### `self_test` {#tool-self_test}

Smoke-test the install: open DB → create synthetic project → index → search → byte-offset retrieve.

Notes: read-only; uses a temp project cleaned up before return.

### Stable symbol IDs

```
"{file_path}::{qualified_name}#{kind}"

e.g.  "internal/db/db.go::db.Open#Function"
      "src/auth/jwt.ts::AuthService.verify#Method"
```

When a file is renamed, pincher records a redirect in `symbol_moves`. `symbol` resolves stale IDs transparently via `store.ResolveStaleID()` — agents never get "not found" because a file moved.

### Field projection

The [`search`](#tool-search) and [`symbol`](#tool-symbol) tools accept a `fields` parameter — a comma-separated list of columns to return. Use it to cut token usage when you only need specific attributes.

```
fields="id,name,file_path"            # minimal — just locate the symbol
fields="id,name,signature,start_line" # enough to understand the interface
fields="id,name,source"               # name + full source, skip metadata
```

Available fields: `id`, `name`, `qualified_name`, `kind`, `language`, `file_path`, `start_line`, `end_line`, `signature`, `docstring`, `source`, `is_exported`, `extraction_confidence`. Omitting `fields` returns all columns.

### Empty-response taxonomy

Every tool that can return an empty result stamps `_meta.empty_reason` (stable machine-readable code) alongside `_meta.diagnosis` (human-readable text). The enum is the routing-friendly signal — agents, aggregators, and fallback chains consume the code; humans read the diagnosis. `meta=lite` callers keep both fields; they're per-call actionable, not dogfood-only.

| Code | When it fires | Recovery |
|---|---|---|
| `no_project_indexed` | No project matches the session/explicit arg; symbol store is empty | `index <path>` |
| `stale_index` | Running binary is newer than `schema_version_at_index` OR working tree drifted vs index | `index force=true` |
| `unsupported_language` | File extension detected but no extractor registered (Haskell, post-v0.63) | Wait on [#1161](https://github.com/kwad77/pincher/issues/1161) |
| `low_confidence_extractor` | Extractor ran but every symbol fell below `min_confidence` floor | Lower the floor or pick a higher-tier language |
| `same_file_only` | Language has same-file CALLS but no cross-file resolver | Scope to same file or wait on cross-file work |
| `cross_file_unavailable` | Extractor emits zero edges; ghost-extraction signature (#815) | Force re-index; check `doctor` extraction_failures |
| `query_too_narrow` | Combined filters (kind + language + corpus + min_confidence) excluded everything; verifier names which one | Drop the filter named in `diagnosis` |
| `no_results_in_corpus` | Query and filters are fine but the symbol genuinely isn't indexed | Re-spell or widen the corpus |
| `cap_dropped_all` | Every candidate was dropped by `max_hops` / `limit` / `offset` cap (incl. #1033 offset-past-end) | Raise the cap or paginate |
| `incremental_no_change` | Index ran but every file was unchanged (incremental fast path) | Expected; `force=true` if you suspect corruption |
| `all_files_blocked` | Every discovered file was filtered by `ast.ShouldSkip` (lockfiles, minified bundles) | Index a parent directory if sources are nested elsewhere |
| `extractor_emitted_nothing` | Files processed and not blocked, but extractor returned zero symbols | Language-detection gap; check `health` per-language coverage |

Stamped by: `search`, `query`, `trace`, `neighborhood`, `dead_code`, `architecture`, `schema`, `list`, `index`, `changes`. The enum lives in `internal/server/empty_reason.go`; add new codes there and the gate test fails if a stamp site uses a literal. ([#1252](https://github.com/kwad77/pincher/issues/1252))

### Extraction confidence

Every symbol carries an `extraction_confidence` score surfaced in search results and graph queries.

| Score | Parser | Languages |
|---|---|---|
| `1.0` | `go/ast` / `yaml.v3` / `mvdan.cc/sh/v3` / `hashicorp/hcl/v2/hclsyntax` / `BurntSushi/toml` / `yuin/goldmark` / `nikolalohinski/gonja` / `python/ast` (#856) | Go, YAML, JSON, Bash, HCL/Terraform, TOML, Markdown, Jinja2, Python |
| `~0.92–0.98` | AST/regex blends | HTML (Section, 0.917), JavaScript/TypeScript (Regex, ~0.96–0.98 typical) |
| `0.85` | Stable regex | JSX, TSX, Rust, Java, Swift, Kotlin, C#, PHP, C, C++, Makefile, SQL |
| `~0.9` | Approximate regex (#1107 Ruby tuning) | Ruby |
| `0.70` | Approximate regex | (none — all single-language extractors promoted in v0.73) |
