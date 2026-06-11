# The 34 MCP tools

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

Source for one symbol by stable ID. O(1): 1 SQL + 1 `os.Seek` + 1 `os.Read`. No re-parse. Supports `fields` projection and `detail="skeleton"` ([Skeleton mode](#skeleton-mode)).

Token savings: file size minus symbol size (real BPE).

### `symbols` {#tool-symbols}

Batch retrieve up to **100** symbols in one call. Hard cap: requests >100 IDs are rejected. Always prefer this over calling `symbol` in a loop. Supports `detail="skeleton"` ([Skeleton mode](#skeleton-mode)) — the cheap way to skim several bodies at once.

Token savings: same per symbol.

### `context` {#tool-context}

Symbol + all direct callees in one call. The preferred tool for understanding a function. Supports `detail="skeleton"` ([Skeleton mode](#skeleton-mode)), applied to every source payload (symbol + imports + callees); skeleton mode bypasses the `PINCHER_DIFF_CONTEXT` diff cache so the two representations can't poison each other.

Diff-encoded repeat reads (#655, default-on since v1.4): a repeat `context(id)` on an unchanged file short-circuits to `{unchanged:true}`; a changed file ships `symbol.diff` (a line diff against what was last served) + `since_hash` instead of `source`. The cache is per-connection and only engages on full-fidelity serves — `max_tokens`-budgeted, `fields`-projected, skeleton, HTTP-REST, and `batch quiet` calls never read or write it. Pass `diff=false` to bypass the cache and always receive the full body — the recovery path when the prior response is not in your context (fresh session against a long-lived server, subagent sharing a parent connection); the `{unchanged:true}` response names this escape hatch in `_meta.warnings`. `PINCHER_DIFF_CONTEXT=0` disables the feature process-wide.

Token savings: ~90% vs reading files.

## Search & graph

### `search` {#tool-search}

FTS5 BM25 across names, signatures, docstrings. Wildcards (`auth*`), phrases (`"process order"`), AND/OR. `kind`/`language`/`corpus` filters. `corpus` defaults to `code`; pass `config` for YAML/JSON/HCL settings, `docs` for Markdown / Documents. The legacy `all` value was removed in v0.5; older callers passing it get soft-redirected to `code` with a deprecation log line. `fields` projects columns. `project=*` searches all repos. `format="text"` replaces the `results` array with `results_text` — a TSV block (header row, then `id<TAB>kind<TAB>file:line<TAB>signature-or-name` per hit) at ~0.45× the JSON token cost on a representative 20-hit search; the rest of the envelope (`count`/`total`/`has_more`/`_meta`) is unchanged. `format="toon"` replaces it with `results_toon` — a TOON (Token-Oriented Object Notation) tabular block: the field list declared once (`results[N]{file,id,kind,line,name,signature}:`), then one bare comma-delimited row per hit, strings quoted only when needed; ~0.50× the JSON token cost on the same fixture. Opt-in only — ids/file paths/names appear verbatim, so follow-up calls can copy them exactly. `count_only=true` returns just `{query, total, by_kind}` — the conclusion-density shape for "how many matches?" questions; composes with every filter and skips the per-hit snippet disk read.

Tested latency: 1 ms.

### `query` {#tool-query}

pinchQL graph queries — Cypher-shaped subset. Three SQL paths: node scan, single-hop JOIN, variable-length BFS. `max_rows` defaults to 200 and caps at 10000. Parameter: `pinchql`; legacy alias `cypher` is accepted for one release.

Tested latency: 2 ms (single-hop).

### `trace` {#tool-trace}

BFS call-path trace — who calls this, or what does it call. Grouped by depth. Risk labels: CRITICAL (depth 1) → LOW (depth 4+). `format="text"` replaces the `hops` array with `results_text` — a TSV block (header row, then `depth<TAB>risk<TAB>id` per hop). `format="toon"` replaces it with `results_toon` — a TOON tabular block (`hops[N]{depth,id,risk}:`, one bare comma-delimited row per hop). `compact=true` additionally ditto-compresses consecutive same-file nodes within a depth block: a node repeating the previous node's `file_path` omits the field — scan up to the nearest node carrying `file_path` to decode. `count_only=true` returns just `{root, direction, total, by_depth, by_risk}` — the conclusion-density shape for "how many callers?" questions; counts come from the exact same traversal + filters the row-shaped call runs.

Tested latency: <5 ms (depth 3).

### `assert_graph` {#tool-assert_graph}

Server-side invariant evaluation over the edge graph — the conclusion-density sibling of `count_only`. Pass `kind` + `target` (+ `scope` / `limit` per kind) and get `{pass, checked}` back when the assertion holds, plus `violations` (up to 10 `{id, file_path}`) when it doesn't. Kinds (the set is closed at exactly four; unknown kinds rich-error with the full catalog): `no_callers_outside` (every caller of `target` lives under a `scope` path prefix), `max_callers` (at most `limit` direct callers), `no_calls_to` (nothing under `scope` calls `target` — layering rule), `exists` (`target` resolves to at least one indexed symbol; exact name first, FTS5 fallback). Caller-shaped kinds count direct CALLS/HTTP_CALLS/ASYNC_CALLS edges; test files are included — an assertion is about the whole graph.

Tested latency: ~1-5 ms (one edge query + per-caller lookups).

### `context_for_task` {#tool-context_for_task}

Composite: one call replaces 5-10 atomic calls during investigation. Takes either `task` (free-form) or `seed_id`. Composes `search` → `context` → `trace direction=both` → `changes` overlap into one envelope `{seeds, neighbors, callers, callees, recent_changes}`. `max_seeds` defaults to 3 and caps at 10; `trace_depth` defaults to 2 and caps at 4; `include_changes` defaults true. Use when starting an investigation; use the atomic tools for follow-up. (#1259 v0.71)

Tested latency: ~20-80 ms (≈ Σ atomic-call latencies).

### `investigate_failure` {#tool-investigate_failure}

**Composite #1 of Phase 4 (#1391 v0.81).** The bug-hunt loop in one call. Takes `error_text` (raw stack trace / panic / exception), parses identifier-shaped frame tokens via a stopword-aware heuristic, BM25-searches each across the code corpus, and ranks suspects by a weighted sum of evidence: stack-frame match (+0.45), multi-frame match (+0.10), file-appears-in-trace (+0.20), modified-in-working-tree (+0.20), caller fan-in (log-scaled +0.05). Returns `{implicated_symbols, callers, recent_changes, rank, frames_parsed}` with per-suspect `evidence` enumerating which signals fired. Replaces the typical 5-call bug-hunt sequence. Stamps `_meta.empty_reason` when no frames parse or no suspects resolve.

Tested latency: ~40-150 ms (BM25 × N frames + trace × maxSuspects).

### `plan_change` {#tool-plan_change}

**Composite #2 of Phase 4 (#1391 v0.82).** Pre-edit blast-radius composite. Takes `target` (file path, symbol id, or free-form name). Resolves to one or more affected callable symbols, traces inbound callers at depth 1 (CRITICAL) and depth 2 (HIGH), partitions them by package boundary + test-file status, and looks up ADRs whose key/value mentions the target's package, directory, or symbol name. Returns `{target, blast_radius, related_adrs}` with `blast_radius.summary` counts and `blast_radius.test_files_intersecting` (the test files you should run before pushing). Emits `_meta.warnings_v2.blast_radius_high` when depth-1 caller count exceeds 14 (suggests staged refactor). Replaces the typical 4-call pre-edit sequence (`changes` → `trace direction=in` → `context` → `adr list`).

Tested latency: ~20-100 ms (resolve + trace × affected symbols + ADR overlap).

### `verify_change` {#tool-verify_change}

**Loop-substrate PR-10.** The loop's post-edit gate in one call — "did my edit do what I planned, what do I run, what broke." Composes: (1) the `changes` blast-radius analysis over `scope` (`unstaged`/`staged`/`all`/`base:<branch>`); (2) `tests_to_run` ranked by overlap descending — run the top entries first; (3) when `target` matches a prior `plan_change` run, `plan_comparison.{predicted_callers, actual_impacted, unpredicted_impact}` — the plan cache stores each `plan_change` run's full depth-1 caller set (newest 32, in-memory), and verify compares it against the depth-1 callers the diff actually impacts, warning via `warnings_v2 code=unpredicted_impact` when the edit reached callers the plan never saw and `code=plan_stale` when the index generation moved between plan and verify (no bogus diff); (4) `possibly_orphaned` — the `dead_code` SQL path restricted to the changed files, reporting symbols with zero inbound edges now (`possibly_orphaned_by_change`, advisory; Functions only — Methods are interface-dispatch-blind). `max_tokens` bounds the envelope: bulk lists trim first (changed_symbols → possibly_orphaned → tests_to_run with a floor of 3 → plan-comparison id lists), summary counts always ship.

Tested latency: ~20-100 ms (git diff + trace × changed symbols + one orphan SQL pass).

## Architecture & knowledge

### `architecture` {#tool-architecture}

Language breakdown, entry points, hotspot functions, graph stats, surprising connections (rarest cross-package CALLS pairs). Start here on any unfamiliar project.

Tested latency: 12 ms.

### `branch_overlap` {#tool-branch_overlap}

Diffs two in-flight branches against their shared merge-base, maps changed files to symbols, intersects the sets. Returns `overlapping_files`, `overlapping_symbols`, and a merge-order-risk `verdict`.

Tested latency: ~10 ms.

### `coach` {#tool-coach}

Retro-coaching mined from pincher's own recorded usage telemetry (per-call events, query-failure counters, hook invocations). `window=session` (default) or `window=7d`. Returns priced findings — `single_fact_burst`, `unbudgeted_heavy_context`, `zero_result_churn`, `hook_fall_through` — each with `occurrences`, `est_tokens_left_on_table`, a concrete `recommendation`, and a `basis` string documenting exactly how the number was computed from recorded data. Degrades honestly: counts-only when the schema lacks per-row token estimates, empty findings plus a note when fewer than 10 calls are recorded in the window.

Tested latency: ~5 ms.

### `schema` {#tool-schema}

Node kind counts, edge kind counts, totals. Use before `query` to see what's indexed.

Tested latency: 1 ms.

### `adr` {#tool-adr}

Persistent key/value store per project. Survives context resets and binary upgrades. Actions: `get`, `set`, `list`, `delete`.

Tested latency: <1 ms.

### `loop` {#tool-loop}

Durable work-state for multi-iteration agent loops (loop-substrate PR-8/9). Actions: `start` (open a named loop), `checkpoint` (append one iteration's claim/decision/confidence/reopen_trigger/evidence, stamped with the index watermark), `handoff` (compose a pointer manifest server-side — open reopen-triggers incl. `AWAITING HUMAN` entries verbatim, ADR keys, working-tree summary via the shared changes analysis, current watermark, last 3 checkpoint receipts, suggested re-entry seed ids parsed from recent evidence — and append it as a regular checkpoint, ≤600 tokens; replaces prose handoff.md files: ~200 tokens to write vs 2-5k prose, ~150 tokens to resume, and every pointer dereferences live state instead of freezing line numbers), `list`, `resume` — one bounded brief (default ~800 tokens, `max_tokens` to adjust) with the checkpoint tail, open reopen-triggers, ADR keys, and whether the index changed since the last checkpoint; when the newest checkpoint is a handoff, it leads the brief — and `export` (render the ledger as a human-readable Markdown document on demand — claims/decisions/open triggers/awaiting-human, covering the window since the previous handoff by default, `seq` to pick an endpoint, `max_tokens` default 2000; returns `{markdown}`, never writes files). ADR holds conventions; the loop ledger holds in-flight work.

Tested latency: <1 ms.

### `batch` {#tool-batch}

One envelope, N read-only answers (loop-substrate). Carries up to 12 `{tool, args}` sub-queries — any of `search`, `symbol`, `symbols`, `context`, `trace`, `query`, `neighborhood`, `changes` — answered in order under a shared `max_tokens` budget (default 4000; later sub-queries past the budget return `skipped:"budget_exhausted"` plus a `budget_truncated` warning). Each entry carries a slim per-entry `_meta` (`empty_reason`, `tokens_used`, `warnings_v2` only); the outer envelope carries the single full `_meta` — one watermark/capabilities/stats accumulation per N answers instead of per answer. Sub-errors are isolated per entry (`error` field + a `batch_sub_errors` warning naming the failed indexes); the rest of the batch completes. Top-level `project` is merged into every sub-query that doesn't set its own.

**Chain mode** (server-side pipelining, additive — independent queries are unchanged). When sub-query N's input is sub-query M's output (M < N), declare `from: {query: M, select: "<named selector>", into: "<arg key>"?}` and the server splices the selection into N's args — the intermediate never crosses the token envelope. Named selectors only (v1, no path language): `top_id` (first result's stable symbol id — `search results[0].id`, `trace hops[0].nodes[0].id`, `context symbol.id`), `ids` (all result ids, deduped, capped at 20 with a `chain_selector_trimmed` warning), `files` (distinct `file_path` values, capped at 10). `into` defaults: `context`/`symbol`/`trace` → `"id"`, `symbols` → `"ids"`; plan-shaped tools (`search`, `query`, ...) require it named explicitly. Multi-value selects pair with `symbols`' `ids` arg only — fan-out is not implemented in v1. Execution is strictly declaration-order: `from.query` must reference a lower index (forward references rich-error at validation, before any execution); an errored/skipped/empty upstream yields `{index, tool, skipped:"upstream_empty", upstream: M}` — never a guessed call; chain segments respect the shared `max_tokens` budget exactly as independent queries do. Mark an upstream `quiet: true` to omit its body from the response entirely — its entry becomes `{index, tool, quiet:true, selected: <the value(s) passed on>}`, keeping the chain auditable. A quiet `search` → chained `context` ships one context envelope plus a one-line provenance pointer — measured ≥25% smaller than the two-call equivalent on the test fixture.

Tested latency: sum of sub-query latencies (in-process dispatch; no per-sub-query transport cost).

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

Notes: per-target `{target, path, action, diff_preview, bytes_in, bytes_out}`. Codex emits `{target, action: "skipped_always_global", reason}`. `target=claude-skills` (the shipped methodology-skills installer) is refused over MCP — it writes into the always-global `~/.claude/skills/`, outside `project_path`; use the `pincher init --target=claude-skills --write` CLI.

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

### Skeleton mode {#skeleton-mode}

[`symbol`](#tool-symbol), [`symbols`](#tool-symbols), and [`context`](#tool-context) accept `detail="full"` (default) or `detail="skeleton"`. Skeleton mode replaces each `source` payload with a deterministic structural outline: the signature line(s) and top-level control-flow lines (`if`/`for`/`switch`/`select`/`return`/…) are kept verbatim — nesting indicated by the original indentation — and every other run of lines is elided into a marker:

```
… 12 lines (calls: parseInput, validateOrder)
```

Call names are harvested from the symbol's outbound CALLS edges (already in the graph — free) intersected with textual occurrence ordering; edge-listed callees that text matching can't place are appended in one trailing `… calls (from graph): …` line, so **every CALLS-edge callee name is guaranteed to appear in the skeleton**. Affected responses carry one top-level `_meta.skeleton: true` marker.

The economics: agents skimming code in the orientation/probe phase need shape, not bodies — a representative 120-line mixed-flow function compresses to under 25% of its full-source tokens. Honest accounting: `tokens_saved` keeps the same file-read baseline; the skeleton's win shows up as a smaller `tokens_used`, not an inflated saving. v1 is a line-classifier pass over the byte-offset-retrieved source — deterministic, language-agnostic, no re-parse; tree-sitter-precise skeletons (exact block boundaries, expression-level elision) are the documented v2 path. In `context`, skeleton mode bypasses the [#655](https://github.com/kwad77/pincher/issues/655) diff cache: the cache only operates on `detail=full` so full and skeleton representations can never poison each other. Documents (fetched URLs) are prose, not code, and always ship full.

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
| `all_files_blocked` | Every discovered file was filtered by `ast.ShouldSkip` (lockfiles, minified bundles) or an over-broad `.pincherignore` / `.gitignore` | Index a parent directory if sources are nested elsewhere; check ignore files |
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
