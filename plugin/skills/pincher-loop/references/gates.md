# Directional-decision gates: when to pause a running loop

The loop is **self-continuing**. You pause for exactly two things. Everything else
is a Stage-6 decide-or-defer-with-trigger that you make yourself and keep moving.

## 1. Permission / approval wall

Stop and ask before a **risky or irreversible action outside the routine delivery
envelope**:
- force-push to `main`/`master`; deleting unmerged branches or work
- `git reset --hard` / destructive resets / overwriting uncommitted changes
- publishing a **stable** release (tags/RCs on a feature line are routine)
- external communications (Slack/email/issue comments visible to others) beyond
  the PRs the kick-off authorized
- any harness-gated tool the permission layer blocks

**Pre-authorized by the kick-off (do NOT pause):** feature branches, commits,
pushing feature branches, opening PRs, watching/merging green CI, squash-merging
your own green PR, creating issues/milestones, re-indexing.

When a harness gate blocks something routine (e.g. an auto-mode classifier denies a
force-push needed for a DCO amend), surface it once, concisely, with the exact
command and why — then let the user authorize.

## 2. Bonafide directional decision

A genuine fork the **evidence cannot settle** that would alter direction and is the
user's to make. These are **rare**. Signals that you've hit one:

- **Crossing a documented bar.** A measured result exceeds a threshold the project
  wrote down (an ADR's "<2% parse-error" promotion bar, a binary-size budget, a
  latency SLO). Even if you believe the bar should be re-interpreted (e.g. graceful
  fallback makes it a non-regression), the re-interpretation is the user's call —
  present it.
- **A real, non-obvious cost.** A jump the kick-off didn't anticipate (a +6 MB
  binary, a 30× indexing slowdown, a new heavyweight dependency).
- **A regression with no graceful fallback.** If you can't make it degrade to
  today's behavior, that's not a Stage-7 gate you can pass alone.
- **Scope surprise.** The task turns out materially larger/different than scoped
  (e.g. "clean flip" becomes "reimplement the whole convention surface"). Re-confirm
  before sinking the extra effort.
- **A choice with no evidence-based winner.** Two viable paths, tradeoffs only the
  user can weigh (which language next; one bundled PR vs many).

## How to phrase the pause

- Lead with the **measured evidence** and the specific fork — not a vague "should I
  continue?"
- Give a **recommendation** with the one-line tradeoff, so the user can ratify in a
  word rather than design from scratch.
- Offer **concrete options** (an `AskUserQuestion` with 2-4 mutually-exclusive
  choices works well), recommended option first.
- Make clear what you'll do on each choice, and that the loop resumes after.

Example shape:
> "[S6] Measured X on a real corpus: <number> vs the ADR's <bar>. The per-file
> fallback means it's not a regression, but it crosses the documented bar — your
> call. Recommend: ship default-ON + re-document the bar (because …). Alt: ship
> default-OFF / hold. Which?"

## What is NOT a gate (decide it yourself, keep moving)

- Matching an existing convention you discovered mid-loop (just match it).
- A failing test with a clear fix (fix it; that's Stage 4→7, not a pause).
- Choosing a variable name, a helper's shape, whether three similar lines need an
  abstraction (they usually don't).
- A deferral with an obvious re-open trigger (record it via `adr`, move on).
