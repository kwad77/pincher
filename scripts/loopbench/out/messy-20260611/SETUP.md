# messy-corpus ladder — exact setup (2026-06-11)

Standing reopen-trigger being answered: *"re-run the three-arm ladder on a
polyglot/generated-code corpus where naive navigation degrades."*

## Environment

- Host: kwad77 workstation, Linux 6.17.0-1018-nvidia; all arms run in one sitting.
- Harness: `scripts/loopbench/run-arm.sh` at branch `feat/messy-corpus`
  (base: origin/master = v1.5.0, commit a7f2246).
- `claude` CLI from PATH, default model per arm spec (none pinned), one run per arm
  (n=1 — treat <15% token deltas as noise per loopbench README).
- Repo under test (`repo_dir`): `/tmp/messy-corpus-repo`, built by
  `scripts/loopbench/fixtures/build-messy-corpus.sh /tmp/messy-corpus-repo`
  (deterministic; 48 tracked files, 4 commits, `go build ./...` passes,
  all committed Go gofmt-clean).

## Corpus hostility (measured)

- `grep -ri processorder .` → 63 matches across 3 languages + generated noise.
- `grep -r process_order .` → **12,627 matches** (fixtures + pb.go + bundle).
- `grep -ri retry .` → 3,589 matches.
- `order_retry_limit` appears in 5 files; the defining one is generated
  (`gen/defaults.gen.json`, value 7) with decoy fallbacks (3) in two readers and
  historical snapshot values (3/5) in fixtures.

## Pincher arm setup

```sh
rm -rf /tmp/loopbench-messy-data && mkdir -p /tmp/loopbench-messy-data   # FRESH data dir
/home/kwad77/.local/bin/pincher index /tmp/messy-corpus-repo --data-dir /tmp/loopbench-messy-data
# → indexed messy-corpus-repo: 1557 total symbols, 137 total edges, 43 files (3 blocked, ~1.8s)
#   blocked = web/dist/bundle.min.js + minified/oversized noise, by ShouldSkip design
```

- Binary: `/home/kwad77/.local/bin/pincher` → **pincherMCP v1.5.0** (release build).
- MCP config: `scripts/loopbench/mcp-pincher-messy.json` (stdio, server name
  `pincher-next`, `--data-dir /tmp/loopbench-messy-data`).
- Arm spec: `scripts/loopbench/arms/pincher-mcp-messy.json` (arm name `pincher-mcp`,
  same coaching/tool policy as the standard `pincher-mcp` arm: built-ins NOT
  disallowed, one-line `prefer-pincher.md` nudge).
- Verified before the run: no orphaned writer process held the data dir
  (single-writer lock gotcha).

## Run commands

```sh
cd scripts/loopbench
OUT=out/messy-20260611
for a in arms/native-naive.json arms/native-coached.json arms/pincher-mcp-messy.json; do
  ./run-arm.sh "$a" tasks/messy-10q.md "$OUT" /tmp/messy-corpus-repo
done
./score.sh "$OUT" | tee "$OUT/scoreboard.md"
```

## Grading

Accuracy graded by hand against `tasks/messy-10q.answers.md` (verified ground truth:
manual reading + pincher trace/search cross-checks + executing the Python registry to
dump live handlers). Per-question verdicts in `RESULTS.md`.

## Known environmental constant

The user-global `~/.claude/CLAUDE.md` (pincher usage policy) is loaded by every arm
equally; native arms have pincher MCP tools disallowed/absent so the policy is inert
there beyond a possible wasted intent. Same condition as all prior loopbench runs.
