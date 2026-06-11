# loopbench scoreboard

Source: `out/scale-20260611/r3/results.tsv` (2 run(s))

| arm | task | model | turns | total tokens | input | cache-create | cache-read | output | cost (USD) | wall (s) | error |
|---|---|---|--:|--:|--:|--:|--:|--:|--:|--:|---|
| pincher-mcp-core-lean | messy-scale-8q.md | default | 20 | 386707 | 2784 | 39037 | 332243 | 12643 | 1.7730 | 186.7 | false |
| native-naive | messy-scale-8q.md | default | 22 | 421922 | 2656 | 26829 | 382621 | 9816 | 1.4366 | 163.0 | false |

_total tokens = input + cache_creation + cache_read + output (all billed categories)._
