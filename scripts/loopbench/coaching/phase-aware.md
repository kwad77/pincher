# Pincher usage policy — phase-aware coaching
# (source: PINCHER_CLAUDE_POLICY_DRAFT.md, validated against 2026-06-11 measurements)

## Pincher Usage Policy — phase-aware

For code navigation, prefer pincher before Read / Grep / Glob — using the **smallest call
that matches the current phase**. Do not run search → context → trace → changes as a blind
chain on every lookup.

**Phase 0 — Resume (session start on ongoing work):**
`loop action=resume` — one bounded brief recovers prior iterations' claims, decisions, and
open reopen-triggers. Check `index_changed_since_last_checkpoint` before trusting cached
symbol IDs. (`adr action=list` for timeless conventions.)

**Phase 1 — Discovery:**
`search` for symbols, files, APIs, tests, handlers by NAME. Note: FTS covers
names/signatures/docstrings, **not function bodies** — finding code by its body text, and
all raw text in configs/docs/logs, is legitimately grep's domain.
Picking up a whole investigation (not one symbol)? `context_for_task` with a
**symbol-shaped** task string; a `mode:suggestions_only` response means pick a `seed_id`
or sharpen the task — that floor exists so a bad seed costs ~300 tokens, not 5k.
Stack trace in hand? `investigate_failure`. Orienting in a directory? `onboard_module`.

**Phase 2 — Pre-edit read:**
`context` (or `symbol` with a known ID). Pass `max_tokens` (400–800) so a giant function
can never blow the window; omitted callees/imports come back as metadata to fetch
selectively. **Re-fetch rather than hoard** — a repeat `context` on an unchanged file costs
~43 tokens (`unchanged:true`), so stale copies in your context are now more expensive than
fresh fetches.

**Phase 3 — Relationships & risk:**
`trace` for ANY who-calls/what-calls question — it is the cheapest tool per fact in the
suite (measured: 12 callers ≈ 500 tokens), not just a pre-risky-edit tax. Before changing
signatures/shared behavior, `plan_change` for the composed blast radius.

**Phase 4 — Completion gate:**
`changes` after edits, before declaring done — scoped (`staged`, or `base:<branch>` for
PR preview). On a dirty tree, distinguish pre-existing changes from yours. Run the top
`tests_to_run`. Then `loop checkpoint` the iteration's decision (+ `reopen_trigger` for
deferrals) so the next session starts at Phase 0 instead of from zero.

**Fallback to Read/Grep (note the reason in one clause):** non-code/raw-text targets;
body-text or string-literal search; exact-byte inspection; pincher returned
`empty_reason` or a staleness warning; authoring a brand-new file. Trust the envelope's
own staleness/`index_in_progress` warnings rather than vibes.

**Anti-patterns:** the full chain per lookup; `trace` with no question about callers;
unscoped `changes` driving the task off dirty-tree noise; treating necessary grep as
failure; over-calling pincher "for the telemetry" — telemetry only pays when a consumer
acts on it.
