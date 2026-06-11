---
name: pincher-loop
description: Run a non-trivial engineering task as a context-frugal, evidence-gated delivery loop driven by pincher's code-intelligence tools. Use when the user says "kick off a loop", "loop through X", "ship X then keep going", or asks for an iterative deliver-measure-decide workflow over a codebase — especially multi-step work (refactors, rollouts, bug-hunts, migrations) where re-acquiring context each iteration would burn the window. Portable across any repo pincher can index. v0.3 binds each loop stage to the cheapest pincher call that answers it (phase-aware, measured 2026-06-11), persists loop state in the ledger, and uses native Read/Grep where they are genuinely the better tool.
version: 0.3.0
---

# Pincher Loop (v0.3 — phase-aware)

A delivery loop where **context economy is a first-class constraint**. Discipline:
the Evidence-Gated Delivery Loop (EGDL). Efficiency: the **smallest pincher call
that matches the current phase** — never the full chain by habit — plus the
**loop ledger** so iteration N+1 starts from state, not from re-reading.

> v0.3 replaces v0.2's "composite-first" rule. Five measured benchmark rounds
> (2026-06-11) showed composites win when picking up a *cluster*, atomic calls
> win for single facts, `trace` is the cheapest tool per fact for any
> caller/callee question, and native Read/Grep legitimately win raw-text and
> body-text work. Use each where it wins; the ledger and watermark are what no
> native tool can offer.

## Opening script — run before any other tool

**Step 0 — preload** (pincher MCP tools are deferred; calling unfetched schemas
fails):

```
ToolSearch query="select:mcp__pincher__health,mcp__pincher__index,mcp__pincher__adr,mcp__pincher__loop,mcp__pincher__batch,mcp__pincher__search,mcp__pincher__symbols,mcp__pincher__context,mcp__pincher__trace,mcp__pincher__context_for_task,mcp__pincher__plan_change,mcp__pincher__changes,mcp__pincher__investigate_failure"
```

(`loop`/`batch` exist from schema v40; on older servers skip them.)

**Step 1 — resume state**: `loop action=resume` (most recent loop, or by name).
This is the loop's memory: prior claims, decisions, open reopen-triggers, and
`index_changed_since_last_checkpoint` (if true, distrust cached symbol IDs and
re-probe). On a brand-new effort: `loop action=start name=<slug> claim="<the
falsifiable goal>"`. Then `adr action=list` for the repo's conventions/recipes.

**Step 2 — verify the index**: `health`. Errors/empty → `init` + `index`.
Re-`index` after large edits; watch `_meta.watermark` — the `g<N>` generation
changing is the signal that prior graph answers may have shifted.

**Step 3 — orient, phase-aware** (table below). Only now enter the stages.

## Phase → cheapest call (the core of v0.3)

