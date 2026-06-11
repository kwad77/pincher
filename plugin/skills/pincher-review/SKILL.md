---
name: pincher-review
description: Blast-radius-first code review driven by pincher's impact graph. Use when reviewing a PR, a branch, or local edits — "review this PR", "what does this diff break", "is this change safe to merge". The doctrine inverted from conventional review: interrogate the impact graph BEFORE reading the diff text — the diff tells you what changed; the graph tells you what it breaks.
version: 0.1.0
---

# Pincher Review

A diff is a claim about a change; the call graph is the evidence about its
consequences. Review the consequences first. Diff-text-first review finds style
issues and misses the caller two modules away whose contract just broke.

## Preload

```
ToolSearch query="select:mcp__pincher__changes,mcp__pincher__plan_change,mcp__pincher__trace,mcp__pincher__context,mcp__pincher__search,mcp__pincher__health"
```

(`verify_change` / `assert_graph` / `count_only` / `batch` exist from schema
v40/v41 servers; on older servers degrade to the atomic fallbacks named below.)

## The sequence — graph before text

| # | Step | Call | What you're extracting |
|---|---|---|---|
| 1 | Scope the change | `changes scope=base:<branch>` for a PR/branch; `unstaged`/`staged`/`all` for local work | `changed_symbols`, impact tagged CRITICAL/HIGH/MEDIUM/LOW, ranked `tests_to_run` |
| 2 | Interrogate the hot symbols | `plan_change` on each CRITICAL/HIGH symbol | Predicted blast radius per symbol — what the *change* claims vs what the graph says it touches |
| 3 | Size caller exposure | `trace` depth 1-2 on the hot symbols; `count_only` (v40+) when the question is "how many" | 12 callers ≈ 500 tok; a count ≈ 29 tok — don't pay row-shape for a number |
| 4 | Check claimed invariants | `assert_graph` (v40+) for each invariant the diff description asserts ("nothing else calls this", "only tests use X"); fallback: `trace` and count by hand | 32-token verdicts; a failed assert is a finding, verbatim |
| 5 | Only now, read the diff | native `git diff` / Read — with the graph in hand you know *which hunks matter* | Correctness of the hot hunks; skim the rest |
| 6 | Executed evidence | `pincher test-impacted` (where available) or run the top `tests_to_run` from step 1 | "Tests pass" is a claim until the *impacted* tests ran |

Budget rule from the loop doctrine applies: every source-carrying call gets
`max_tokens`; review reads use `context` with a 400-800 budget, not whole-file
Reads, except where the carve-outs hold (test-assertion bodies, configs, CI).

## The verdict — structured, labeled

Findings come in exactly two flavors and each says which it is:

- **measured** — backed by a graph query, an assert verdict, or an executed
  test. ("`trace` shows 9 non-test callers of `ParseConfig`; 2 are not in the
  diff and still pass the old argument — breaks at runtime.")
- **inferred** — judgment from reading. ("This error path looks unreachable" —
  if it matters, promote it: write the `assert_graph`/test that would settle it.)

Verdict shape: blocking findings (measured first) → non-blocking → what was
*verified* (asserts run, tests executed, caller counts) so the approval is
auditable, not vibes. An approval that lists no verifications is a skim, and
should say "skim".

## Anti-patterns

- **Diff-text-first.** Reading hunks top-to-bottom before any graph call — you
  review what the author showed you, weighted by line count instead of risk.
- **Trusting the PR description's reach claims.** "Pure refactor, no behavior
  change" is an `assert_graph`/`trace` query, not a fact.
- **Whole-suite-or-nothing testing.** The ranked `tests_to_run` exists so
  executed evidence is cheap; running nothing because running everything is
  slow is the false economy `changes` removes.
- **Unlabeled findings.** A measured break and a style hunch presented in the
  same voice — the author can't triage what the reviewer didn't grade.

## Exit

Verdict delivered with labels + the verification list. For multi-round reviews,
checkpoint the round's verdict (pincher-loop's ledger, v40+) so round 2 starts
from findings, not from re-deriving the blast radius.
