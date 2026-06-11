# The eight EGDL stages and their exit gates

Each iteration **terminates in a decision** (Accept / Defer-with-trigger / Reject)
— never open-ended exploration. A stage isn't "done" until its gate is satisfied;
the gates are the whole point — each blocks a specific recurring failure.

| # | Stage | What happens | Exit gate (do not skip) | Pincher driver |
|---|---|---|---|---|
| 1 | **Frame** | State the goal as a *falsifiable* claim — an explicit `iff` | A claim that could be proven wrong | `onboard_module`, `guide`, `adr list`, `health` |
| 2 | **Pre-review** | Red-team *before* starting; steelman the status quo you'd replace | ≥1 concrete way it could be wrong, named | (subagent for high-stakes) |
| 3 | **Probe** | Cheapest experiment that could falsify it, on **real** data | Measured numbers, not intuition | `context_for_task`, then a real-corpus run |
| 4 | **Root-cause** | Investigate every surprise; bucket/control the data; don't stop at the first explanation | The surprise is *explained*, not hand-waved | `investigate_failure`, `trace` |
| 5 | **Self-critique** | Audit your *own* method for bugs/bias | The method's weakest point is stated out loud | — |
| 6 | **Decide** | Accept / Defer-with-trigger / Reject; label confidence | A decision **plus** a confidence label | — |
| 7 | **Gate** | Structural no-regression gates + graceful degradation | Green gates AND a fallback path exist | `changes`, `plan_change`, test suites |
| 8 | **Capture** | ADR / memory / issue so the decision compounds | Decision is durable, not buried in chat | `adr set` |

## Failure mode → guardrail (why each gate exists)

- **Confirmation bias** (skipping the red-team when excited) → Stage 2 is *mandatory*.
- **Premature closure** (first plausible cause wins) → Stage 4 requires explaining
  surprises. *(Worked example: "15% parse errors → it's let-else" → no, isolation
  parses clean → it's an emergent GLR error-recovery cascade. The first guess was wrong.)*
- **Unmeasured assertion** ("the new way is just better") → Stage 6 forbids deciding
  on an `inferred` core claim when `measured` is feasible. Go get the number first.
  *(Worked example: "tree-sitter agreement is only 71%" looked bad until root-cause
  showed the divergence was tree-sitter being MORE correct — symbol-count parity was
  the wrong metric.)*
- **Method blindness** (benchmarking the wrong thing) → Stage 5 makes self-audit a step.
- **"Cheap now" overriding "needed"** → Stage 1's `iff` keeps *value*, not just cost, on the table.
- **Decide-forever** → every Defer ships an explicit **re-open trigger**.
- **Cross-turn drift / re-litigation** → Stage 8 (durable `adr` capture) + the task tracker hold state.

## Standing rules

- **Confidence labels.** Every load-bearing claim tagged `measured | inferred | assumed`.
  An ADR cannot be **Accepted** on an `inferred` core claim — it stays **Proposed** until measured.
- **Re-open triggers.** Every Defer carries the conditions that would reopen it.
- **Falsifiability budget.** Any architecture/tier decision states, up front, the
  measurement that would prove it wrong, on a real corpus.
- **Graceful degradation is structural.** New capability ships behind a fallback (e.g.
  a `HasError()` → regex dispatcher), so a regression degrades to today's behavior,
  never worse. No-regression is a *gate*, not vigilance.
- **Re-interpret your own bars when evidence warrants.** A documented threshold (e.g.
  ">2% parse errors") may have been written before a safety mechanism existed; if a
  per-file graceful fallback makes ">2%" a non-regression, that's a Stage-6 decision to
  surface (and re-document), not a silent override. *(This is itself a directional gate
  — see gates.md.)*

## Improvements over the unstructured loop

1. **Independent adversarial review.** For high-stakes loops, a subagent reviewer gets
   the *artifact but not your reasoning*, so it red-teams fresh — catches what you
   rationalized before you publish.
2. **Loop ledger + meta-loop.** Keep a light running record (the task tracker + `adr`
   index half-do this). Every N iterations, run the loop on itself: audit recent
   decisions for the failure modes above.
