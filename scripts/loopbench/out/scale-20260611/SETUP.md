# scale + repetition round — exact setup (2026-06-11)

Standing falsification gate from the messy-corpus verdict (#2002): *"the next
falsification step is scale (a corpus too large to grep-windows through) and reps
(n≥3)."* Plus the schema-diet open question from #2003: does core+lean's turn
inflation (22→34 on the small corpus) bite at scale?

## Environment

- Host: kwad77 workstation, Linux 6.17.0-1018-nvidia; all 7 runs in one sitting.
- Harness: `scripts/loopbench/run-arm.sh` at branch `bench/scale-round`
  (base: origin/master = 95a7354, post-#2003).
- `claude` CLI 2.1.173 from PATH, default model (none pinned).
- Repo under test (`repo_dir`): `/tmp/messy-scale-repo`, built by
  `scripts/loopbench/fixtures/build-messy-corpus.sh --scale 40 /tmp/messy-scale-repo`
  (deterministic — two builds diff-identical; 542 tracked files, 486 live source
  files, ~13.0k live LOC vs the base corpus's 43/904; 15 MB total incl. chaff;
  `go build ./...` passes, generated+committed Go gofmt-clean, all Python compiles,
  worker registry imports and dumps 43 handlers).

## Corpus hostility (measured)

- `grep -ri processorder .` → 9,904 matches; `grep -r process_order .` → 12,787.
- `grep -ri retry .` → **15,360 matches**.
- `grep -r CapturePayment .` → 599 matches across 40 same-shaped shard variants;
  every shard's symbols also appear in that shard's generated `.pb.go`, the shard
  fixtures, and the one-line bundle, so per-shard names are NOT grep-friendly.
- Cross-shard wiring: pkgK's pipeline captures via pkg(K+1)'s gateway and audits via
  pkg(K+1)'s store — the correct answer to "who charges a pkg21 order" lives in a
  DIFFERENT package than the question names.

## Arms

| arm | binary | data dir | schema surface | n |
|---|---|---|---|--:|
| `native-naive` | — | — | — | 3 |
| `pincher-mcp-core-lean` (arms/pincher-mcp-scale-corelean.json) | `/tmp/pincher-scale-bin` (master build, post-#2003) | `/tmp/loopbench-scale-data-cl` | `PINCHER_TOOLSET=core` + `PINCHER_SCHEMA_STYLE=lean` via MCP-config env (verified over stdio: 10 tools, 12,038-byte tools/list) | 3 |
| `pincher-mcp` full/rich (arms/pincher-mcp-scale.json) | `~/.local/bin/pincher` v1.5.0 (release) | `/tmp/loopbench-scale-data` | 34 tools, rich (overhead control) | 1 |

Both data dirs indexed fresh, identical extraction:
**527 files, 4,945 symbols, 2,384 edges** (13 oversized generated files blocked by
ShouldSkip design; ~3.2 s). Verified before the runs: no orphaned writer process
held either data dir; key graph edges (SaveRecordPkg12 inbound ×3,
CapturePaymentPkg22←ProcessOrderPkg21, RecordAuditPkg30←ProcessOrderPkg29,
ProcessOrderPkg33V1 inbound ×0) spot-checked over HTTP `/v1/trace`.

## Run commands

```sh
cd scripts/loopbench
OUT=out/scale-20260611
for r in 1 2 3; do
  ./run-arm.sh arms/native-naive.json            tasks/messy-scale-8q.md "$OUT/r$r" /tmp/messy-scale-repo
  ./run-arm.sh arms/pincher-mcp-scale-corelean.json tasks/messy-scale-8q.md "$OUT/r$r" /tmp/messy-scale-repo
done
./run-arm.sh arms/pincher-mcp-scale.json tasks/messy-scale-8q.md "$OUT/control" /tmp/messy-scale-repo
```

## Grading

Accuracy graded by hand against `tasks/messy-scale-8q.answers.md` (verified ground
truth: manual reading + pincher trace/search cross-checks + executing the Python
registry to dump live handlers). Per-question verdicts in `RESULTS-scale.md`.

## Known environmental constant

The user-global `~/.claude/CLAUDE.md` (pincher usage policy) is loaded by every arm
equally; native arms have pincher MCP tools disallowed/absent so the policy is inert
there beyond a possible wasted intent. Same condition as all prior loopbench runs.
