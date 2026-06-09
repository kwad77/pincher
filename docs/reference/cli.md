# CLI subcommands & flags

[Back to reference index](README.md)

## CLI subcommands

`pincher <subcommand> --help` prints per-subcommand flag detail.

### `pincher index`

One-shot index without starting an MCP server — useful in CI, pre-commit hooks, or as a Claude Code SessionStart hook.

```bash
pincher index                        # index current directory
pincher index /path/to/repo          # index a specific path
pincher index --force                # re-parse all files, ignore content hashes
pincher index --hook                 # emit Claude Code SessionStart JSON envelope
pincher index --json-summary         # machine-readable JSON output
pincher index --data-dir /custom     # override data directory
pincher index --max-file-size-mb 32  # per-file size cap (override default)
```

`--hook` outputs the JSON envelope Claude Code's SessionStart hook injects as `additionalContext`. Configure in `.claude/settings.json`:

```json
{
  "hooks": {
    "SessionStart": [
      { "type": "command", "command": "pincher index --hook" }
    ]
  }
}
```

### `pincher doctor`

Diagnostic report — schema version, index staleness, extraction-failure counts by reason, slow-query log. Both human-readable and JSON output:

```bash
pincher doctor                       # Markdown report
pincher doctor --json                # structured output for CI
pincher doctor --data-dir /custom    # inspect a non-default install
pincher doctor --lookback 24         # filter slow queries / failures by age, in hours
pincher doctor --top 20              # cap failure / slow-query rows
pincher doctor --project pincher     # scope project rows and failures by name/id substring
pincher doctor --fix                 # auto-resolve the safe subset of advisories
pincher doctor --fix --json          # structured fix-action report for CI
```

The JSON report includes database-level `db_size_bytes` / `wal_size_bytes`
plus a per-project `projects[].db_bytes_estimate`. The per-project estimate is
best-effort and intended for triage: use the relative ordering to find which
project owns disk growth before removing stale projects or vacuuming. Human
output surfaces the same value as `db≈...` in each project row. The estimate
includes durable resolver caches such as `pending_edges`, so a project with
heavy cross-file call/read/import candidates is attributed to the project that
created that storage instead of hiding inside the database total.

