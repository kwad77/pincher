---
name: pincher-onboard
description: First contact with an unfamiliar repository, driven by pincher code intelligence. Use when landing in a codebase you have never indexed or oriented in — "get to know this repo", "onboard onto X", "what is this codebase" — or as the opening move before the first real task in a new project. Success is being oriented with project memory (ADRs) seeded in under 10 tool calls, with extraction-coverage trust checks run before any graph answer is believed.
version: 0.1.0
---

# Pincher Onboard

From zero to *productively oriented* in **<10 pincher calls**, leaving durable
project memory behind so the next session starts warm instead of re-onboarding.
The failure this skill prevents: an hour of file-by-file Reads that produces a
mental model no one (including future-you) can resume.

## Preload

Pincher MCP tools may be deferred; fetch schemas before calling:

```
ToolSearch query="select:mcp__pincher__health,mcp__pincher__init,mcp__pincher__index,mcp__pincher__architecture,mcp__pincher__onboard_module,mcp__pincher__adr,mcp__pincher__guide,mcp__pincher__search"
```

## The sequence (don't improvise on first contact)

| # | Call | Why this, why now |
|---|---|---|
| 1 | `health` | Is there an index at all, and can you trust it? (trust checks below) |
| 2 | `init` + `index` — only if step 1 said no/stale | One-time cost; everything after is O(query) |
| 3 | `architecture` | Entry points, hotspots, language breakdown — the 10k-ft map in one call |
| 4-5 | `onboard_module` on each area the task touches (1-2 areas, not every directory) | Module-level summary: local entry points, external consumers/dependencies — cheaper than reading any file in it |
| 6-8 | `adr action=set` × 3 — seed **PURPOSE**, **STACK**, **PATTERNS** | The capture step; see below |
| 9 | `guide` with the first real task | Returns the 2-3 next calls; hands off to pincher-loop / the task itself |

Ten calls is a budget, not a ritual: a repo you half-know may need 4. If you're
past 10 calls and still "orienting", you've drifted into exploration — start the
actual task and let it pull context on demand.

## Trust checks — run before believing graph answers

`health` is step 1 because graph answers are only as good as extraction:

- **Per-language coverage.** A language at stub/regex tier gives name-level
  answers only; caller/callee claims about it are `inferred` at best — label
  them so downstream work doesn't treat them as `measured`.
- **Symbol/file counts vs reality.** ~0 symbols in a clearly large repo means
  the index is empty or pointed at the wrong root — fix before step 3, or
  `architecture` will confidently describe nothing.
- **Staleness.** If the index predates recent commits, re-`index` first; an
  onboarding snapshot of last week's code seeds wrong ADRs.

## Seeding the ADRs (the part most agents skip)

Onboarding that lives only in your context window dies with the session. Write
three short ADRs — conclusions, not transcripts:

- **PURPOSE** — what the project is for, its users, the one-sentence shape of
  the architecture (from `architecture` + the README).
- **STACK** — languages and their extraction tiers (from `health`), build/test
  entry points, the commands that gate a merge.
- **PATTERNS** — conventions `onboard_module` exposed: layering, naming, where
  tests live, "X is generated — never hand-edit" gotchas.

Each claim carries a confidence label (`measured | inferred | assumed`); an
`assumed` in an ADR is a todo, not a fact. After capture, refer to ADRs by key —
don't re-paste their content into the window (evict-after-capture).

## Anti-patterns

- **Read-first onboarding** — opening files before `architecture` has named
  which files matter. The map costs one call; N file reads cost N×.
- **Exhaustive `onboard_module`** — one call per directory "to be thorough".
  Orient where the work is; the rest stays lazy.
- **Trusting an unhealthy index** — skipping the coverage check and presenting
  graph claims about a stub-tier language as measured fact.
- **Capture skipped** — ending onboarding with knowledge only in chat history.
  If the ADRs aren't seeded, the onboarding didn't happen.

## Exit criteria

You are done when: (1) you can name the entry points and hotspots without
looking, (2) PURPOSE/STACK/PATTERNS ADRs exist, (3) `guide` has named the first
task's opening calls — all within the ~10-call budget. Hand off to
**pincher-loop** for the delivery work itself.
