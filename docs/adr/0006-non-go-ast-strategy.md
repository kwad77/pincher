# ADR-0006: Non-Go AST strategy — regex tier sufficient, non-Go AST work deferred

**Status:** Accepted — **Option-D ("regex sufficient, no non-Go AST") superseded in part by [ADR-0008](0008-treesitter-via-pure-go-wasm.md)**, which re-opens this decision on the cost axis (a pure-Go tree-sitter via WASM now exists, satisfying re-open trigger #3). The regex tier remains the universal fallback; ADR-0008 governs AST-tier promotion.
**Date:** 2026-06-10
**Decision-maker:** kwad77 (sole maintainer)
**Issues:** [#1689](https://github.com/kwad77/pincher/issues/1689) (decision request) · [#1452](https://github.com/kwad77/pincher/issues/1452) Swift AST · [#1182](https://github.com/kwad77/pincher/issues/1182) Rust AST · [#1183](https://github.com/kwad77/pincher/issues/1183) Java AST
**Related:** [ADR-0002](0002-v1-frozen-surface.md) frozen surface · [ADR-0003](0003-plugin-extractor-deferral.md) plugin-extractor deferral · [ADR-0005](0005-v1.3-substrate-and-language-coverage.md) v1.3 theme · [#856](https://github.com/kwad77/pincher/issues/856) Python dispatcher pattern (the precedent that does *not* transfer)

## Context

Pincher's AST-tier (1.0-confidence) extraction works for Go, Python, and JavaScript. The pattern is:

- **Go**: native `go/ast` in the pincher binary.
- **Python**: a single stdlib-only `python_extract.py` helper `//go:embed`-ed into the pincher binary and executed by the system `python3` interpreter when present (`#856` dispatcher). Falls back to the regex tier when no `python3` is on PATH. Zero distribution artifact.
- **JavaScript**: similar default-on AST extractor (#266).

Three more languages have open AST tickets — Swift ([#1452](https://github.com/kwad77/pincher/issues/1452)), Rust ([#1182](https://github.com/kwad77/pincher/issues/1182)), Java ([#1183](https://github.com/kwad77/pincher/issues/1183)) — and **none of them can use the Python dispatcher pattern**. SwiftSyntax, `syn`, and JavaParser are all compiled libraries — there is no "embed a stdlib script, run with the system interpreter" equivalent.

[#1689](https://github.com/kwad77/pincher/issues/1689) names this as the decision point so the three blocked issues can each follow one chosen path or close out, rather than relitigating the same options three times.

The three options it raises:

| Option | Distribution model | Pure-Go invariant | Coverage |
|---|---|---|---|
| **A** Bundled per-platform helper binaries | Release matrix grows by N×(OS×arch×language) | Core stays pure-Go; helpers are sidecars | Each new extractor adds a helper binary + CI build leg |
| **B** Build-on-first-use (compile helper at install/first-use) | Single binary preserved; helper compiled at runtime, cached | Held | Only where the language toolchain is installed |
| **C** tree-sitter + CGo | `CGO_ENABLED=0` invariant lost; per-target C toolchains for cross-compile | **Broken across the whole binary** | One CGo dep, grammars cover most languages uniformly |

A fourth option the issue body lists as a valid outcome:

| Option | Distribution model | Pure-Go invariant | Coverage |
|---|---|---|---|
| **D** Regex tier (0.85) is sufficient — no non-Go AST work | Unchanged | Held | Same as today: AST-tier for Go/Py/JS, regex-tier for everything else |

The constraints from [#1689](https://github.com/kwad77/pincher/issues/1689):

- The pure-Go single-binary distribution (`go install`, trivial cross-compile, no C toolchain) is currently load-bearing.
- The regex tier (0.85) is the universal fallback and is the v1.0 answer for Swift/Rust/Java.
- v1.0 is about stability — adopting CGo (option C) is a distribution-model rewrite that should not land during the v1.0 hardening phase.

## Decision

**Pincher commits to Option D for v1.x: the regex tier is sufficient; no non-Go AST work ships in any v1.x minor without concrete dogfood evidence that the regex tier is materially blocking user value for the affected language.**

[#1452](https://github.com/kwad77/pincher/issues/1452), [#1182](https://github.com/kwad77/pincher/issues/1182), and [#1183](https://github.com/kwad77/pincher/issues/1183) close as deferred with a documented re-open trigger (see below).

### Rationale

Each of Options A, B, C imposes a real cost. None of them has a corresponding concrete dogfood signal that the cost is justified. The regex tier (0.85 confidence) is doing the job today for Swift, Rust, Java, TS/TSX, Kotlin, C#, PHP, C, C++, and the rest. Until a specific user-visible problem traces back to an extractor accuracy gap — not to "AST is theoretically better" — committing to any of A/B/C is speculative engineering.

The Python AST dispatcher pattern transferred because it was nearly free: one embedded stdlib script, no new release artifact, no toolchain assumption, opportunistic fallback to the regex tier. The proposed non-Go AST options are not free.

- **Option A** grows the release matrix multiplicatively. Each new helper binary needs building, signing, hosting, integrity verification, and version-skew handling across at least Linux × {amd64, arm64} × {Swift, Rust, Java} = 6 artifacts to start. The install-validation matrix already gates 6 cells on every release; doubling it to validate sidecar binaries before they can be trusted is real ongoing operational cost.

- **Option B** is operationally appealing — pincher's binary stays pure-Go — but in practice forces a "first run is slow and may fail" experience that conflicts with the trivial-install story. A first-run `cargo build` against `syn` on a fresh `go install` machine will take 30+ seconds, fail if Rust isn't installed, and create a maintenance liability for the embedded helper source. The Python dispatcher avoided this exactly because Python doesn't compile anything.

- **Option C** is the most coverage-positive answer but trades the entire `CGO_ENABLED=0` invariant. Once pincher links a tree-sitter parser, every release platform needs the C toolchain, cross-compile becomes painful, and `go install github.com/kwad77/pincher/cmd/pinch@latest` stops being a one-line install for everybody. That is a v2.x decision, not a v1.x one.

- **Option D** has the cleanest cost/benefit ratio: it ships nothing, breaks nothing, holds every invariant, and explicitly names the trigger that would re-open the question. The regex tier is **already** at 0.85 confidence for these languages; the marginal value of AST tier is bounded by the gap between 0.85 and 1.0 on a sample of real codebases, which we do not yet have evidence is the rate-limiter on user value.

The right way to revisit this is when there is hard evidence — see Re-open trigger below.

## Non-goals

- This ADR is **not** a closure of the regex-tier promotion work for individual languages (e.g. promoting Swift from 0.70 stub to 0.85 stable regex per [#1450](https://github.com/kwad77/pincher/issues/1450)). Regex-tier promotions are normal extractor work, additive on the existing tier ladder, and continue per their own milestones.
- This ADR is **not** a commitment to never adopt CGo. It says CGo is a v2.x decision. If the v1.x lifetime surfaces evidence that one tree-sitter dep with N grammars is the only sane answer for breadth-of-language coverage, a v2.x ADR can reopen that.
- This ADR does **not** prevent shipping a non-AST `cross-modal` extractor — e.g. a language-server-protocol (LSP) bridge that emits edges at `INFERRED` provenance tier (per [ADR-0005](0005-v1.3-substrate-and-language-coverage.md) and the `provenance_tier` substrate). That is a different design space and falls under future inferred-edge work, not non-Go AST.
- This ADR does **not** block external plugin extractors per [ADR-0003](0003-plugin-extractor-deferral.md). If a plugin author chooses to ship a CGo extractor on their side of the plugin API once that surface graduates, that is their choice; pincher's core remains pure-Go.

## Re-open trigger

A v1.x minor can re-open this ADR by filing a follow-up ADR that demonstrates **all three** of the following with concrete artifacts:

1. **Three or more `dogfood-found` issues** with `severity-2` or higher per language axis (`axis-extractor-rust`, `axis-extractor-java`, or `axis-extractor-swift`) where the root cause analysis explicitly attributes the user-visible problem to regex-tier extraction accuracy rather than to resolver, query, or routing causes. Per the volume-based axis-escalation rule in [`docs/process/dogfood-routing.md`](../process/dogfood-routing.md), this is the existing threshold for axis-level hardening work.
2. **A measured falsifiability budget** — a benchmark over a real-world corpus in the affected language showing the specific accuracy gap (precision, recall, missing-CALLS-edge rate) between the current regex tier and a candidate AST tier. "AST is more correct" is not enough; the gap has to be quantified for the rate-limiting workflow.
3. **A distribution plan that preserves the pure-Go single-binary invariant** for at least the affected language — meaning Option B or some not-yet-articulated hybrid, not Option A or C. Option A and C remain v2.x decisions; the follow-up ADR can argue for them but must do so explicitly.

Until all three of those exist, this ADR's "no non-Go AST work" decision stands.

## Consequences

**Positive:**

- Closes the v1.3 substrate-blockage question that [#1689](https://github.com/kwad77/pincher/issues/1689) named. The remaining v1.3 sub-goals (provenance-tier substrate, schema Phase 2b, MCP capability completions, plugin-extractor decision) can each move on their own track without waiting for this decision.
- Preserves the pure-Go single-binary distribution that the v1.0 launch narrative is built on. The "just `go install` and run" promise stays trivially true.
- Frees three v1.3 issue slots (#1452, #1182, #1183) for higher-leverage work in the milestone.
- Makes the re-open trigger explicit so future contributors don't have to relitigate the same A/B/C trade.

**Negative:**

- Swift / Rust / Java users continue to get the 0.85-confidence regex tier instead of 1.0 AST. For most agent workflows (search, trace, context, blast-radius checks) this is the same answer in practice. For the workflows where it isn't, this ADR makes the trade explicit rather than promising a different answer.
- Pincher's tier-coverage table in [docs/reference/languages.md](../reference/languages.md) keeps showing 0.85 for Swift/Rust/Java in the v1.x lifetime. The honest answer to "when does Rust get AST tier?" becomes "when there's concrete user-value evidence requiring it" rather than "it's planned."

**Mitigations:**

- The regex tier 0.85 → 0.85+ promotions per individual language remain in scope. The release notes and tier-table updates from those promotions continue to be a visible source of "Swift/Rust/Java coverage improvement" without the AST-tier rewrite.
- If dogfood signal accumulates that the regex tier is the rate-limiter for a specific axis, the [`axis-extractor-*` escalation rule](../process/dogfood-routing.md#volume-based-axis-escalation) will trigger the re-open path automatically.
- External plugin extractors per [ADR-0003](0003-plugin-extractor-deferral.md) remain a valid path for users who specifically need 1.0-tier coverage on a non-Go language and are willing to ship a CGo extractor through the plugin surface.

## Implementation

1. Close [#1452](https://github.com/kwad77/pincher/issues/1452), [#1182](https://github.com/kwad77/pincher/issues/1182), and [#1183](https://github.com/kwad77/pincher/issues/1183) with a comment referencing this ADR.
2. Update the [v1.3 umbrella tracker (#1945)](https://github.com/kwad77/pincher/issues/1945) to remove those three issues from the substrate / language-coverage section and reference this ADR for the rationale.
3. Update [`docs/adr/0005-v1.3-substrate-and-language-coverage.md`](0005-v1.3-substrate-and-language-coverage.md) — the "Language coverage" sub-section now lists Tier-2 AST extractors as resolved-deferred rather than open. (Done in the same PR as this ADR.)
4. The v1.3.0 ship cut per ADR-0005 §"Release sequencing" no longer requires "at least one second-language-tier AST" — adjust that minimum to "at least one MCP capability **or** plugin-extractor-graduation decision."

## References

- [#1689 — ADR request: pincher's non-Go AST strategy](https://github.com/kwad77/pincher/issues/1689). The decision request and option enumeration in the issue body is the source of truth this ADR resolves.
- [#856 — Python dispatcher pattern](https://github.com/kwad77/pincher/issues/856). The precedent that worked because Python is interpreted and the helper is a `//go:embed`-ed stdlib script.
- [ADR-0002](0002-v1-frozen-surface.md) §"Database schema (evolving)" and §"What's explicitly NOT frozen" — internal extraction confidence values per the regex-tier ladder are explicitly non-frozen, so future tier promotions remain free.
- [docs/reference/languages.md](../reference/languages.md) — the current language tier table this ADR commits to maintaining in its present shape for v1.x.
- [docs/process/dogfood-routing.md](../process/dogfood-routing.md) §"Volume-based axis escalation" — the >3-per-axis trigger this ADR's re-open clause builds on.
