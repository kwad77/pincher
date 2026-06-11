# messy-corpus ladder — RESULTS (2026-06-11, n=1 per arm)

Reopen-trigger answered: *"re-run the three-arm ladder on a polyglot/generated-code
corpus where naive navigation degrades."* Setup: `SETUP.md`. Task: `tasks/messy-10q.md`.
Grading key: `tasks/messy-10q.answers.md` (verified ground truth). CLI 2.1.173,
default model, one run per arm, all in one sitting.

## Scoreboard (accuracy graded by hand, tokens from score.sh)

| arm | accuracy | turns | total tokens | cache-create | cache-read | output | cost (USD) | wall (s) |
|---|--:|--:|--:|--:|--:|--:|--:|--:|
| native-naive | **10/10** | 36 | **350,388** | 28,289 | 312,091 | 7,225 | 1.267 | 136.9 |
| pincher-mcp (v1.5.0, stdio) | **10/10** | **22** | 511,289 | 46,484 | 453,585 | 8,216 | 1.824 | 166.2 |
| native-coached | **10/10** | 26 | 526,093 | 26,317 | 489,784 | 7,328 | 1.409 | 173.8 |

## The headline, stated plainly

**The hypothesis — "pincher wins accuracy and/or tokens HERE specifically" — was not
confirmed on this run.**

- **Accuracy: a three-way tie at 10/10.** The corpus (12,627 `process_order` grep
  matches, 3,589 `retry` matches, three same-named live twins, two dead twins, two
  layers of string-keyed dispatch, decoy config values) degraded *efficiency*, not
  *correctness*. Every arm correctly identified all four `SaveOrder` call sites
  (including the admin backdoor and the dead V1), refused both dead twins, picked the
  right `ProcessOrder`, traced the full TS→Go→Python chain, and reported 7 (not the
  decoy 3/5) for `order_retry_limit`.
- **Tokens: native-naive won outright** (350k), pincher second (511k, +46%), coached
  grep last (526k, +50%). The pincher-vs-coached gap (~3%) is inside single-run noise.
- **Turns: pincher won clearly** (22 vs 26 vs 36) — the graph answers in fewer steps,
  but each step carries a bigger context (MCP tool schemas: cache-create 46.5k vs
  ~27-28k for the native arms; cache-read compounds that every turn).
- **Cost: pincher was the most expensive** ($1.82 vs $1.27 / $1.41).

## What the corpus DID prove

Compare against the same arms on the friendly single-repo task (smoke/investigate
rounds, ~38-52k tokens for naive): on hostile terrain naive ballooned to **350k
tokens and 36 turns** — a ~7x degradation. The terrain works. What didn't follow was
the second half of the bet: pincher's per-call efficiency did not overcome its fixed
schema overhead plus the model's willingness to brute-force grep its way through.

Notably, **coached grep — the winner on friendly terrain (27.2k) — came in dead last
here.** Its "locate with grep first, then read narrow windows" discipline is exactly
the wrong prior when grep returns thousands of generated-noise matches; it paid for
repeated wide locates (highest cache-read of all arms, 489k).

## Per-question detail

All 30 cells correct; per-question verdicts vs `messy-10q.answers.md`:

| Q | topic | native-naive | native-coached | pincher-mcp | notes |
|---|---|---|---|---|---|
| 1 | dispatch indirection (Go) | ✓ | ✓ | ✓ | all three found the `init()` registration in register.go |
| 2 | which ProcessOrder charges | ✓ | ✓ | ✓ | nobody fell for the gen/orderspb or cross-language twins |
| 3 | ProcessOrderV1 dead? | ✓ | ✓ | ✓ | pincher cited a 0-caller inbound trace; native arms grepped definitions vs uses |
| 4 | legacy.py never runs | ✓ | ✓ | ✓ | all three nailed both reasons (no decorator, not imported) |
| 5 | SaveOrder blast radius (4 sites) | ✓ | ✓ | ✓ | all found admin.go backdoor AND dead process_v1.go; pincher got it from one `trace` call |
| 6 | checkout → Python fulfiller | ✓ | ✓ | ✓ | full cross-language chain incl. all three string keys, every arm |
| 7 | order_retry_limit = 7 | ✓ | ✓ | ✓ | every arm dodged the `3` fallbacks and the 3/5 fixture snapshots |
| 8 | registered job types (3) | ✓ | ✓ | ✓ | nobody listed legacy or pb.go method names |
| 9 | processOrderOld dead? | ✓ | ✓ | ✓ | pincher cited inbound trace; naive additionally checked the bundle |
| 10 | billing.ProcessOrder callers (2) | ✓ | ✓ | ✓ | all found cmd/reconciled — the one grep tends to miss in chaff (but didn't here) |

Where each arm's evidence came from (self-reported in answers): the pincher arm used
`trace` for the dead-code and blast-radius questions (3, 5, 9, 10) and mixed
search/read elsewhere; both native arms reported grep+windowed reads throughout.
Per-tool-call token attribution is not available from the `json` envelope
(re-run with `--output-format stream-json` to get it — see loopbench README).

## Honest caveats

- **n=1 per cell.** The pincher-vs-coached delta (3%) means nothing; the
  naive-vs-rest delta (~45-50%) is large enough to take seriously but still wants
  3 reps before being quoted as a ratio.
- **The model is strong enough to brute-force a 48-file corpus.** 10/10 across the
  board suggests this corpus size cannot separate arms on *accuracy* with current
  frontier models; separating them now requires either a much larger corpus (where
  grep windows physically can't fit), multi-rep variance on cost, or edit-loop tasks.
- **MCP schema overhead is structural.** Pincher's extra ~18k cache-create is paid
  on every turn via cache-read. On a 22-turn session that's the whole gap to naive.
  Fewer-turns-but-fatter-turns lost to more-turns-but-thinner-turns.
- **Environment constant:** the user-global CLAUDE.md (pincher usage policy) was
  visible to all arms; both native arms noted pincher's absence and fell back, as
  designed. Same condition as prior loopbench rounds.
- No harness mechanics failed; no arm errored; nothing was re-run.

## Verdict

The messy corpus successfully creates terrain where naive navigation *degrades* (7x
token blowup, 36 turns) and where coached grep's friendly-terrain win inverts. It
does not (at this size, n=1) produce the pincher win the thesis hoped for: pincher
delivered the fewest turns and tied on perfect accuracy, but paid more total tokens
and dollars. If the claim "pincher wins on messy code" is to survive, the next
falsification step is scale (a corpus too large to grep-windows through) and reps
(n≥3), not another same-size rerun.
