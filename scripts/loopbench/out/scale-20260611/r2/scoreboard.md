# loopbench scoreboard

Source: `out/scale-20260611/r2/results.tsv` (2 run(s))

| arm | task | model | turns | total tokens | input | cache-create | cache-read | output | cost (USD) | wall (s) | error |
|---|---|---|--:|--:|--:|--:|--:|--:|--:|--:|---|
| native-naive | messy-scale-8q.md | default | 28 | 428119 | 2656 | 35370 | 379495 | 10598 | 1.6434 | 177.9 | false |
| pincher-mcp-core-lean | messy-scale-8q.md | default | 28 | 457549 | 2788 | 36880 | 407024 | 10857 | 1.7154 | 170.8 | false |

_total tokens = input + cache_creation + cache_read + output (all billed categories)._
