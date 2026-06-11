---
name: pincher-steward
description: Scheduled maintenance pass over a pincher-indexed project — usable verbatim as a heartbeat/cron prompt (Claude Code /schedule routine, CI nightly, or a manual "give the repo a checkup"). Runs doctor advisories, health drift, coaching/adherence review, and ledger triage, then emits a ≤40-line report. Conclusion-density doctrine - counts and asserts before any row-shaped call; spawn real work only on actionable findings.
version: 0.1.0
---

# Pincher Steward

A heartbeat that outlives any session. The steward's job is **triage, not
work**: detect drift cheaply, act on the pre-priced safe fixes, queue the rest,
and leave a short report plus a checkpoint. A steward that burns a full context
window has failed regardless of what it found.

## Preload

```
ToolSearch query="select:mcp__pincher__doctor,mcp__pincher__health,mcp__pincher__adr,mcp__pincher__stats,mcp__pincher__list"
```

(`loop` / `coach` / `count_only` / `assert_graph` exist from schema v40/v41
servers; on older servers skip those steps — the doctor/health legs alone are a
worthwhile heartbeat.)

## Conclusion-density triage (the standing doctrine)

Every question gets the cheapest call that yields a *verdict*, escalating to
row-shaped output only when a verdict demands it: a count (~29 tok) before a
listing; an assert verdict (~32 tok) before a traversal; `health`'s summary
before any per-symbol query. The steward reads conclusions and prices; it does
not browse.

## The pass

| # | Leg | Call → action |
|---|---|---|
| 1 | **Doctor** | `doctor`; act on the safe advisories it prices: vacuum (reclaimable DB space), prune (stale extraction_failures / dead projects), settings_flood and similar config advisories. Safe subset only — destructive remediations (project deletion, force-reindex) go to the report's queue, never auto-run. |
| 2 | **Health drift** | `health`; compare against the last steward checkpoint: per-language coverage tier drops, parse-error climbs, symbol-count cliffs, watermark/generation vs last run. Drift = a finding; stable = one line. |
| 3 | **Coach** | `coach window=7d` (v40+): act on findings that arrive priced (e.g. "callers of X re-fetched N× — an ADR would have saved ~M tokens" → write the ADR now). Unpriced suggestions are queue material, not steward work. |
| 4 | **Adherence** | Review the adherence/`next_steps` telemetry (coach output or `stats`). Persistent prescription-ignoring is a *design* signal, not a discipline failure — flag it for migration down the stack (skill prose → server default → hook). Measured precedent: 0/31 next_steps followed even by a motivated agent; bolder prose is not the fix. |
| 5 | **Ledger triage** | `loop resume`/`loop list` (v40+): (a) stale reopen_triggers — condition met but never reopened → reopen or escalate; (b) the **AWAITING-HUMAN queue** — surface every blocked item in the report so decisions rot in front of the user, not in chat history; (c) loops with no checkpoint in >N days → candidates to close or hand back. |
| 6 | **Report + capture** | ≤40 lines (format below); then checkpoint it (`loop checkpoint`, claim "steward pass <date>") so the next heartbeat diffs against this one instead of recomputing. Evict after capture. |

## The report (≤40 lines, hard cap)

```
STEWARD <project> <date>
ACTED:    <auto-fixes taken, with the advisory that priced each>   # measured
DRIFT:    <health deltas since last pass, or "none">               # measured
QUEUE:    <needs-human items: destructive fixes, unpriced coach
           findings, AWAITING-HUMAN ledger entries, stale triggers>
ADHERENCE:<prescriptions being ignored + migrate-down candidates>
NEXT:     <what the next heartbeat should watch>
```

One line per item, confidence labels on anything load-bearing, pointers
(advisory id, ADR key, `<loop>#<seq>`) instead of payloads. If a leg found
nothing, it gets one word, not a paragraph.

## Anti-patterns

- **Row-shaped triage.** Listing symbols/failures/sessions to "get a feel" —
  counts and verdicts first; rows only when acting on a specific finding.
- **Heroic stewardship.** Discovering a juicy refactor and doing it in the
  heartbeat. Queue it; the steward's budget is for *detection*. Spawn a real
  agent (pincher-loop) only on actionable, priced findings — and say so in ACTED.
- **Unpriced action.** Acting on a hunch-grade coach suggestion while priced
  advisories sit unactioned.
- **Report inflation.** 200 lines of healthy-system narration. Healthy = short.
- **No checkpoint.** A steward pass that doesn't checkpoint forces the next one
  to recompute drift from scratch — the heartbeat loses its memory.
