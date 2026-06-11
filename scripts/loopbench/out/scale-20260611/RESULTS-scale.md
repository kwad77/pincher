# scale + repetition round — RESULTS (2026-06-11, n=3/n=3/n=1)

The standing falsification gate from the messy-corpus verdict (#2002), executed with
the reps the schema-diet rerun (#2003) demanded. Setup: `SETUP.md`. Corpus:
`build-messy-corpus.sh --scale 40` (542 files, 486 live source files, ~13.0k live
LOC — ~10x the 43-file base; 527 indexed, 4,945 symbols, 2,384 edges). Task:
`tasks/messy-scale-8q.md` (same shapes as messy-10q: cross-copy chains,
which-of-N twins, dead twins, blast radius). Grading key:
`tasks/messy-scale-8q.answers.md` (verified ground truth). CLI 2.1.173, default
model, all 7 runs in one sitting. Raw data: `r1/ r2/ r3/ control/` +
`results-all.tsv`.

## Scoreboard (accuracy graded by hand; tokens from results.tsv)

| arm | n | accuracy | turns (mean, per-rep) | total tokens (mean ± range) | cache-read mean | cost mean | wall mean |
|---|--:|--:|---|---|--:|--:|--:|
| native-naive | 3 | **8/8, 8/8, 8/8** | 21.7 (15 / 28 / 22) | **384,603** (303,767 – 428,119) | 335,927 | $1.74 | 197.2 s |
| pincher-mcp-core-lean | 3 | **8/8, 8/8, 8/8** | 26.3 (31 / 28 / 20) | 475,246 (386,707 – 581,483) | 420,408 | $1.83 | 180.0 s |
| pincher-mcp full/rich (control) | 1 | **8/8** | 38 | 1,440,781 | 1,382,234 | $2.84 | 266.7 s |

Per-rep detail:

| rep | arm | acc | turns | input | cache-create | cache-read | output | total | cost |
|---|---|--:|--:|--:|--:|--:|--:|--:|--:|
| r1 | native-naive | 8/8 | 15 | 5,177 | 40,884 | 245,665 | 12,041 | 303,767 | $2.142 |
| r2 | native-naive | 8/8 | 28 | 2,656 | 35,370 | 379,495 | 10,598 | 428,119 | $1.643 |
| r3 | native-naive | 8/8 | 22 | 2,656 | 26,829 | 382,621 | 9,816 | 421,922 | $1.437 |
| r1 | core-lean | 8/8 | 31 | 2,923 | 45,786 | 521,956 | 10,818 | 581,483 | $2.008 |
| r2 | core-lean | 8/8 | 28 | 2,788 | 36,880 | 407,024 | 10,857 | 457,549 | $1.715 |
| r3 | core-lean | 8/8 | 20 | 2,784 | 39,037 | 332,243 | 12,643 | 386,707 | $1.773 |
| ctl | full/rich | 8/8 | 38 | 3,429 | 44,478 | 1,382,234 | 10,640 | 1,440,781 | $2.838 |

## Verdict (a): does the token gap invert when the corpus outgrows comfortable grepping?

**No. It did not invert — on means it widened.** On the small corpus (#2003) the
core-lean arm sat within 6% of native-naive (372k vs 350k). At 10x scale the mean
gap is **+24%** (475.2k vs 384.6k). The n=3 ranges do overlap at one point —
core-lean's best rep (386.7k) beat native's two worst reps (428.1k / 421.9k) — so
the 24% is not a clean separation at this n, but the direction is unambiguous:
**native-naive still wins tokens at this scale, with full accuracy, and there is no
evidence of an approaching crossover.** Cost is closer to parity (mean $1.74 vs
$1.83, overlapping ranges) because pincher's excess is mostly cheap cache-read
while native pays more raw input/output; wall clock mildly favors pincher
(180s vs 197s mean). If you value latency or dollars the arms are within noise of
each other; on the registered metric (total tokens) native wins.

**Why scale didn't hurt grep the way the thesis hoped:** the scaled corpus is
self-similar. Once the agent reads ONE shard cluster it has learned the entire
pkgNN pattern, and per-shard distinct names (`CapturePaymentPkg22`,
`fulfill_pkg09`) made the *targeted* greps small even though the *naive* greps are
enormous (15,360 `retry` matches, 12,787 `process_order`). All three native reps
discovered the cross-shard wiring from one pipeline read and then answered the
remaining questions nearly from priors, verifying with narrow greps. Corpus bytes
grew 10x; the information the agent needed grew ~1x.

**What WOULD have to be true for inversion** (extrapolating from the observed
shapes, not wishing): native's spend is cache-read growth per turn — its context
accumulates grep output and file windows. Pincher arms' spend is the same
cache-read compounding plus the per-turn schema/envelope tax, *minus* nothing —
because in every pincher answer file the agent reports verifying pincher results
with grep anyway ("confirmed by both pincher trace and grep"), it pays for BOTH
navigation stacks. Inversion therefore needs at least one of: (1) terrain without
a learnable naming scheme — heterogeneous code where each question forces fresh
wide reads, so native's per-question cost stays at its question-1 level instead of
amortizing (here: native's first ~8 turns were exploration, the rest cheap); (2)
questions whose textual answer-path requires pulling large files into context
(multi-hop chains through files too big to window), pushing native's cache-read
per question above pincher's flat ~10-15k per answered question; or (3) an agent
policy that trusts the graph and skips textual re-verification — without that, the
pincher arm's floor is native's cost plus the MCP overhead, and it can never win
tokens. (3) is the cheapest lever and is a coaching/policy question, not a corpus
question.

## Verdict (b): does the schema-diet turn inflation (22→34) bite at n≥3?

**No — it does not replicate as a structural cost.** Core-lean turns at scale:
31 / 28 / 20 (mean 26.3), native-naive: 15 / 28 / 22 (mean 21.7) — the
distributions interleave, and core-lean's best rep (20 turns) beats two of three
native reps. Meanwhile the full/rich control took **38 turns — more than any
core-lean rep** — while carrying the rich pedagogy whose removal supposedly caused
the inflation. The 22→34 signal from #2003 was n=1 sampling noise, not "lean
schemas make the agent take more, smaller steps." Within this round, turn count
correlates with sampling luck (how fast the rep spotted the pkgNN pattern), not
with schema style.

**What the control DID prove:** the schema diet is not optional at scale. Full/rich
spent **1.44M tokens — 3.0x core-lean and 3.7x native** — because the 34-tool rich
advertisement (~18.4k schema tokens) is re-read on every one of 38 turns
(cache-read 1.38M). That is the messy-round mechanism (#2002: fewer-but-fatter
turns) amplified by a longer session. Core+lean cut the same workload to 475k.
The #2003 diet survives its first adversarial rerun, and by a wide margin.

## Accuracy: 56/56 — the corpus still cannot separate arms on correctness

Every arm, every rep, every question (7 runs × 8 questions). All reps named
`CapturePaymentPkg22` (not the pkg21 decoy), all found the single cross-shard
caller of `RecordAuditPkg30`, all three SaveRecordPkg12 sites including the dead
V1, the 5-not-2 knob with the right consumer AND its cross-shard caller, 43
registered job types, and both dead twins with correct mechanism citations.
Divergence per question was zero; the only divergence was *how much each arm
spent* getting there (native: grep + windowed reads throughout, self-reported;
pincher arms: trace/search for Q3/Q5/Q6 + grep re-verification). A 10x corpus and
8 adversarial questions did not produce a single wrong cell from a frontier model.
Accuracy separation, if it exists, needs corpora beyond honest single-machine
fixture scale, edit-loop tasks, or weaker models.

## Honest caveats

- n=3 vs n=1: the two n=3 arms have overlapping ranges (native 304-428k, core-lean
  387-581k); the mean gap (+24%) is larger than the messy round's single-run gap
  (+6%) but a sign test at n=3 proves nothing. The full/rich figure is n=1 — its
  *direction* (3x worse) is far outside any plausible noise band, the exact ratio
  is not.
- Run-order/cache effects: arms alternated within each rep, all in one sitting,
  same machine. First runs of each arm may carry cold-cache penalties (native r1
  has the highest cache-create and the longest wall of the native reps).
- The corpus's self-similarity is both its realism (generated/sharded code is
  repetitive) and its weakness as a grep-killer; see verdict (a) for what a
  grep-hostile-at-scale corpus would additionally need.
- Environment constant: the user-global CLAUDE.md pincher policy was visible to
  all arms (native arms noted pincher absent and fell back, as designed).
- No harness mechanics failed; no run errored or was re-run; all 7 runs are the
  first and only attempts.

## Verdict, stated plainly

At 10x scale with n=3, **native-naive wins total tokens outright (384.6k mean) with
perfect accuracy; the gap to pincher did not invert — it grew.** The schema-diet
turn-inflation fear is retired (it was noise), and the diet itself is strongly
re-confirmed: full/rich pincher at scale is 3x the tokens of core+lean. The
surviving pincher case on read-only Q&A is turns/wall-clock and near-cost-parity,
not tokens. The next falsifiable lever is not a bigger corpus of the same shape —
it is (1) trust-the-graph coaching that eliminates double-verification, measured
against the same key, and/or (2) edit-loop tasks where `changes`/blast-radius has
no cheap textual substitute.
