# Pincher best practices

Use Pincher when the question is about code structure, dependencies, callers,
or several candidate matches. Use native `rg`/`sed` for a tiny exact lookup
that can be answered in one bounded command.

## Efficient workflow

1. Index once at the start of a project session.
2. Start with `search` and keep the result page narrow.
3. Use `format:"text"` or `token_mode:"save"` for agent-facing search and trace calls.
4. Use `compact:true` when you only need IDs, paths, signatures, or line numbers.
5. Use `count_only:true` for “how many?” questions.
6. Use `context` for one selected symbol rather than reading an entire file.
7. In save mode, fetch dependency bodies selectively by returned ID.
8. Use `trace direction:"in"` before changing a public or widely called symbol.
9. Use `batch` for several related probes so intermediate results stay server-side.
10. Pass explicit `max_tokens` for large or uncertain contexts.

## When not to use Pincher

- A single exact identifier is already known and one bounded `rg` result answers it.
- The repository is not indexed or the index is stale.
- The task is purely textual replacement across known files.
- The requested data is outside the indexed project.

## Measuring actual savings

Use `PINCHER_TOKEN_ACCOUNTING=exact` while benchmarking. Compare identical
replays against a disciplined native baseline (bounded `rg` plus narrow reads),
not whole-repository reads. Track both provider-reported input tokens and
Pincher's `_meta.tokens_used`; local estimates cannot see hidden system
prompts, tool schemas, or provider prompt caching.

## Adoption checks

Install the host hook where supported and inspect `pincher hook-stats` regularly.
Review missed opportunities rather than enforcing Pincher for every command:
small lookups can be cheaper natively, while dependency-heavy and repeated
investigations are usually where Pincher earns its cost.
