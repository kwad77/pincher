# ADR-0007: Schema Phase 2b (multi-branch coexistence) — deferred for v1.x

**Status:** Accepted
**Date:** 2026-06-10
**Decision-maker:** kwad77 (sole maintainer)
**Issues:** [#1371](https://github.com/kwad77/pincher/issues/1371) (Phase 2b request) · [#1303](https://github.com/kwad77/pincher/issues/1303) (multi-branch umbrella) · [#1370](https://github.com/kwad77/pincher/issues/1370) (Phase 2a, shipped)
**Related:** [ADR-0002](0002-v1-frozen-surface.md) frozen surface · [ADR-0005](0005-v1.3-substrate-and-language-coverage.md) v1.3 theme · [ADR-0006](0006-non-go-ast-strategy.md) defer-until-evidence precedent

## Context

The multi-branch story is split across three phases per the original [#1303](https://github.com/kwad77/pincher/issues/1303) plan:

- **Phase 1** (v30→v31, shipped): `branch` column on `symbols` / `edges` / `files` / `pending_edges`. Every persisted row now carries which branch it was extracted from.
- **Phase 2a** (v31→v32, shipped): `projects.current_branch` plus the `pincher doctor` branch-drift advisory. The advisory surfaces the silent "checkout-without-reindex → wrong byte offsets" footgun — the actual correctness failure mode of branch switching — and prompts the user to re-index.
- **Phase 2b** ([#1371](https://github.com/kwad77/pincher/issues/1371), proposed for v1.3): widen `PRIMARY KEY` / `UNIQUE` constraints across `symbols` / `edges` / `files` / `pending_edges` to include `branch`, so two branches of the same project coexist in one DB without one overwriting the other on re-index. Update every query path to scope by `branch` (defaulting to `projects.current_branch`). Update DELETE plumbing + tail-pass GC + resolver passes to be per-branch.

[#1371](https://github.com/kwad77/pincher/issues/1371)'s acceptance criteria explicitly note: *"This is a substantial migration touching every query path. The pre-release branch tolerates the risk but Phase 2b should be cut cleanly from a stable base and merged on its own to make rollback straightforward if a user hits corruption."*

Phase 2b is the largest single architectural change still listed in v1.3 scope per [ADR-0005](0005-v1.3-substrate-and-language-coverage.md). Before committing v1.3 to it, this ADR asks: **does the user-visible problem Phase 2b would solve actually justify the cost in v1.x?**

## Decision

**Schema Phase 2b is deferred for v1.x. The doctor branch-drift advisory shipped in Phase 2a is the v1.x answer to multi-branch correctness; coexistence-without-reindex remains an open trigger-gated work item.**

Phase 2b can re-open with a follow-up ADR when concrete dogfood evidence demonstrates that the reindex cost on branch switch-back is a material user pain — see Re-open trigger below.

### Rationale

The Phase 2a advisory already handles the actual **correctness** failure of branch switching. A user who checks out branch B without re-indexing gets a clear, named, remediation-pointed warning from `pincher doctor` instead of silent wrong-byte-offset answers. That was the user-visible footgun the multi-branch story was filed to fix.

What Phase 2b adds is **UX**: switching from B back to A no longer requires re-indexing — the rows are already there. That is real value, but it is a UX improvement, not a correctness fix. The cost-benefit shifts accordingly:

- The cost is enumerated in [#1371](https://github.com/kwad77/pincher/issues/1371)'s scope section: schema migration with table rebuild + FTS5 vtab recreate, query-layer threading on **every** read path (`GetSymbol`, `GetSymbolsByQN`, `SearchSymbols`, `SearchSymbolsByCorpus`, `GetHotspots`, `GetDeadCode`, `cypher.Engine`'s `runJoinQuery` + `runNodeScan`, `trace` BFS, `neighborhood`, and so on), per-branch `DeleteSymbolsForFile`, per-branch tail-pass GC, per-branch resolver passes (`resolveImports` / `resolveCalls` / `resolveReads`). The issue body itself names `GetSymbol(id)` as "biggest blast radius; used by every read-by-id tool."
- The benefit is bounded by how often users actually thrash between branches at a frequency that makes re-indexing prohibitive. Re-index speed went up substantially in v1.1.0-rc.2 ([#1899](https://github.com/kwad77/pincher/issues/1899) / [#1938](https://github.com/kwad77/pincher/pull/1938)) — the binary-drift reindex write-batching fix bounded the worst-case writer-starvation latency. The pain of re-indexing branch A again is real but smaller than it was when [#1371](https://github.com/kwad77/pincher/issues/1371) was filed.
- The pattern that worked for [ADR-0006](0006-non-go-ast-strategy.md) applies here too: *"committing to a large architectural change is speculative engineering until there is concrete user-value evidence that the cost is justified."* The non-Go AST decision deferred Rust/Java/Swift AST work for the same reason. Phase 2b's blast-radius is comparable; the evidence bar to commit it should be the same.

## Non-goals

- This ADR is **not** a closure of [#1303](https://github.com/kwad77/pincher/issues/1303) (multi-branch umbrella). Phase 1 (branch column) and Phase 2a (advisory) remain shipped; Phase 3 (post-checkout git hook auto-updating `projects.current_branch`) is a separate filing and not affected.
- This ADR is **not** a commitment to never ship Phase 2b. It explicitly names the trigger that would re-open the question — see below.
- This ADR does **not** alter the [ADR-0002](0002-v1-frozen-surface.md) frozen-surface promises. The `branch` columns on `symbols` / `edges` / `files` / `pending_edges` are part of the schema (evolving), not the frozen wire surface; future PK/UNIQUE changes per the migration story remain permitted within the `evolving` policy.
- This ADR does **not** affect the [provenance-tier substrate](0005-v1.3-substrate-and-language-coverage.md) (PR #1947) or any v1.3 work outside the multi-branch coexistence question.

## Re-open trigger

A v1.x minor can re-open this ADR by filing a follow-up ADR that demonstrates **all three** of the following:

1. **At least 3 `dogfood-found` `severity-2+` issues** with `axis-multi-branch-reindex` (a new label to be added if/when this fires) where the root-cause analysis attributes user-visible failure to "re-index needed on branch switch-back" rather than to other multi-branch defects (resolver drift, advisory false positives, etc.). Same volume-based axis-escalation rule as [`docs/process/dogfood-routing.md`](../process/dogfood-routing.md). The Phase 2a advisory must remain the recommended remediation throughout — the trigger fires when users explicitly reject the re-index workflow.
2. **A measured branch-switch frequency budget** — telemetry from at least one volunteer dogfood user showing branch switches per indexable workday and the time-cost of re-index in that user's actual workflow. "I switch branches a lot" is not enough; the cost of the workflow vs. the cost of Phase 2b's implementation has to be a defensible comparison.
3. **A staged rollout plan** that respects the [#1371](https://github.com/kwad77/pincher/issues/1371) own caveat that this work "should be cut cleanly from a stable base and merged on its own." The follow-up ADR should propose the work as a multi-PR sequence: (a) schema migration with table rebuild and FTS5 vtab recreate; (b) read-path threading on every query, behind a feature flag so the migration can land before the threading completes; (c) write-path per-branch DELETE / GC / resolver scope; (d) feature flag flipped on by default + acceptance test for two-branch coexistence; (e) feature flag removed in a follow-up minor.

Until all three exist, the deferral stands and the doctor advisory is the v1.x answer.

## Consequences

**Positive:**

- Frees v1.3 of the largest single architectural risk in its current scope. The substrate-flavored framing of v1.3 ([ADR-0005](0005-v1.3-substrate-and-language-coverage.md)) holds without forcing a coexistence-rewrite under the same banner.
- Preserves the existing query-layer simplicity in v1.x. Every read path can continue assuming a single `(project_id, id)` PK rather than wiring a branch parameter through every code path and every test.
- Removes the rollback-corruption risk that [#1371](https://github.com/kwad77/pincher/issues/1371) explicitly warned about. If Phase 2b had landed in v1.3 and a user hit a migration defect, the rollback story for an already-released v1.3 binary is substantially harder than it is for a deferred work item.
- Establishes a consistent pattern with [ADR-0006](0006-non-go-ast-strategy.md): large architectural commitments require concrete dogfood evidence, not "it would be nicer."

**Negative:**

- Users who frequently switch between long-lived branches continue to pay the re-index cost on switch-back. Per [#1899](https://github.com/kwad77/pincher/issues/1899) / [#1938](https://github.com/kwad77/pincher/pull/1938) this cost is bounded but not zero.
- The [#1303](https://github.com/kwad77/pincher/issues/1303) multi-branch story remains "shipped through Phase 2a" rather than "fully shipped through Phase 2b." Release-notes language should describe the multi-branch story honestly: the doctor advisory is the v1.x remediation, not "pincher supports multi-branch coexistence transparently."
- Phase 3 (post-checkout git hook auto-updating `projects.current_branch`) becomes the natural next multi-branch item rather than Phase 2b. That's a separate filing and not affected, but readers tracking the umbrella should see Phase 2b's deferral explicitly.

**Mitigations:**

- The v1.3 release notes for the [Pincher report artifact](https://github.com/kwad77/pincher/issues/1912) and the [provenance-tier substrate](https://github.com/kwad77/pincher/issues/1945) can foreground the substrate framing without making any multi-branch promises beyond Phase 2a.
- If [#1303](https://github.com/kwad77/pincher/issues/1303) Phase 3 (post-checkout hook) ships in v1.3 or later, the multi-branch advisory becomes nearly invisible to users — the hook updates `projects.current_branch`, the advisory rarely fires, and the perceived pain of re-index drops further.
- The [v1.3 umbrella tracker #1945](https://github.com/kwad77/pincher/issues/1945) should be updated to reflect this deferral so contributors don't pick up Phase 2b expecting it to land in v1.3.

## Implementation

1. Close [#1371](https://github.com/kwad77/pincher/issues/1371) with a comment referencing this ADR.
2. Update [ADR-0005](0005-v1.3-substrate-and-language-coverage.md) §"Substrate" — `#1371` was listed under "Substrate" with the schema Phase 2b framing; mark it resolved-deferred and reference this ADR for the rationale. (Done in the same PR as this ADR.)
3. Update the [v1.3 umbrella tracker #1945](https://github.com/kwad77/pincher/issues/1945) substrate section accordingly.
4. The v1.3.0 ship cut per ADR-0005 §"Release sequencing" already drops the language-extractor minimum per [ADR-0006](0006-non-go-ast-strategy.md); after this ADR, the ship cut also drops the Phase 2b schema work. Substrate (provenance-tier ✅, schema Phase 2b deferred) + MCP capability + plugin-extractor decision carry the remaining v1.3.0 story.

## References

- [#1371 — schema Phase 2b: widen PKs/UNIQUEs for true multi-branch coexistence](https://github.com/kwad77/pincher/issues/1371). The decision request and scope this ADR resolves; the issue body's own warning about rollback-corruption risk is the source of the conservative posture this ADR adopts.
- [#1303 — multi-branch umbrella](https://github.com/kwad77/pincher/issues/1303). Phase 1 + Phase 2a remain shipped; Phase 3 (post-checkout hook) is unaffected.
- [ADR-0006: non-Go AST strategy](0006-non-go-ast-strategy.md). The defer-until-evidence pattern this ADR mirrors.
- [#1899](https://github.com/kwad77/pincher/issues/1899) / [#1938](https://github.com/kwad77/pincher/pull/1938) — re-index speed improvements that lowered the marginal value of Phase 2b since the original filing.
- [docs/process/dogfood-routing.md](../process/dogfood-routing.md) §"Volume-based axis escalation" — the >3-per-axis trigger this ADR's re-open clause builds on.
