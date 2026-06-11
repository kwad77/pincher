# Token-savings methodology

This document is the falsifiable procedure behind pincher's token-savings claims. Every "Nx" number in `README.md`, `docs/index.html`, or marketing copy traces back to a run produced by `scripts/reproduce-savings.sh` against the methodology described here.

If you read a savings claim and want to reproduce it: clone the repo, run the script, compare the output against the published figure. The script captures the exact corpus, query workload, and measurement procedure pincher uses internally. Numbers within ±10% of published figures count as confirmation; differences beyond that should be filed as issues.

## What "tokens saved" means

For every MCP tool call, pincher returns a `_meta.tokens_used` value (the bytes of the JSON response, divided by 4 — the heuristic that approximates a tokenizer for charge-back accounting) and a `_meta.tokens_saved` value. `tokens_saved` is the difference between:

- **Actual** — bytes of the pincher response, divided by 4.
- **Baseline** — bytes an agent would have read WITHOUT pincher to obtain the same information.

The baseline isn't theoretical — every tool has a `baseline_method` registered in `internal/server/server.go baselineMethodForTool`. Three baseline shapes cover all 30 tools:

| Baseline method | Tools | What "baseline" measures |
|---|---|---|
| `full_file_read` | `symbol`, `symbols`, `context`, `neighborhood`, `search`, `trace`, `query`, `dead_code`, `audit_unused`, `plan_change`, `onboard_module`, `investigate_failure`, `context_for_task`, `why_empty` | The total bytes of every file pincher's response references. Models the "agent grepped + read each file end-to-end" path. |
| `index_summary` | `index` | The bytes of the human-readable index summary an agent without pincher would have to construct from `git ls-files` + `wc -l` etc. |
| `none` | `health`, `stats`, `doctor`, `list`, `architecture`, `schema`, `init`, `rebuild_fts`, `self_test`, `fetch`, `guide`, `adr`, `changes` | Tools whose information has no obvious file-read substitute. `tokens_saved` reports `0` — these tools win on convenience, not on savings. |

The full mapping is the source of truth at `internal/server/server.go:baselineMethodForTool`. Tests in `internal/server/server_test.go` confirm every registered tool has a classification.

## Token counting

Two modes, both documented in the reference docs:

### Cheap mode (default — `PINCHER_TOKEN_ACCOUNTING=cheap` or unset)

Bytes ÷ 4. Used by every per-call `_meta.tokens_used` stamp by default. Trades ±15% per-call accuracy for ~60% allocator reduction on the auth path (the cheap heuristic shipped in #1320, v0.69 perf hardening).

When to use: dashboard cumulative totals, session-flush aggregates, this script in its default mode. The error compounds toward the mean — over 50+ calls, total savings line up within ±3% of the exact-BPE total.

### Exact mode (`PINCHER_TOKEN_ACCOUNTING=exact`)

cl100k_base BPE — the same tokenizer family Claude uses. Restores accurate per-call counts. Set at server start (env var; no runtime switch).

When to use: validating a single tool call's savings claim, comparing pincher vs an external comparator, publishing per-tool perf numbers.

The reproducer script defaults to **exact mode** for the published figures, since N=1 published numbers can't average away cheap-mode noise.

## The reproducer

`scripts/reproduce-savings.sh` runs end-to-end on a checkout of this repo. Three phases:

1. **Setup.** Builds `pincher` from current `HEAD`, indexes this repo, captures schema + binary version stamps.
2. **Measurement.** Runs `pincher bench` with `PINCHER_TOKEN_ACCOUNTING=exact` and N=50 samples. Captures per-tool p50/p95 latency, average actual bytes, average baseline bytes, average savings percent.
3. **Comparison.** Prints a table comparing the run against the published numbers committed alongside this methodology. Drift beyond ±10% prints a warning.

The published numbers live in `testdata/savings-baseline.json` and are regenerated on each release tag.

### Sample output

```
=== Token-savings reproducer ===
Binary: pincher v0.85.0 (HEAD)
Project: pincher-repo (this repo)
Schema: v33
Tokenizer: cl100k_base BPE (exact)
Samples: 50 per tool shape (search / context / trace)

Tool      | Avg actual | Avg baseline | Savings % | p50 latency | p95 latency
----------|-----------:|-------------:|----------:|------------:|------------:
search    |       420  |       45000  |    99.1%  |       2 ms  |       8 ms
context   |      1200  |       28000  |    95.7%  |       3 ms  |      11 ms
trace     |      2400  |      120000  |    98.0%  |      12 ms  |      45 ms

Published baseline (v0.85.0 tag, testdata/savings-baseline.json):
  search:  98.5-99.5%   ✅ within band
  context: 94.0-97.0%   ✅ within band
  trace:   96.5-99.0%   ✅ within band
```

Re-runs on the same repo at the same tag are reproducible because pincher indexes deterministically and `pincher bench --seed N` pins the sample set.

## What we DON'T claim

- **Universal savings.** Numbers above are for `pincher-repo` (Go-heavy, ~700 files). A Python-only project or a TypeScript monorepo will produce different numbers. The reproducer's numbers are this-repo-specific; comparable numbers for your own repo come from running `pincher bench` against your indexed project.
- **Real-world tokenizer parity.** cl100k_base is one tokenizer family. Other LLM token counts (Gemini's, the o-series, Llama's) differ by a few percent. We pick cl100k_base because it's open, well-documented, and matches the family pincher is most often consumed against.
- **End-to-end agent-workflow savings.** This methodology measures per-tool input substitution. A real agent loop also has reasoning costs, instruction-prompt overhead, and multi-turn conversation costs — none of which pincher addresses directly. The savings here are upper-bound on the input-side reduction.

## How to add a new published claim

Every claim in `README.md`, `docs/index.html`, or marketing copy that quotes a number MUST:

1. Reference this methodology doc.
2. Map to a row in `testdata/savings-baseline.json` (regenerated at release tag).
3. Be expressible as a value the reproducer can produce.

A claim that doesn't pass those three checks is removed at the next release-prep audit. The drift gate in CI (one of the v0.85+ adds — `TestSavingsClaimsHaveMethodologyReference`) fails when README quotes a percent or ratio without linking here.

## Refs

- Issue: [#1520 (FILE-A)](https://github.com/kwad77/pincher/issues/1520)
- Source: `internal/server/server.go:baselineMethodForTool`
- Bench CLI: `pincher bench` ([Reference → pincher bench](../reference/cli.md#pincher-bench))
- External comparator follow-up: FILE-B (#1521 v0.86) — head-to-head vs raw Read+Grep agent loop
- Per-tool latency budget follow-up: FILE-C (#1522 v0.91) — p50/p99 CI gate
