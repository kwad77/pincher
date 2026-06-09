# Hotspot risk dogfood correlation

This artifact closes the remaining #1914 acceptance evidence: document, with numbers, how an existing hotspot metric relates to `mcp_pincher_changes` blast-radius output on recent Pincher dogfood PRs.

## Method

- Project: `/home/kwad77/pincher`
- Measurement date: 2026-06-09
- Hotspot metric: `risk_score = incoming_calls*3 + outgoing_calls + test_adjacent_calls`, the same additive scoring formula used by `pincher report`.
- Blast-radius source: `mcp_pincher_changes(scope=base:<PR base SHA>)` run on fetched PR heads.
- Scope: four recent v1.2 dogfood samples covering report-code edits and docs-only evidence artifacts.

This is a small dogfood correlation check, not a universal statistical claim. It is intended to prove that the metric is useful enough for planning/test prioritization and that the raw evidence remains falsifiable.

## Samples

| PR | Slice | Max changed production `risk_score` | `changes` impacted symbols | Critical | High | Medium | Ranked tests | Result |
|---:|---|---:|---:|---:|---:|---:|---:|---|
| #1930 | report hotspot risk scoring | 18 | 10 | 7 | 2 | 1 | 4 | Report-code hotspot edits produced non-zero blast radius and focused report tests. |
| #1933 | report rationale query fields | 18 | 9 | 6 | 2 | 1 | 3 | Report-code hotspot edits again produced non-zero blast radius and focused report tests. |
| #1934 | rationale precision audit docs | 0 | 0 | 0 | 0 | 0 | 0 | Docs-only report artifact did not touch production hotspots and had no code blast radius. |
| #1935 | architecture report token-savings docs | 0 | 0 | 0 | 0 | 0 | 0 | Docs-only report artifact did not touch production hotspots and had no code blast radius. |

Observed separation in this sample:

- Samples with max changed production `risk_score >= 18`: 2/2 had non-zero `changes` blast radius and ranked tests.
- Samples with max changed production `risk_score = 0`: 0/2 had impacted code symbols or ranked tests.
- The `risk_score` metric therefore correlates directionally with the `changes` blast-radius signal on these dogfood samples: report-code hotspots require focused verification, while docs-only artifacts do not.

## Raw hotspot inputs for report-code samples

| PR | Changed symbol | incoming | outgoing | degree | test-adjacent | risk_score |
|---:|---|---:|---:|---:|---:|---:|
| #1930 | `writeProjectReportMarkdown` | 2 | 11 | 13 | 1 | 18 |
| #1930 | `writeProjectReportJSON` | 2 | 9 | 11 | 1 | 16 |
| #1930 | `reportHotspots` | 3 | 3 | 6 | 1 | 13 |
| #1930 | `reportHotspotRiskScore` | 1 | 0 | 1 | 0 | 3 |
| #1933 | `writeProjectReportMarkdown` | 2 | 11 | 13 | 1 | 18 |
| #1933 | `writeProjectReportJSON` | 2 | 9 | 11 | 1 | 16 |
| #1933 | `reportRationaleMapFor` | 2 | 4 | 6 | 0 | 10 |
| #1933 | `reportLineSpan` | 2 | 0 | 2 | 0 | 6 |
| #1933 | `reportRationaleAttachment` | 1 | 0 | 1 | 0 | 3 |

## Reproduction notes

1. Fetch PR heads into local refs:

   ```bash
   git fetch origin pull/1930/head:refs/tmp/pr-1930 pull/1933/head:refs/tmp/pr-1933 pull/1934/head:refs/tmp/pr-1934 pull/1935/head:refs/tmp/pr-1935
   ```

2. For each sample, check out the PR head and run `mcp_pincher_changes` against the PR base SHA:

   ```text
   #1930: head=refs/tmp/pr-1930 base=4e064dd2ccd34f533923228e06bfe62f717b3e1c
   #1933: head=refs/tmp/pr-1933 base=d64db492e758130166fd439d9dd286e32dce2d42
   #1934: head=refs/tmp/pr-1934 base=f55aa1b9651c3591011f5260a7ae3dcc63b11cac
   #1935: head=refs/tmp/pr-1935 base=922bd9ea5360d02ba933d94ab5a06a4477a42896
   ```

3. Compute hotspot raw inputs from the Pincher DB using the same `CALLS` edge counts and test-adjacent path policy as the report scorer.

## Planning implication

For report and graph-intelligence work, treat changed production symbols with non-zero risk inputs as a signal to run `mcp_pincher_trace`, the ranked `changes` tests, and the focused report CLI tests before broad CI. Treat docs-only artifacts separately: they still need doc/link/changelog checks, but a zero production hotspot score plus zero `changes` impact is evidence that no report API/CLI/schema behavior changed.
