# loopbench task sets

A task set is a single markdown file passed verbatim as the `claude -p` prompt by
`run-arm.sh`. Current sets:

| file | shape | notes |
|---|---|---|
| `investigate-5q.md` | 5 mixed investigation questions | The original manual-round task: config caps, callers, test coverage, wire formats, feature archaeology. |
| `graph-12q.md` | 12 who-calls / what-calls / blast-radius questions | Call-graph-heavy — pincher's home turf (`trace` answers most of them in one call each). |
| `smoke-modpath.md` | 1 trivial question | Harness smoke only; not a benchmark. |
| `smoke-health.md` | 1 trivial MCP call | MCP-wiring smoke only; not a benchmark. |

## Adding a task set

1. Write `tasks/<name>.md`. Rules of thumb:
   - Address "THIS repository" explicitly and forbid file modification, so arms stay
     read-only and comparable.
   - Ask for file:line evidence — it makes grading the answers tractable.
   - **Verify every symbol you name actually exists at the pinned commit** (questions
     about phantom symbols measure hallucination handling, not navigation cost).
   - Keep the question count fixed across arms; never tailor wording per arm.
2. Run every arm against it into one outdir:
   ```sh
   for a in arms/*.json; do ./run-arm.sh "$a" tasks/<name>.md out/<name>/; done
   ./score.sh out/<name>/
   ```
3. Grade answers manually (`out/<name>/<arm>-answer.md`) before trusting the token
   numbers — a cheap wrong answer is not a win.

## Caveats baked into the current sets

- Both sets are *navigation/comprehension* tasks. Edit-loop tasks (fix a bug, land a
  refactor) would exercise `changes`/`plan_change` and are not yet represented.
- `graph-12q.md` is intentionally favourable terrain for pincher; `investigate-5q.md`
  is the neutral task. Report both, never just one.
