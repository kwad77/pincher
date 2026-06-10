# ADR-0008: AST-tier extraction via real tree-sitter compiled to WASM (pure-Go)

**Status:** Proposed
**Date:** 2026-06-10
**Decision-maker:** kwad77 (sole maintainer)
**Confidence:** core architectural claim **measured** (Rust); per-language error rates for Java/TS/C# **inferred** pending Phase-1 confirmation
**Supersedes:** [ADR-0006](0006-non-go-ast-strategy.md)'s "Option D only" stance (regex-tier sufficient, no non-Go AST). This ADR re-opens that decision on the **cost axis** ADR-0006 named as a valid trigger.
**Issues:** [#1689](https://github.com/kwad77/pincher/issues/1689) non-Go AST strategy · [#1182](https://github.com/kwad77/pincher/issues/1182) Rust · [#1183](https://github.com/kwad77/pincher/issues/1183) Java · [#1452](https://github.com/kwad77/pincher/issues/1452) Swift
**Related:** [ADR-0002](0002-v1-frozen-surface.md) frozen surface · [ADR-0005](0005-v1.3-substrate-and-language-coverage.md) provenance-tier substrate · `delivery-loop.md` (the EGDL practice this ADR was produced under)

## Context

ADR-0006 deferred all non-Go AST work and committed to the regex tier (0.85) for Swift/Rust/Java/etc. Its decisive constraint: the only coverage-complete option (Option C, tree-sitter) required **CGo**, which breaks pincher's load-bearing `CGO_ENABLED=0` single-static-binary / trivial-`go install` / clean-cross-compile invariant. ADR-0006 explicitly left a re-open path: *"a not-yet-articulated hybrid that preserves the pure-Go single-binary invariant."*

That hybrid now exists. Two distinct pure-Go tree-sitter approaches were evaluated under the EGDL loop:

- **Option A — native Go reimplementation** (`gotreesitter`): reimplements the tree-sitter *runtime* in Go, reusing extracted grammar tables.
- **Option B — real tree-sitter via WASM** (`malivvan/tree-sitter` pattern): the **actual upstream tree-sitter** C runtime + official grammars compiled to WebAssembly with `zig cc`, executed by `wazero` (a pure-Go, `CGO_ENABLED=0` WASM runtime).

Both preserve the invariant. They differ on correctness: A is a young reimplementation; B runs the same parser that powers GitHub, Neovim, Zed, and Helix.

## Measured evidence (EGDL Stage 3, ripgrep + redis corpora)

Built our own `ts.wasm` (`zig cc --target=wasm32-wasi-musl`, hermetic — **not emscripten**) and measured all engines on identical, real corpora, `CGO_ENABLED=0`:

| Corpus | Engine | Parse errors | Wall (parse+walk) |
|---|---|---|---|
| **Rust** (ripgrep, 100 files / 52K LOC) | **real-tree-sitter / WASM** | **0 / 100** | **722 ms** |
| | gotreesitter (native reimpl) | 15 / 100 | 5.5 s |
| | regex tier | 0 (degrades, never errors) | 0.18 s |
| **C** (redis, 100 files / 131K LOC) | real-tree-sitter / WASM | 18 / 100¹ | 1.4 s |
| | gotreesitter (native reimpl) | 37 / 100 | 8.9 s |

¹ C is the worst case for any tree-sitter (preprocessor/macros produce ERROR nodes even upstream); inflated and grammar-version-sensitive. Rust is the representative result.

**Findings:**
- **Correctness:** real-tree-sitter-via-WASM parsed *all* of ripgrep at **0% error**, including the let-else and macro-heavy files that cascaded gotreesitter into 15% failures. The reimplementation bug class is absent because it is the real parser.
- **Speed (inverts the "WASM is slow" assumption):** WASM was **6.5–7.6× faster** than the native-Go reimplementation on both corpora — wazero's optimizing compiler runs the mature C parser faster than the young Go runtime. ~4× slower than regex (722 ms vs 180 ms), irrelevant for an indexing-time pass (~14 µs/line).
- **Invariant held:** every probe built and ran `CGO_ENABLED=0`, statically linked ("not a dynamic executable"), cross-compiled to arm64 clean.

On clean-parsing files the regex and tree-sitter symbol counts matched within 1% (ratio 1.01), and tree-sitter additionally captured structural wins regex cannot: full `use a::{B,C,D}` import-tree enumeration (3.1× import targets), `macro_rules!` definitions, and native `impl Trait for Type` association (the #1783 QN-collision problem).

## Decision

Two separable decisions — only the first is settled by current evidence:

**(a) Engine decision — DECIDED (measured).** When pincher does AST-tier extraction for a non-Go language, the engine is **real upstream tree-sitter compiled to WASM via pinned `zig cc`, executed by `wazero`** (pure-Go, `CGO_ENABLED=0`). This beats both alternatives ADR-0006/this spike considered: tree-sitter+CGo (breaks the invariant) and the native-Go reimplementation (15% errors). Measured on Rust.

**(b) Per-language promotion — NOT decided here; gated per language.** No language moves from regex (0.85) to AST (1.0) until its **corpus gate passes on ≥2 real corpora** (see Accept criteria). Rust is the only language measured so far. Java/TS/C# are Phase-1 *candidates*, explicitly **unmeasured** — the C result (18% on redis) is a standing warning that grammar/language specifics matter and "Rust = 0%" does **not** generalize. The phasing table below is a work-ordering proposal, not a correctness claim.

Supporting commitments:

- **Binding:** own a thin in-tree fork of the `malivvan/tree-sitter` MIT glue. This is the **second-largest liability** after the build pipeline: it is `unsafe` pointer arithmetic across a WASM linear-memory boundary, and a marshaling bug surfaces as **silently-wrong, confidence-1.0 edges** — worse for a trust-graph tool than an honest regex miss. It is gated accordingly (differential + fuzz tests, below). We fork rather than depend-on-v0.0.1 or rewrite-from-scratch because both alternatives are worse, not because the fork is low-risk.
- **Artifacts:** the `.wasm` is a **generated, version-pinned build artifact** — `zig` + grammar versions pinned, built **hermetically and bit-reproducibly** in CI (checksum proves bytes are stable; the reproducible build proves they were *correctly* produced), with an SBOM for the upstream grammar C. The recurring cost is **not** binary size — it is the per-grammar update treadmill, where every grammar bump re-runs the full corpus gate.
- **Dispatcher (graceful degradation):** per file, **all-or-nothing** — if tree-sitter parses with `HasError()==false`, use its symbols/edges (stamped `EXTRACTED`, confidence 1.0, **plus a provenance stamp of the producing engine+platform** so consumers can detect cross-host graph divergence); otherwise fall back to the regex tier. A regression degrades to today's behavior, never worse. Reuses the `FileResult.ConfidenceOverride` pattern proven by the Python AST path (#856).
- **Status = Proposed.** Direction (a) is measured; promotion (b) and the operational claims (concurrency, memory, value) are not yet — so the ADR stays Proposed until the Accept criteria produce numbers.

## Phasing

| Phase | Languages | Notes |
|---|---|---|
| **Infra** | — | WASM build pipeline (`zig cc`), in-tree wazero binding, shared extractor framework + dispatcher, **parser-instance pool** (tree-sitter parsers are not thread-safe; pincher indexes concurrently), corpus-gate + golden tests. ~80% of total effort. |
| **1** | Java, **Rust** (measured), TypeScript, C# | Highest value + cleanest grammars (Java has no external scanner). First task: confirm Java/TS/C# at ~0% error via the corpus gate. |
| **2** | PHP, C++, Ruby, Kotlin, Swift | Mostly per-language extractor maps; Kotlin/Swift are community grammars (higher maintenance). |
| **3** | Scala, Lua, Elixir, SQL, Makefile, Zig, Dart, R, C | Opportunistic, gated by dogfood demand. C is low-value (preprocessor caps AST gains). |

## Token & context economy

Per the EGDL standing rule, every ADR states its context-cost impact:

- **Zero added per-call cost.** This change is entirely in the **extraction/indexing layer**. It adds **no** `_meta` fields and **no** per-response tokens; the graph is consumed by the same compact `search`/`symbol`/`context`/`trace` tools.
- **Net token-economy *positive* downstream — `assumed`, not yet measured.** More precise symbols and edges (complete import trees, real call edges, correct impl-trait scoping) *should* make those tools return tighter results, so agents make fewer dead-end follow-up calls — the core pincher value proposition. This is **plausible but unmeasured**; Phase-1 includes a before/after agent-call-count benchmark to confirm or retire the claim.
- **Costs are binary size *and* process RSS — not response context.** A grammar adds ~0.5–1 MB as WASM (vs ~0.1 MB as a native blob); Phase-1's four grammars + runtime ≈ +8–12 MB on a 37 MB binary. Separately, each pooled `wazero` instance holds a WASM linear-memory arena, so peak **RSS** during concurrent indexing is a real cost (see Concurrency & memory) — distinct from, and not to be confused with, per-call response/token cost, which is zero.

## Concurrency & memory (the riskiest engineering surface)

pincher indexes files in parallel goroutines; tree-sitter parsers and `wazero` instances are **not thread-safe**. This is the single hardest part and is **not yet designed or measured** — flagged here so it is not mistaken for solved:

- **Pooling model:** a bounded pool of parser instances (sized against `GOMAXPROCS`, not unbounded), checked out per file. Open questions Phase-1 must answer with numbers: per-instance instantiation cost; whether a single large file (e.g. 131K-LOC) holds an instance long enough to starve the indexer; and whether the pool serializes throughput vs today's regex pass.
- **Memory:** each instance carries a WASM linear-memory arena. Peak RSS ≈ pool-size × per-instance arena, potentially hundreds of MB on a wide machine indexing a large repo. **An RSS budget is a first-class Accept criterion**, not an afterthought.
- The race detector proves *no data races*; it does **not** prove *no throughput regression* and *no RSS blowup*. Those need their own measured gates.

## Test discipline (no-regression gate — EGDL Stage 7)

- **Corpus gate per language:** parse-error rate on a real corpus must stay ~0; any regression fails CI and blocks promotion.
- **Golden symbol/edge snapshots** per language (mirrors the existing `testdata/corpus/*.snapshot.json` pattern).
- **Dispatcher fallback test:** a forced-ERROR file must yield byte-identical output to the regex tier (no double-count, all-or-nothing per file).
- **Differential test (catches silent marshaling corruption):** on clean-parsing files, tree-sitter and regex symbol sets must agree within a tight bound; unexplained divergence is treated as a marshaling bug, not a "win," until proven otherwise. Guards against confident-but-wrong 1.0 edges.
- **Fuzz the marshaler:** fuzz the WASM↔Go node/query boundary (malformed/huge/adversarial source) — this is `unsafe` pointer code; memory-safety bugs must fail in CI, not in users' graphs.
- **Race detector** (already a release gate) over the parser pool — **plus** a measured throughput gate (no indexing-time regression vs regex) and an **RSS-ceiling gate** under concurrent indexing.
- **Hermetic, bit-reproducible WASM build** verified in CI (rebuild → identical checksum) + pinned `zig`/grammar versions + grammar-C SBOM.

## Non-goals / explicit caveats

- **Platform tier policy.** wazero's optimizing compiler is amd64/arm64 only; other GOARCHes fall to the interpreter (much slower). AST tier is enabled on amd64/arm64; **regex fallback elsewhere**, advertised in `_meta`/capabilities so it is never silent. AST tier varying by platform is an accepted, documented trade.
- Not a commitment to Phase 2/3 timelines — those are demand-gated.
- Does not retire the regex tier; regex remains the universal fallback and the answer on interpreter-only platforms.
- Go/Python/JS stay on their existing native/dispatcher extractors; consolidating them onto tree-sitter is a future nicety, not in scope.

## Accept criteria (Proposed → Accepted)

This ADR moves to **Accepted** when, for all four Phase-1 anchors (Java, Rust, TypeScript, C#):
1. corpus-gate parse-error rate is ~0% on ≥2 real corpora each;
2. the concurrent parser-pool passes under the race detector with no measured indexing-throughput regression;
3. the dispatcher-fallback test proves zero-regression on forced errors; and
4. measured WASM binary-size delta is within the stated budget.

## Re-open / reversal trigger

Reverse to ADR-0006 (regex-only) if Phase-1 surfaces: a sustained >2% corpus parse-error rate that grammar updates can't close, an unfixable concurrency/throughput regression, or a binary-size blowout beyond budget that selective embedding can't contain.

## References

- `delivery-loop.md` — the EGDL practice; this ADR is its Stage-6/8 artifact, with the spike as the Stage-3 evidence.
- Spike harness: `internal/ast/spike_treesitter_rust_test.go` (branch `spike/gotreesitter-rust`); WASM build via `zig cc` from the `malivvan/tree-sitter` vendored grammar sources.
- [ADR-0006](0006-non-go-ast-strategy.md) §"Non-goals" (CGo is a v2.x decision) and re-open trigger #3 (pure-Go-preserving distribution plan) — satisfied by this ADR.