| You need | Reach for | Not |
|---|---|---|
| Resume prior work | `loop resume` | re-reading transcripts/files |
| One symbol's location | `search` (fields=id,name,file_path) | context_for_task |
| Who calls X / what does X call | **`trace`** (compact, depth 1-2) — measured cheapest tool per fact | grepping for callers |
| Several facts at once | **`batch`** — N sub-queries, one envelope, shared max_tokens | N single calls (each pays harness ceremony) |
| Pick up a whole investigation | `context_for_task` (symbol-shaped task; a `suggestions_only` response means pick a seed_id, don't force) | reading 5 files |
| Read a function before editing | `context` with `max_tokens` 400-800 | unbounded context; whole-file Read |
| Re-read something you saw before | just re-fetch — repeat `context` ≈ 43 tokens (`unchanged:true`) | hoarding stale copies in your window |
| Pre-edit blast radius | `plan_change` | manual trace+changes chain |
| Post-edit gate | `changes` (+ `verify_change` where available) → run the top `tests_to_run` | declaring done on build alone |
| Stack trace in hand | `investigate_failure` | search×N by hand |
| Raw text / configs / body-text search / string literals | **native Grep/Read** — FTS indexes names/signatures/docstrings, NOT bodies | forcing pincher |
| Mechanical multi-site edit | native sed/Edit after pincher located the sites | API round-trips per site |

**Budget rule:** every source-carrying call gets `max_tokens`; every batch gets
a shared budget. A loop that can't bound a call can't bound an iteration.

**Trip-wire (revised):** before a code-navigation Read/Grep, name the pincher
call you considered and why it loses here. Valid reasons now include "body-text
/ raw-text target" and "edit mechanics" — but *"I forgot trace exists"* is the
failure v0.2's wire caught most, and it still counts.

## The loop stages

Run the EGDL stages (references/egdl-stages.md). Stage bindings:

| Stage | Reach for |
|---|---|
| **Frame** | `loop resume` + `adr list`; `onboard_module` only on unfamiliar directories |
| **Probe** | `batch` (the N questions you actually have) or `context_for_task` for a cluster |
| **Decide** (pre-edit) | `plan_change` |
| **Root-cause** | `investigate_failure error_text=...` |
| **Gate** (post-edit) | `changes`/`verify_change` → run the top `tests_to_run` |
| **Capture** | **`loop checkpoint`** {claim, decision, confidence, reopen_trigger, evidence}; `adr set` only for timeless conventions/recipes |

**Checkpoint discipline:** the next iteration may be a fresh session whose only
memory is your checkpoint. Write `decision` so it stands alone (exact names,
numbers, file:line). Deferrals MUST carry `reopen_trigger` — resume surfaces
them until addressed.

**Evict-after-capture:** once material is checkpointed, refer to it by pointer
(`<loop>#<seq>`, ADR key, symbol id) and never re-paste it — the window holds
the pointer table, the substrate holds the payloads. Re-fetch is cheap
(`loop resume`, `context` ≈ 43 tokens on unchanged files); hoarding evictable
payloads through a compaction is how loops die.

## Continuation & stop rules (self-continuing)

Once kicked off the loop self-continues; do not halt at iteration boundaries to
ask "continue?". Stop only for (1) a permission/approval wall or a genuinely
risky/irreversible action outside the routine delivery envelope, or (2) a
bona-fide directional fork that is the user's to make — present it crisply with
a recommendation. Routine branches/commits/PRs/CI are pre-authorized by the
kick-off.

## Standing rules

- **Confidence labels** on load-bearing claims: `measured | inferred | assumed`;
  a decision resting on a number goes and gets the number first — and verify
  the baseline comes from the *current* index generation (a stale-index
  baseline corrupted a real audit on 2026-06-11; the watermark exists to catch
  exactly this).
- **Every Defer ships a re-open trigger.**
- **Graceful degradation is structural**: new capability behind a fallback.
- **Reference the stage** in updates: `[S3: Probe]`, `[S6: Decide — measured]`.
- **Honest scoreboards**: when measuring pincher against alternatives, name the
  baseline (naive agent vs expert-frugal) — they differ by 2-10x and the claim
  changes with them.

## Cross-project portability

The discipline is universal; project knowledge lives in pincher's per-project
`adr` store and `loop` ledger, not in this skill. `loop resume` + `adr list`
at Frame; `loop checkpoint` + (sparingly) `adr set` at Capture.

## Loop-engineering blocks (Greyling, 2026 — adopted 2026-06-11)

The loop is a control system, not a chat pattern. Three rules this skill
enforces beyond the stages:

- **Maker/checker split, designed in — not bolted on.** Implementation agents
  never grade their own wave: every parallel wave ends with an independent
  verification agent (different instructions; integration-merge + full suite +
  live smoke). A maker's "tests green" is a claim until a checker reproduces it.
- **The human queue is explicit.** Anything blocked on the user (merge
  decisions, permission-denied actions, directional forks) gets a checkpoint
  with `reopen_trigger: "AWAITING HUMAN: <decision>"` so every `resume` —
  including the heartbeat's — surfaces the queue instead of letting blocked
  items rot in chat history.
- **The heartbeat outlives the session.** A session-bound loop dies with its
  window. For multi-day work, pair the ledger with a scheduled routine
  (`/schedule` / cron) whose triage is conclusion-density cheap (`health`,
  `count_only`, `assert_graph`, PR-CI checks) and which spawns real agents only
  on actionable findings, checkpointing what it did.

## The 30k-foot review (run at wave boundaries — non-optional)

Every several iterations, after any parallel wave lands, or on user cue: zoom
all the way out before launching anything new. Inputs: the FULL ledger
(`loop resume` with a generous budget, or `loop list`), the open-PR/branch
inventory, and the stated goal verbatim. Ask, in order:
1. **Direction** — does the recent work still serve the goal, or has momentum
   replaced judgment? Name any drift plainly.
2. **Delivery vs inventory** — what has actually reached the user vs what is
   parked on branches? Building ahead of delivering is the most common failure
   this review exists to catch.
3. **Compounding debt** — integration debt, worktree/branch sprawl, deferred
   housekeeping, stale reopen-triggers nobody re-opened.
4. **Stop/converge/continue** — explicit verdict per active stream.
Checkpoint the review (`claim: "30k review"`, decision = the verdicts) — it
is loop state like any other, and the next review starts from it.

## Meta-loop

Every several iterations, run the loop on itself: skim recent checkpoints for
the failure modes in references/egdl-stages.md (confirmation bias, premature
closure, unmeasured assertion, method blindness). v0.2→v0.3 of this skill was
exactly such a pass — five benchmark rounds falsified "composite-first" and
this file changed accordingly. Capture practice-tuning as its own checkpoint.
