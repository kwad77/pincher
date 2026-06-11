---
name: pincher-debug
description: Failure triage driven by pincher's evidence-ranked investigation. Use when a test fails, a build breaks, a stack trace lands, or a bug report arrives with concrete error output — "why is this failing", "debug this stack trace", "this test broke". The discipline is evidence-first - feed the raw error to investigate_failure and follow its per-suspect ranking - instead of hypothesis-first grepping.
version: 0.1.0
---

# Pincher Debug

The expensive debugging failure is not the bug — it's the *investigation
method*: forming a hypothesis from the error's vibes, grepping for it, and
reading whole files of every match. This skill replaces that with one composite
call that ranks suspects by evidence, then spends tokens **proportional to
suspicion**.

## Preload

```
ToolSearch query="select:mcp__pincher__investigate_failure,mcp__pincher__context,mcp__pincher__trace,mcp__pincher__changes,mcp__pincher__search,mcp__pincher__health"
```

(`verify_change` / `detail=skeleton` / `loop` exist from schema v40/v41
servers; on older servers degrade as noted per step.)

## The sequence

| # | Step | Call | Discipline |
|---|---|---|---|
| 1 | Feed the failure, raw | `investigate_failure error_text=<the verbatim error/stack trace>` | Paste it uncut — paths, line numbers, quotes are what the ranker keys on. Summarizing the error first throws away the evidence. |
| 2 | Trust the ranking | read the per-suspect evidence, in order | Each suspect comes with *why* (trace hit, recent change, name match). Overriding the ranking requires naming the evidence it missed — "I have a feeling" doesn't count. |
| 3 | Skim the suspects, cheap | `context detail=skeleton` (v40+) on the top 2-3 suspects; fallback `context max_tokens=400` | Skeleton ≈ 0.21× full cost — enough to rule a suspect in/out, not to read its soul. |
| 4 | Go deep on ONE | full `context` (budgeted 800ish) on the prime suspect only; `trace` its callers if the bug needs a path to the failure site | Full source is the most expensive read in the loop — it's earned by ranking + skim, not dealt to every suspect. |
| 5 | Fix | edit; mechanical multi-site fixes via native Edit/sed after pincher located the sites | |
| 6 | Gate | `verify_change` (v40+) or `changes` → run the top `tests_to_run` (+ `pincher test-impacted` where available), including the originally-failing case | The original failure passing is necessary; the impacted set passing is what makes it a fix rather than a displacement. |
| 7 | Capture the root cause | `loop action=checkpoint` (v40+; fallback `adr set` for timeless gotchas) — claim: the failure; decision: root cause + fix, self-contained with exact names/file:line | Root causes recur. A checkpointed one costs the next session one resume; an unchecked one costs the whole investigation again. Evict after capture — refer to it by pointer. |

**Surprise rule** (from the loop's Stage 4): if the fix works but you can't say
*why the error pointed where it did*, the root cause isn't found — you've
treated a symptom. Explain the surprise or label the fix `inferred` and ship a
reopen trigger with it.

## Anti-patterns (each one is the step it skips)

- **Hypothesis-first grepping.** Grep for words from the error before
  `investigate_failure` — you find where the *message* lives, not what *caused*
  it, and you anchor on the first plausible hit (skips steps 1-2).
- **Whole-file reads of every suspect.** N suspects × whole files is the
  quadratic blow-up; ranking + skeleton exists to make spend proportional to
  suspicion (skips step 3).
- **Fixing at the stack-trace frame.** The top frame is where the error
  *surfaced*; `trace` toward callers to find where the bad value was *born*.
- **Declaring victory on the one test.** Re-running only the failing test and
  skipping the impacted set — the classic displacement bug (skips step 6).
- **Root cause left in chat.** The investigation's conclusion not checkpointed
  — next regression pays full price again (skips step 7).

## Exit

Original failure green, impacted tests green, root cause checkpointed with a
confidence label. If the cause was a *class* of bug (convention violation,
missing invariant), consider whether the prescription should migrate down the
stack — a hook or server default that catches it beats prose that remembers it.
