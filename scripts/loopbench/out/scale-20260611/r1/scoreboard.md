# loopbench scoreboard

Source: `out/scale-20260611/r1/results.tsv` (2 run(s))

| arm | task | model | turns | total tokens | input | cache-create | cache-read | output | cost (USD) | wall (s) | error |
|---|---|---|--:|--:|--:|--:|--:|--:|--:|--:|---|
| native-naive | messy-scale-8q.md | default | 15 | 303767 | 5177 | 40884 | 245665 | 12041 | 2.1420 | 250.8 | false |
| pincher-mcp-core-lean | messy-scale-8q.md | default | 31 | 581483 | 2923 | 45786 | 521956 | 10818 | 2.0078 | 182.6 | false |

_total tokens = input + cache_creation + cache_read + output (all billed categories)._
