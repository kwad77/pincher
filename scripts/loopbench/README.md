# loopbench — reproducible cross-toolset benchmark harness

Measures what a real agent session spends (tokens, cost, turns) answering the same
code-navigation task with different toolsets: built-in tools, coached grep, pincher over
MCP, pincher over MCP with phase-aware coaching, and the deprecated pincher-via-curl arm.

Every arm is a **real `claude -p` session** (`--output-format json`, `--mcp-config`,
`--strict-mcp-config`), so the treatment no longer pays the curl-harness tax the manual
rounds did, and usage comes from the CLI's own billing-grade JSON instead of estimates.

## Why

Five manual benchmark rounds (June 2026) established a three-arm ladder on the
`investigate-5q` task:

| arm (manual rounds) | tokens |
|---|--:|
| coached-grep | **27.2k** |
| naive Claude (built-ins, no coaching) | 38.8k |
| pincher-via-curl | 39.6k |

Two problems made that ladder untrustworthy: (1) the pincher arm drove the server through
curl-in-Bash, paying JSON-quoting/boilerplate/full-envelope costs a real MCP integration
never pays; (2) the runs were hand-driven and unrepeatable. loopbench fixes both.

## Layout

```
run-arm.sh                 run one arm: arm spec + task → raw JSON, answer, results.tsv row
score.sh                   results.tsv → markdown scoreboard (sorted by total tokens)
mcp-pincher-next.json      DEFAULT MCP config: stdio via /tmp/pincher-loop-bin (see below)
mcp-pincher-next-http.json HTTP variant — requires the server be started with --mcp-http-path /mcp
arms/                      five arm specs (JSON)
coaching/                  system-prompt fragments referenced by arm specs
tasks/                     task sets + how to add one (tasks/README.md)
```

## Usage

```sh
cd scripts/loopbench
OUT=out/$(date +%Y%m%d-%H%M%S)
for a in arms/native-naive.json arms/native-coached.json \
         arms/pincher-mcp.json arms/pincher-mcp-coached.json; do
  ./run-arm.sh "$a" tasks/investigate-5q.md "$OUT"        # repo_dir defaults to this repo
done
./score.sh "$OUT" | tee "$OUT/scoreboard.md"
```

`run-arm.sh <arm.json> <task.md> <outdir> [repo_dir]` — relative `mcp_config`/`coaching`
paths in arm specs resolve against `scripts/loopbench/`. Outputs per arm:
`<arm>.json` (raw CLI output), `<arm>.stderr.log`, `<arm>-answer.md`, plus an appended
`results.tsv` row.

## Arms

| arm | mcp | coaching | notes |
|---|---|---|---|
| `native-naive` | none | none | out-of-the-box baseline (pincher MCP tools explicitly disallowed) |
| `native-coached` | none | `grep-coached.md` | the manual-round winner: surgical grep/sed discipline |
| `pincher-mcp` | pincher-next (stdio) | `prefer-pincher.md` (one line) | realistic usage — built-ins NOT disallowed; measures what an agent spends with pincher merely *available* |
| `pincher-mcp-coached` | pincher-next (stdio) | `phase-aware.md` | the phase-aware usage policy (smallest call per phase) |
| `pincher-curl` | none | `pincher-curl.md` | **DEPRECATED** — curl against `http://127.0.0.1:7878/v1/<tool>`; kept only to quantify the harness tax vs. the MCP arms; needs the :7878 server running |
| `pincher-mcp-messy` (file `arms/pincher-mcp-messy.json`, arm name `pincher-mcp`) | `mcp-pincher-messy.json` (release binary, stdio) | `prefer-pincher.md` | the pincher-mcp arm wired for the **messy corpus**: `~/.local/bin/pincher --data-dir /tmp/loopbench-messy-data`; pre-index the corpus before running (see below) |

## The messy corpus (hostile-terrain benchmark)

`fixtures/messy-corpus/` is a committed polyglot fixture app (Go service + Python
workers + TS storefront with real cross-language call relationships, registry/dispatch
indirection, three same-named `ProcessOrder`/`process_order`/`processOrder` twins plus
dead duplicates). `fixtures/build-messy-corpus.sh [target]` materializes a runnable
repo (default `/tmp/messy-corpus-repo`): it copies the tree, deterministically
generates ~1.3 MB of grep-chaff (a protobuf-gen-style `.go`, a one-line 500 KB
minified bundle, machine-generated JSON fixtures — deliberately not committed here),
and creates a 4-commit git history. The corpus has its own `go.mod`, so the parent
module's `go test ./...` never sees it; `go build ./...` inside the corpus passes.

```sh
fixtures/build-messy-corpus.sh /tmp/messy-corpus-repo
pincher index /tmp/messy-corpus-repo --data-dir /tmp/loopbench-messy-data
OUT=out/messy-$(date +%Y%m%d)
for a in arms/native-naive.json arms/native-coached.json arms/pincher-mcp-messy.json; do
  ./run-arm.sh "$a" tasks/messy-10q.md "$OUT" /tmp/messy-corpus-repo
done
./score.sh "$OUT"
# then grade each <arm>-answer.md against tasks/messy-10q.answers.md
```

First run results + grading: `out/messy-20260611/RESULTS.md`.

### Scale mode (the 10x corpus)

