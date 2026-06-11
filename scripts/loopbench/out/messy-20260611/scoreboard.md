# loopbench scoreboard

Source: `out/messy-20260611/results.tsv` (3 run(s))

| arm | task | model | turns | total tokens | input | cache-create | cache-read | output | cost (USD) | wall (s) | error |
|---|---|---|--:|--:|--:|--:|--:|--:|--:|--:|---|
| native-naive | messy-10q.md | default | 36 | 350388 | 2783 | 28289 | 312091 | 7225 | 1.2670 | 136.9 | false |
| pincher-mcp | messy-10q.md | default | 22 | 511289 | 3004 | 46484 | 453585 | 8216 | 1.8241 | 166.2 | false |
| native-coached | messy-10q.md | default | 26 | 526093 | 2664 | 26317 | 489784 | 7328 | 1.4092 | 173.8 | false |

_total tokens = input + cache_creation + cache_read + output (all billed categories)._