**`--fix` safe-action allowlist (#1260 §3 v0.69):**

| Action | Condition | Status if applied |
|---|---|---|
| `vacuum-db` | DB has >50 MB reclaimable space (VACUUM run; threshold gates the cost on a clean install) | reports `applied` with byte counts |
| `prune-stale-failures` | Extraction-failure rows predate the current project index pass | reports removed-row count |
| `reap-orphan-locks` | Dead/recycled processes left stale project lockfiles | reports reclaimed lock count |

Each action ends up `applied` / `noop` (criterion not met) / `skipped` (precondition like an open WAL reader blocks the fix) / `error` (the fix attempted and failed). **Destructive remediations** — project deletion, force-reindex, prune-stale — stay explicit-action and require the targeted subcommand (`pincher project rm`, `pincher index --force`, `pincher project prune-stale`). Their cost or destructiveness shouldn't be silently absorbed into a generic `--fix`.

### `pincher self-test`

End-to-end smoke check against a temporary data directory: open the database, create a synthetic project, index a sample file, search for a known symbol, retrieve it via byte-offset. Exits non-zero on any failure.

```bash
pincher self-test                    # 5-step smoke test
pincher self-test --verbose          # also prints per-step timings
pincher self-test --json             # machine-readable liveness report
```

`--json` writes a single structured report to stdout and keeps prose off the
stream, so CI can pipe it directly into `jq`. Each step includes a stable
`name`, human label, `ok`, and `duration_ms`; failed steps also include `error`.

### `pincher rebuild-fts`

Escape hatch for FTS5 corruption. Drops every FTS5 virtual table (legacy `symbols_fts` + the per-corpus `symbols_{code,config,docs}_fts`) and their sync triggers, then bulk-loads them back from the canonical `symbols` table.

```bash
pincher rebuild-fts                  # rebuild and print row count
pincher rebuild-fts --quiet          # row count only — pipe-friendly
pincher rebuild-fts --data-dir /x    # override data directory
```

Use this if the `search` MCP/HTTP tool returns results inconsistent with the `query` MCP/HTTP tool against the same project. Cost is proportional to symbol count (seconds-to-minutes on large repos). Source files are not re-walked.

### `pincher update`

Auto-detects whether the binary is running from a clone of pincherMCP (walks ancestors looking for a `go.mod` matching this module). In-repo: `git fetch` + `git pull --ff-only` + `go build`. Otherwise: queries the GitHub releases API, picks an asset matching `GOOS`/`GOARCH`, atomically swaps the running binary aside on Windows before installing the replacement.

**Install-method detection (#1260 §5).** When the running binary's path resolves under a Homebrew prefix (`/opt/homebrew/`, `/usr/local/`, `/home/linuxbrew/.linuxbrew/`), `pincher update` skips the GitHub-asset path and prints the brew command instead (`brew update && brew upgrade pincher`). Pre-fix Mac users got `go install` instructions they couldn't follow without a Go toolchain. Pass `--yes` to invoke brew directly; default stays advisory because brew users generally want to see brew's output live.

```bash
pincher update                       # apply update if behind
pincher update --check               # report status only
pincher update --source DIR          # override auto-detected checkout
pincher update --dry-run             # print what would run
pincher update --yes                 # skip confirmation
```

Standalone update consumes the release archives published for each tag
(`pincher-vX.Y.Z-linux-amd64.tar.gz`, `pincher-vX.Y.Z-darwin-arm64.tar.gz`,
Windows `.zip`, etc.) and extracts the contained binary before swapping it
into place. If no matching archive or raw binary exists for the current
platform, it prints the `go install` fallback instead of silently trying an
unsupported asset.

### `pincher web`

Resolve the dashboard URL of the running pincher HTTP server. If a live server is found via the sessions table (PID liveness + `/v1/health` probe), prints the URL. Otherwise auto-spawns `pincher --http 127.0.0.1:N` detached on a free port (scans upward from 7777, 16-port window), polls `/v1/health` until ready, prints the new server's URL.

```bash
pincher web                          # print dashboard URL; auto-start if none
pincher web --no-start               # exit non-zero if none running
pincher web --port 8080              # scan from 8080 instead of 7777
pincher web --json                   # {url, base, pid, started_by}
pincher web --timeout 8              # auto-start readiness wait (seconds)
```

The dashboard URL is `<base>/v1/dashboard` (honors `--basepath` reverse-proxy prefix).

### `pincher setup`

Interactive install wizard for terminal users. It detects supported agent/editor
targets, lets you select hosts and options, writes through the same idempotent
marker-block machinery as `pincher init`, and can optionally index the current
project when it finishes.

```bash
pincher setup                         # interactive TUI; requires stdin/stdout TTYs
```

For scripted installs or non-interactive shells, use `pincher init --target=<name>`
instead. `setup` refuses piped/non-TTY execution so automation does not hang on
an invisible prompt.

### `pincher init`

Inject the pincher usage policy block into an editor or agent's rules file. Wraps the policy in `<!-- pincher:start --> ... <!-- pincher:end -->` markers (or `// pincher:start` line markers for JSON-based targets like Continue) so re-running replaces the block in place — idempotent, no duplicates.

```bash
pincher init                              # default: ./CLAUDE.md
pincher init --global                     # claude global: ~/.claude/CLAUDE.md
pincher init --target=cursor              # ./.cursor/rules/pincher.mdc (with frontmatter)
pincher init --target=cursor-legacy       # ./.cursorrules
pincher init --target=windsurf            # ./.windsurf/rules/pincher.md
pincher init --target=aider               # ./CONVENTIONS.md
pincher init --target=continue            # ~/.continue/config.json (merges into systemMessage)
pincher init --target=detect              # write only to editors whose marker file exists under cwd
pincher init --target=all                 # write every project-scoped target
pincher init --dry-run                    # print what would be written; do not modify
```

The cursor target preserves any user-edited YAML frontmatter (`description`, `globs`, `alwaysApply`) on re-runs — only the marker block in the body is replaced. The continue target preserves all unknown JSON keys; only the `systemMessage` field is touched.

After writing, prints a short next-steps recipe + the URL of any running pincher HTTP dashboard discovered via the v11 sessions table.

### `pincher init --git-hooks`

```bash
pincher init --git-hooks                  # install post-checkout / post-merge / post-rewrite
pincher init --git-hooks --dry-run        # preview what would be written
pincher init --git-hooks --force          # replace non-pincher hooks (backed up to .pincher-backup)
```

Installs git hooks into `.git/hooks/` so branch switches, fast-forward merges, and rebases trigger an eager reindex (#1261 §1). Without these, the `Watch()` poller catches the changes one diff-pass at a time, leaving a window where the index reflects a mix of both branches.

Each hook carries the `pincher.io/managed` marker so future runs can safely replace pincher-managed hooks without clobbering hand-written user hooks. The hook is a small POSIX sh script that calls `pincher index "$REPO_ROOT" --force` in the background — git operations don't block, and the indexer fires as soon as `git checkout` returns. `command -v pincher` guard means a missing pincher binary never breaks the user's git workflow.

**post-checkout no-op shortcuts (#1303 §2a):** the post-checkout hook respects git's no-op signals — file checkouts (`git checkout README.md`, where `$3=0`) and re-checkouts of the current branch (where `$1=$2`) skip the reindex entirely. Saves the per-call BuildClosure cost on every routine file-level operation; only real branch movement triggers a reindex.

§2b (schema `branch` column for branch-aware queries — `search`/`query` filterable by branch dimension) deferred to its own follow-up issue.

### `pincher project`

Surface the HTTP `DELETE /v1/projects` and the `list` MCP tool as CLI verbs so users on the stdio binary don't need a SQL or curl one-liner.

```bash
pincher project list                      # human-readable table (alias: ls)
pincher project list --json               # machine-readable JSON
pincher project rm <name>                 # interactive Y/n confirmation (alias: remove, delete)
pincher project rm <name> --force         # skip confirmation
pincher project rm <name> --json --force  # JSON receipt; --force required in JSON mode
pincher project prune-stale               # drop projects schema-stale AND idle (default --days 30)
pincher project prune-stale --days 7 --force
pincher project prune-dead --force        # drop projects whose path no longer exists
```

`<name>` resolves in this order: full project id → exact name (case-insensitive) → substring on name or path. A substring that matches multiple projects errors with a disambiguation list rather than picking one. JSON mode requires `--force` (no interactive prompt is possible in a scripted workflow).

`prune-stale` drops every project that is **both** schema-stale (indexed by an older binary) **and** not re-indexed in `--days` N days (default 30) — pairing the two conditions scopes the prune to genuinely-abandoned projects, not one a developer touched yesterday that just needs a re-index.

`prune-dead` drops projects whose indexed path no longer exists on disk. It is
the CLI equivalent of the MCP `list prune_dead=true` cleanup path, useful when
the MCP host is not loading but the local binary still works.

### `pincher vacuum`

```bash
pincher vacuum                            # reclaim DB file space (checkpoint → VACUUM → checkpoint)
pincher vacuum --json                     # JSON receipt: bytes_before / bytes_after / bytes_reclaimed
```

SQLite does not shrink the database file when rows are deleted — `pincher project rm` / `prune-stale` / `prune-dead` free pages internally but the file stays large. `pincher vacuum` rewrites the file to reclaim that space. It is a deliberate, explicit CLI step (VACUUM holds an exclusive lock for the duration) kept out of the hot MCP path; run it after a prune, when no agent is mid-query.

### `pincher bench`

```bash
pincher bench                             # bench largest project, 20 samples, text output
pincher bench --project NAME              # bench a specific project
pincher bench --n 50 --depth 3            # more samples, deeper trace
pincher bench --json                      # CI-friendly structured output
pincher bench --seed 42                   # reproducible sample set
```

Falsifiable token-savings measurement against the user's own indexed corpus (#1263 §1). Runs three tool shapes (search / context / trace) against a random sample of edge-bearing Function/Method symbols, computes a full-file Read baseline for each touched file (what an agent without pincher would have paid), and reports per-tool p50/p95 latency plus actual-vs-baseline tokens plus a savings percentage.

Distinct from `make bench` / `make corpus-bench` (internal perf gates) and from the session-stats box (which reports cumulative `tokens_saved` against an assumed baseline). `pincher bench` is the artifact you can run on YOUR codebase to answer "does pincher actually save me tokens on my project?" — the synthetic pincher-repo numbers in [Why it matters](https://kwad77.github.io/pincher/#why-it-matters) are easy to dismiss; this is the local proof.

Baseline model: search baseline = sum of unique file sizes across every result file (Grep + N×Read); context baseline = full file bytes of the symbol's file (cat); trace baseline = sum of unique file sizes across every touched symbol (N×Read while walking callers). Actual = JSON-serialized response bytes/4 — the same heuristic pincher's `tokens_used` envelope uses on every MCP response, so bench savings line up with the session-stats box.

`--json` includes `project_id`, `project_name`, and `project_path` so a saved benchmark artifact remains self-describing without a separate `pincher project list` lookup.

Per #1263 §2 (canonical workflow corpus + comparator implementations vs Sourcegraph CLI etc.) rolls forward to v0.69+; this v0.68 cut is the runs-on-your-own-project minimum.

### `pincher supervised`

Recommended runtime for agent CLIs (Claude Code, Cursor, Codex). Runs the same MCP stdio server as the bare `pincher` invocation, but wraps it in a parent supervisor that auto-restarts the inner process on (a) schema drift detected against a freshly-built binary on disk, or (b) crashes. The supervisor replays the MCP initialize handshake to the host so the agent never sees the underlying respawn.

```bash
pincher supervised                                # the canonical agent-CLI entry point
pincher supervised --inner-binary ./pincher       # stable provider, dirty workspace action binary
PINCHER_SUPERVISED_INNER_BINARY=./pincher pincher supervised
PINCHER_AUTO_RESTART_ON_DRIFT=1 pincher           # equivalent for non-supervised hosts
```

Auto-restart-on-drift is load-bearing for the dogfood loop: when you `make install` a freshly-built `pincher` over the on-PATH binary, the next MCP call detects the mtime bump, the supervisor exits the inner process, and re-spawns against the new binary — no manual `/mcp` reconnect required. The Windows binary-swap path uses an explicit rename-out trick (per #705) so an in-use `pincher.exe` can be replaced without the "Device or resource busy" error.

`--inner-binary` (or `PINCHER_SUPERVISED_INNER_BINARY`) lets the stdio provider stay on a stable installed release while delegating MCP tool actions to a different pincher binary, which is useful when dogfooding a dirty workspace build without invalidating the host's long-lived MCP session.

Without supervised mode (or the equivalent env var on a non-supervised stdio host), a binary swap lands on disk but the running MCP process keeps serving the old binary until the host is restarted. The MCP `health` tool and `POST /v1/health` response report `binary_stale: true` when the swap landed but the running process hasn't picked it up.

### `pincher health-check`

Out-of-process liveness probe for cron/launchd/k8s/systemd — spawns a short-lived MCP client, performs the handshake + `tools/list`, exits 0 on success and non-zero on any failure within the timeout. Distinct from `pincher doctor` (which inspects the stored database; this exercises the live RPC surface).

```bash
pincher health-check                              # probe the running binary (os.Executable)
pincher health-check --binary /path/to/pincher    # probe a specific binary
pincher health-check --supervised                 # probe via `pincher supervised` (matches production launch shape)
pincher health-check --timeout 30s                # raise the default 10s ceiling
pincher health-check --verbose                    # dump JSON-RPC traffic for debugging
```

Output: `OK <stamped-version>` on stderr for success; a failure summary otherwise. Stdout is reserved for future structured output. Exit codes: `0` healthy, `1` probe failed (timeout / handshake error), `2` argument parse error.

Pairs with `pincher supervised`: a typical k8s liveness probe is `pincher health-check --supervised --timeout 30s` so the probe matches the production launch shape rather than a bare `pincher` invocation that would never run in practice.

### `pincher verify` (#1399)

Re-hashes every indexed file's on-disk content and reports drift against the stored `files.hash` column. Surfaces three failure modes: (a) out-of-band file modification since the last index, (b) on-disk deletion of an indexed file, and (c) persistence bugs that left the stored hash diverged from extraction. Doesn't auto-fix anything — mirrors `pincher doctor`'s posture: surface drift, the operator re-indexes.

```bash
pincher verify                                    # all projects, text output
pincher verify --json                             # structured per-project drift report
pincher verify --project NAME                     # restrict to a project (name or id substring)
pincher verify --data-dir /x                      # override data directory
```

Stoa-family precedent: `stoa verify` hashes manifests as the integrity-check leg of the verify/doctor/probe trinity. Pincher's `doctor` is the doctor leg already; `verify` adds the integrity leg. Exit codes: `0` no drift, `1` drift detected (caller re-indexes), `2` couldn't open the database.

### `pincher stats`

Prints persisted session savings (cumulative `tokens_saved`, MCP call count, baseline-method breakdown) plus per-project file/symbol/edge counts. The CLI surface for what the dashboard renders interactively. CLI-only by deliberate choice — wiping stats is destructive admin, not an agent action.

The baseline-method breakdown is reconciled against the all-time totals. Current per-call rows are grouped under `_meta.baseline_method` values such as `full_file_read` and `none`; older session totals recorded before per-call baseline metadata are reported as `legacy_unattributed` instead of disappearing from the breakdown. In `--json`, the shape is `all_time.baseline_methods.<method>.{calls,tokens_used,tokens_saved}`.

```bash
pincher stats                                     # human-readable savings + per-project counts
pincher stats --json                              # structured output for CI / paste
pincher stats --reset                             # wipe sessions table (symbol data unaffected)
pincher stats --reset --json                      # JSON confirmation receipt
pincher stats --data-dir /x                       # override data directory
```

`--reset` clears the `sessions` table only — symbol / edge / project rows are untouched, so a `pincher stats --reset` followed by an index rebuilds the savings counters from zero without losing the indexed corpus. Back up first with `pincher stats --json > snapshot.json` if you want to keep the prior numbers.

Cross-references the `mcp__pincher__stats` MCP tool (read-only equivalent of the non-reset path, agent-accessible).

### `pincher hook-stats` (#662)

Emits a shareable JSON snapshot of trailing 7-day Claude Code PreToolUse hook conversion-rate metrics — what the dashboard's hook panel shows, in a form that pastes cleanly into GitHub issues or DMs. Anonymised by default: no file paths, no hostnames, no project names.

```bash
pincher hook-stats --export-7d                    # required flag — no default action
pincher hook-stats --export-7d --include-host     # add pincher version + GOOS/GOARCH for outlier triage
pincher hook-stats --export-7d --data-dir /x      # override data directory
```

CLI-only by deliberate choice — the data is human-shareable (paste it on a GitHub thread, send it to #640), not LLM-consumable. Adding an MCP surface would invite an agent to "report telemetry" which is exactly the phone-home shape pincher refuses. Telemetry stays local; this subcommand reads what the dashboard already shows and emits a copy-pasteable snapshot.

Cross-references the `/v1/hook-stats` HTTP endpoint (same payload, served by the running HTTP gateway when `--http` is enabled).

### `pincher completion`

Prints a shell completion script for the binary's available subcommands.

```bash
pincher completion bash               # eval "$(pincher completion bash)"
pincher completion zsh                # eval "$(pincher completion zsh)"
pincher completion fish               # pincher completion fish | source
```

Unsupported shells exit non-zero with usage on stderr.

### `pincher export-graph`

Dumps an indexed project's symbol and edge graph for external graph tools.
This is read-only: it selects persisted symbols/edges and does not re-index.

```bash
pincher export-graph                                  # JSON to stdout
pincher export-graph --format=graphml --out graph.xml # Gephi/Cytoscape/yEd
pincher export-graph --format=dot --project pincher   # Graphviz DOT
pincher export-graph --data-dir /x                    # override data directory
```

Formats: `json` keeps the full record shape; `graphml` targets graph-analysis
tools; `dot` targets Graphviz.

`--project` uses the shared project resolver: full project id, exact name
(case-insensitive), then substring on name or path. Ambiguous substrings fail
with a disambiguation list.

### `pincher report`

Generates a Pincher-native markdown architecture report from the existing
symbol and edge index. This is read-only: it selects persisted evidence and
renders project metrics, languages, entry points, hotspots, rationale snippets,
surprising connections, and suggested next Pincher calls. It does not run LLM
extraction or infer missing data.

```bash
pincher report                                  # markdown to stdout
pincher report --out PINCHER_REPORT.md          # write report artifact
pincher report --project pincher --data-dir /x   # select project/data dir
```

`--project` accepts the same id/name/substring forms as `export-graph`.

### `pincher callflow`

Renders a bounded Mermaid call-flow diagram around one symbol using persisted
`CALLS` edges. Output can be pasted into Markdown, GitHub comments, or the
Mermaid live editor.

```bash
pincher callflow --symbol=HandleSearch                 # callers + callees, depth 2
pincher callflow --symbol='path.go::pkg.Func#Function' # disambiguate by full id
pincher callflow --direction=callees --depth=4
pincher callflow --out flow.md --project pincher
```

`--direction` is `callers`, `callees`, or `both`. Depth is clamped to 1-4 and
large diagrams are capped so hub symbols do not emit unreadable graphs.
`--project` accepts the same id/name/substring forms as `export-graph`.

---

## CLI flags

Apply to the no-subcommand form (running as MCP server).

| Flag | Default | Env fallback | Purpose |
|---|---|---|---|
| `--version` | false | — | Print version and exit. |
| `--data-dir` | platform default | — | Override database directory. |
| `--verbose` | false | — | Verbose logging to stderr. |
| `--http` | "" | `PINCHER_HTTP_ADDR` | Listen for HTTP REST on this address (`:8080`, `:0` for OS-picked). |
| `--http-key` | "" | `PINCHER_HTTP_KEY` | Bearer token for HTTP requests. Recommended for non-localhost. |
| `--http-rate` | 0 | — | Max HTTP requests per IP per minute. 0 = unlimited. |
| `--basepath` | "" | `PINCHER_BASEPATH` | URL prefix behind a reverse proxy (e.g. `/pincher`). |
| `--trust-proxy` | false | `PINCHER_TRUST_PROXY=1` | Honor X-Forwarded-* headers. Only enable behind a trusted proxy. |
| `--slow-query-ms` | 0 | `PINCHER_SLOW_QUERY_MS` | Persist tool calls slower than N ms to `slow_queries`. 0 = disabled (zero overhead). |
| `--db-readers` | 4 | `PINCHER_DB_READERS` | Max concurrent SQLite read connections (1–32). Higher = more parallel tool calls under load. |
| `--max-file-size-mb` | 4 | `PINCHER_MAX_FILE_SIZE_MB` | Per-file size cap during indexing. Larger files recorded as `file_too_large`, skipped. 0 disables cap. |

---

## Environment variables

Used when the matching flag is empty — convenient for Docker, systemd, launchd.

| Variable | Equivalent flag |
|---|---|
| `PINCHER_HTTP_ADDR` | `--http` |
| `PINCHER_HTTP_KEY` | `--http-key` |
| `PINCHER_HTTP_ALLOW_OPEN` | `--http-allow-open` (set to `1` to bind HTTP without an auth key — for deployments behind a trusted reverse proxy that handles auth itself) |
| `PINCHER_MCP_HTTP_PATH` | `--mcp-http-path` (e.g. `/mcp`; mounts the Streamable-HTTP MCP transport on the HTTP server. Required by aggregators that speak MCP over HTTP — see [`docs/streamable-http.md`](../streamable-http.md) and the [Codex tutorial](../tutorials/codex.md)) |
| `PINCHER_BASEPATH` | `--basepath` |
| `PINCHER_TRUST_PROXY` | `--trust-proxy` (set to `1` to enable) |
| `PINCHER_SLOW_QUERY_MS` | `--slow-query-ms` |
| `PINCHER_DB_READERS` | `--db-readers` |
| `PINCHER_MAX_FILE_SIZE_MB` | `--max-file-size-mb` |

**Logging and observability** (no flag equivalent — env-var only; see [`docs/deployment/observability.md`](../deployment/observability.md)):

| Variable | Effect |
|---|---|
| `PINCHER_LOG_FORMAT` | `json` for structured logs (recommended for production aggregation); any other value (or unset) keeps the human-readable text format. |
| `PINCHER_LOG_LEVEL` | One of `debug`, `info`, `warn`, `error`. Defaults to `info`. Applied to every `slog` call site. |
| `PINCHER_TRACE_INDEX_FILES` | Set to `1` to add a per-file child span on the `pincher.index.run` OTLP trace. Opt-in: span volume scales with file count; off by default to keep traces shaped for whole-pass analysis rather than per-file noise. |

**Resource-pressure tuning** (no flag equivalent):

| Variable | Effect |
|---|---|
| `PINCHER_WATCHER_MEMORY_BACKOFF_MIB` | Available-memory floor (MiB) below which the file-watcher skips its 5-second tick instead of risking an OOM kill mid-extraction (#1572). Defaults to `512`. A skipped tick logs `pincher.watcher.memory_backoff` at `warn`. Reading is Linux-only (`/proc/meminfo` `MemAvailable`) this release; on macOS / Windows the watcher never backs off (the reader reports unavailable, preserving prior behaviour). Set to `0` to effectively disable the backoff. |
| `PINCHER_PYTHON_AST_DAEMON` | Python AST extraction routes through a persistent CPython subprocess by default since v0.88 (#1626 / #1685) — one process is kept alive and reused, amortising the ~80ms process-spawn + interpreter-init across files (the #1685 bench gate measures 126× on steady-state per-file cost). Set to `0` to opt back into per-file spawn. Safe by construction: daemon output is byte-identical to the spawn path, any daemon error falls through to per-file spawn per-call, and a hung daemon is killed by a 15s timeout. |

`PINCHER_HTTP_ADDR=:0` picks a free port and the bound address is printed to stderr at startup. The Docker image sets `PINCHER_HTTP_ADDR=:8080` by default.

---

## Data directory

| Platform | Default location |
|---|---|
| Windows | `%APPDATA%\pincherMCP\pincher.db` |
| macOS | `~/Library/Application Support/kwad77/pincher.db` |
| Linux | `~/.local/share/kwad77/pincher.db` |

Override with either:

- **`--data-dir /custom/path`** — passed to any `pincher` subcommand. The path is used verbatim (no `pincherMCP` suffix appended); the directory is auto-created if missing.
- **`PINCHER_DATA_DIR=/custom/path`** — same effect at the env-var level. Useful when wiring pincher into an MCP host config that already passes an `env:` block (e.g. VS Code's `.vscode/mcp.json` per the [VS Code Copilot tutorial](../tutorials/vscode-copilot.md#4-register-pincher-as-an-mcp-server) — a per-target `PINCHER_DATA_DIR` keeps each editor's pincher session counters isolated). `--data-dir` wins when both are set.

Back up with any file copy tool.