`fixtures/build-messy-corpus.sh --scale N [target]` additionally generates N shard
copies of the order-processing cluster with DISTINCT symbol names per copy
(`internal/pkg01..pkgNN` + matching Python handlers + TS modules), cross-wired so
call chains span copies: pkgK's pipeline captures payment through pkg(K+1)'s gateway
and audits through pkg(K+1)'s store (the last shard terminates on base billing).
Chaff scales proportionally — one generated `.pb.go` per shard that mentions that
shard's symbol names, N/4 extra fixture dumps, a bigger one-line bundle — so the
distinct names do NOT make grep friendly again. `--scale 0` (default) is
byte-identical to the original 43-file corpus.

```sh
fixtures/build-messy-corpus.sh --scale 40 /tmp/messy-scale-repo   # 542 files, ~13k live LOC
pincher index /tmp/messy-scale-repo --data-dir /tmp/loopbench-scale-data
# core-lean treatment arm needs a schema-diet binary (post-v1.5.0):
go build -o /tmp/pincher-scale-bin ../../cmd/pinch
/tmp/pincher-scale-bin index /tmp/messy-scale-repo --data-dir /tmp/loopbench-scale-data-cl
for a in arms/native-naive.json arms/pincher-mcp-scale-corelean.json; do
  ./run-arm.sh "$a" tasks/messy-scale-8q.md out/scale-$(date +%Y%m%d)/r1 /tmp/messy-scale-repo
done   # repeat per rep; arms/pincher-mcp-scale.json is the full/rich overhead control
```

Scale-round results + grading + the two verdicts: `out/scale-20260611/RESULTS-scale.md`.

## MCP config: stdio is the default (and why)

The intended config was streamable-HTTP against the long-running patched server at
`http://127.0.0.1:7878`. That instance was started **without** `--mcp-http-path`, so the
MCP transport is not mounted; `POST /mcp` falls through to the REST tool dispatcher:

```
{"error":{"code":"not_found","message":"unknown tool \"/mcp\"", ...}}
```

The transport itself is fine — a probe instance launched as
`pincher --no-stdio --http 127.0.0.1:7879 --mcp-http-path /mcp` answered an MCP
`initialize` over SSE correctly. So:

- **`mcp-pincher-next.json` (default)** uses stdio: `/tmp/pincher-loop-bin
  --data-dir /tmp/loopbench-pincher-data`. A dedicated data dir avoids pincher's
  single-writer lock on `/tmp/pincher-loop-data` (held by the :7878 process). Verified
  end-to-end with claude CLI (smoke below).
- **`mcp-pincher-next-http.json`** points at `http://127.0.0.1:7878/mcp` and works only
  once that server is restarted with `--mcp-http-path /mcp` (docs/streamable-http.md).

First pincher-arm run against a cold data dir pays index time (wall clock, not tokens);
pre-warm by running the smoke task once before benchmarking.

## Smoke results (2026-06-11, both passing)

| arm | task | answer | turns | total tokens | cost |
|---|---|---|--:|--:|--:|
| native-naive | smoke-modpath | `github.com/kwad77/pincher` (correct) | 2 | 52,335 | $0.276 |
| pincher-mcp | smoke-health | `schema_version: 40` (correct, via stdio MCP) | 3 | 82,808 | $0.515 |

### Observed `claude -p --output-format json` shape (CLI 2.1.173)

Top-level keys used by the harness:

```json
{
  "type": "result", "subtype": "success", "is_error": false,
  "duration_ms": 8811, "num_turns": 2,
  "result": "<final answer text>",
  "session_id": "…", "total_cost_usd": 0.276075,
  "usage": {
    "input_tokens": 2380,
    "cache_creation_input_tokens": 10357,
    "cache_read_input_tokens": 39485,
    "output_tokens": 113,
    "server_tool_use": {"web_search_requests": 0, "web_fetch_requests": 0},
    "service_tier": "standard",
    "cache_creation": {"ephemeral_1h_input_tokens": 10357, "ephemeral_5m_input_tokens": 0},
    "iterations": [ { "...per-iteration usage..." : 0 } ]
  },
  "modelUsage": { "<model-id>": { "...per-model usage and cost..." : 0 } }
}
```

`results.tsv` totals = `input + cache_creation + cache_read + output` (all billed
categories). Per-tool-call counts are not in the `json` envelope; re-run with
`--output-format stream-json` and count `tool_use` events if call counts are needed —
`num_turns` is the available proxy.

## Honest methodology notes

- **The baseline is coached-grep (27.2k), not naive Claude.** Beating 38.8k while losing
  to 27.2k means pincher still loses to a well-coached agent on this task shape; report
  against both.
- **n=1 per cell** unless you re-run. Model sampling, cache state, and index warmth all
  move the numbers; treat single-run deltas under ~15% as noise. Run ≥3 reps for claims.
- **Same-task-shape caveat:** `investigate-5q` is read-only investigation on one repo
  (this one) at one commit. `graph-12q` is deliberately pincher-favourable terrain.
  Neither covers edit loops. Don't generalize a win on one shape to "pincher saves X%".
- **Session overhead is included on all arms** (system prompt, CLAUDE.md, tool schemas —
  visible as large `cache_read`). That's intentional: MCP tool schemas are a real cost of
  having pincher attached, and identical overhead cancels in arm-vs-arm deltas only if
  you keep the environment fixed. Run all arms from the same machine/config in one
  sitting. `--strict-mcp-config` keeps user-level MCP servers (including the dev pincher)
  out of every arm.
- **Grade the answers.** Token counts mean nothing if an arm answered wrong; eyeball each
  `<arm>-answer.md` against the repo before quoting the scoreboard.
- The pincher arms intentionally do **not** disallow built-in Grep/Glob/Read — the
  question is what an agent spends with pincher available and a nudge, not what it spends
  when forced.
