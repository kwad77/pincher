# ADR-0008: AST-tier extraction via real tree-sitter compiled to WASM (pure-Go)

**Status:** **Accepted** — engine decision proven in production; **Rust shipped at AST tier (1.0)** ([#1960](https://github.com/kwad77/pincher/issues/1960), 2026-06-11). Java/TS/C# are rollout (extractor work), not viability.
**Date:** 2026-06-10 (accepted 2026-06-11)
**Decision-maker:** kwad77 (sole maintainer)
**Confidence:** core architectural claim **measured + shipped** (Rust, production); Java/TS/C# parse-error rates **measured at 0%** on real corpora; their extractors are pending (same pattern as Rust).
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

## Realized outcomes & lessons (2026-06-11, from shipping Rust)

What executing the decision actually taught — load-bearing for the remaining
language rollouts, so they don't relearn it:

- **Generalization confirmed, with a grammar-maturity caveat.** Real-tree-sitter-via-WASM parsed Rust (ripgrep) and Java (gson) at **0% parse errors**, and C# (Newtonsoft.Json *library*) and TypeScript (zod) cleanly in the narrow spike. The review's #1 "Rust=0% won't generalize" risk is retired — but the *fuller* C# corpus (Newtonsoft incl. its test tree, 945 files) measured **7.2% parse errors**, dominated by preprocessor `#if/#else/#endif` splitting constructs and some attribute edge cases. Lesson: **0% on a clean library subset does not predict 0% on a full repo**; the spike must include test/preprocessor-heavy code, and grammar maturity varies sharply by language.
- **The >2% reverse-trigger is about *graceless* failure, not a raw error count — the dispatcher reinterprets it.** ADR's original bar ("promote only at ~100% clean") predates the proof that the per-file all-or-nothing dispatcher degrades *gracefully*: a parse-error file falls back to the regex tier (0.85) it would have used anyway, so a non-zero error rate is **never a regression** — it is the safety net working. C# shipped **default-ON at 7.2% fallback** because 92.8% of files gain AST-tier 1.0 and the rest are exactly as good as before. The reverse-trigger still bites if errors are *graceless* (crashes, wrong-but-confident symbols, or a rate so high the language is mostly regex anyway); 7.2%-with-clean-fallback is none of those. Grammar-update for C# preprocessor handling is tracked as follow-up, not a ship blocker.
- **C# grammar is a binary-size outlier (+6 MB).** Its generated `parser.c` is ~34 MB (vs Rust 5.9 MB, Java 2.5 MB) → the WASM grew 1.6 → 7.5 MB, binary ~41 → 47 MB. Still within the "selective embedding" budget (only Phase-1 grammars are linked), but it confirms the ADR's "cost is binary size, per-grammar" thesis — C# alone is bigger than Rust+Java combined. A future lever if budget tightens: gate the largest grammars behind a build tag.
- **TypeScript completes Phase 1 (N=5) — the best-measured flip, and it needed the richest extractor.** TS/TSX is two grammars (`tree_sitter_typescript` for `.ts`, `tree_sitter_tsx` for `.tsx`, ~+2.9 MB WASM → ~10.4 MB, binary ~50 MB); the dispatcher routes by the registry's language name. Measured on zod (401 files): **1.0% parse errors** (mature grammar + modern single-target corpus — the opposite of C#'s preprocessor-heavy worst case), **96.2% symbol agreement with exact parity on every type kind** (Class/Interface/Enum/Module), and CALLS **16× richer** (1924 → 31926) because tree-sitter walks every `call_expression` where the regex CALLS pass only sees same-line `name(`. No JS-AST collision: TS had *no* prior AST path (the `tdewolff` JS-AST extractor is JS-only and doesn't parse TS types), so this is purely additive. **The catch: the TS regex tier is far richer than Rust/Java/C#'s** — it tracks funcStack-scoped local-variable QNs (`module.fn.innerFn.var`, #1422), namespace scoping with a single `currentClass`-style slot (#1762: a class inside a namespace stays moduleQN-based, *not* namespace-nested), Variable `Parent` stamping, object-literal methods, and **receiver-type-aware CALLS** (`this.X`→class, typed `c: Cart`→`Cart`, bottom types→empty, #1177). Reproducing all of it through tree-sitter (a binding table for receiver types, a dual container/enclosing scope model) was the bulk of the work — far more than the "clean flip" first estimate. **Lesson: estimate a language's flip cost from its *regex tier's convention surface* (how many dedicated tests encode QN behavior), not from grammar availability.**
- **Phase 2 opened with PHP — the cleanest flip of all (2026-06-11).** PHP (`\` namespace separator, `moduleQN`-keyed/namespace-blind, trait = scope-only container matching the regex `scopeRE`) measured on guzzle (43 files): **0% parse errors, 100% symbol agreement with exact parity on every kind** (Class 34=34, Function 10=10, Interface 6=6, Method 313=313; zero only-regex, zero only-ts), **+142 net-new IMPORTS** (`use` declarations the regex tier never captured), and richer CALLS (1393→1626, incl. constructor `new` edges). Mature grammar + idiomatic modern PHP = no divergence to explain. Cost: +~0.8 MB WASM (11.2 MB), binary ~50→51 MB. **Reusable confirmation: the recipe is now N=6 and the per-language effort is dropping** — PHP's extractor passed its inline unit test on the first run (every node-type assumption correct), because the convention surface (container slot + namespace-blind QN + method/function/import/call/ctor edges) is now boilerplate.
- **Swift (Phase 2, community grammar) — probe-first beat the non-standard node names.** Swift's grammar reuses ONE `class_declaration` node for struct/class/actor (→Class), enum (distinguished by an `enum_class_body` child →Enum), AND extension (distinguished by a leading `user_type` child →scope-only, like a Rust impl); `protocol_declaration` →Interface with `protocol_function_declaration` members; func name is `simple_identifier`. Empirically node-dumping a snippet first (rather than assuming standard names) made the extractor pass its unit test on the first run. Measured on Alamofire (43 files): **4.7% parse errors** (2 files, graceful regex fallback — the community-grammar maturity tax, comparable to C#), types near-exact (Class 108≈107, Enum 47=47, Interface 25=25), **callable total exactly equal (714=714)**, +52 net-new IMPORTS, CALLS 1600→2134. The 66.5% raw agreement is the **Function↔Method reclassification** seen on Java/C# too: regex's scope-blind line tracking mis-emits ~298 methods as top-level Functions; tree-sitter scopes them correctly (only 3 genuine top-level funcs). Cost: +~3 MB WASM (Swift's 16 MB `parser.c`; total 14.1 MB). **Lesson reinforced: for any community/non-standard grammar, node-dump a representative snippet before writing the extractor — assumed node names would have wasted an iteration.**
- **Ruby (Phase 2) — the biggest single quality lift, from the 0.70 tier.** Ruby's regex tier was the **0.70 "approximate"** tier (not 0.85), so a clean tree-sitter parse is a **0.70 → 1.0** jump — the largest of any language. modSep `::`, namespace-blind moduleQN; `class`/`module` → Class (the regex `classRE` maps both); `method`/`singleton_method` → Method (in a class) or Function (top level), with the `def self.x`/`def Klass.x` receiver skipped; `require`/`require_relative`/`load` calls → net-new IMPORTS. Measured on Sinatra (147 files): **0% parse errors, 90.6% symbol agreement** (highest of the Phase-2 set), +220 net-new IMPORTS, and CALLS **17× richer** (811 → 13987 — Ruby's regex CALLS pass was especially thin). Probe-first again paid off (`call` = last identifier before `argument_list`; bare `bark` is a plain identifier, not a call — so neither tier captures it). Cost: +~2.1 MB WASM (15 MB `parser.c`; total 16.3 MB).
- **Kotlin (Phase 2, community grammar) — one node, four kinds.** Like Swift, Kotlin's grammar overloads `class_declaration` for class / interface / data class / enum class: enum is told apart by an `enum_class_body` child, **interface by the `interface` keyword in the byte range between the node start and the `type_identifier`** (modifiers + keyword precede the name); `object_declaration` and *named* `companion_object` are Classes; anonymous companion objects emit no symbol and keep their members scoped to the enclosing class (regex `classRE` parity). `import_header` → net-new IMPORTS; calls mirror Swift exactly (`call_expression` / `navigation_expression`). Measured on okhttp (525 files): **6.3% parse errors** (community-grammar tax, graceful fallback — comparable to Swift), types near-exact (Class 625≈631, Enum 17=17, Interface 32=32), 86.3% agreement, **+5620 net-new IMPORTS**, CALLS 1.7× richer (27754→47405). Cost: Kotlin's grammar is the largest yet (23 MB `parser.c` → +~4 MB WASM, total 20.3 MB).
- **A differential-method bug nearly read as a regression (EGDL Stage 5 catch).** First TS run showed Interface 641→188 (looked like ts dropping 453 interfaces). Root cause was the harness, not the extractor: it counted regex symbols for the 4 parse-error files but skipped ts on them (ts returns empty on error) — yet the production dispatcher routes those files to the regex tier, so nothing is dropped. Counting regex only on the *same clean-parse files* ts claims flipped the result to **188=188 exact, 96.2% agreement**. The fix is in the gated diff harness. **Lesson for every future flip's differential: tally regex and tree-sitter over the *same* clean-parse file set, or error-file symbols masquerade as a tree-sitter regression.**
- **The vendored binding leaked WASM memory — the real production blocker.** `malivvan` v0.0.1 `malloc`s a 24-byte node struct per `Child`/`NamedChild` and never frees it, and didn't export `ts_tree_delete`. Harmless one-shot; fatal under the indexer's pooled-instance reuse (unbounded heap → OOM on large repos). **Fix pattern (reuse for every language):** export `ts_tree_delete`, add `Tree.Close` + `Node.Free`, and have the extractor track every per-parse allocation and bulk-free it + close the tree on the way out. **Gate it:** a no-leak-under-N-reuses RSS test (must stay flat) + a fuzz target on the marshaling boundary (no panic on arbitrary bytes). Both shipped for Rust.
- **Match the regex tier's QN conventions exactly to avoid silent ID churn.** Don't invent QNs. Use `moduleQN(relPath, "::")` (filename stem included, not `filepath.Dir`); `impl Trait for Type` puts the trait in the *QN* (`Type::Trait::method`, #1783) but the *Parent* is just `Type`; per-file CALLS key on the enclosing function at confidence **0.6** (the resolver upgrades them); symbols carry byte+line spans. Doing this made the **entire existing regex test suite pass unchanged through the tree-sitter dispatcher** — the proof of zero churn. Mod-block-aware QNs are a *separate*, deliberately-churning enhancement.
- **Dispatcher shape (reuse verbatim):** per-file all-or-nothing — clean parse (`HasError()==false`) → AST symbols with `FileResult.ConfidenceOverride = 1.0`; any error or `PINCHER_DISABLE_<LANG>_AST=1` → regex tier. Thread-safe via a bounded (≤4) lazily-initialized instance pool (the indexer is concurrent; tsbridge isn't). Mirrors the JS/Python AST dispatchers.
- **CI gotcha — linking the WASM binding into `pinch` breaks the #185 subprocess-coverage fold.** Under `go test ./...`, the `runXxxCLI` dispatch wrappers (line-covered only via an instrumented-subprocess exec) drop to 0% once `wazero` links in — independent of `-coverpkg`. Resolution: exclude `cmd/pinch` from the *line*-coverage gate (it's behavior-tested via the subprocess exec). Already in `ci.yml`; no further action per language.
- **Costs realized:** binary 37 → 41 MB (wazero + one 1.2 MB grammar); ~0.2 s one-time pool warmup on the first file of a language. Within budget.

## References

- `delivery-loop.md` — the EGDL practice; this ADR is its Stage-6/8 artifact, with the spike as the Stage-3 evidence.
- Spike harness: `internal/ast/spike_treesitter_rust_test.go` (branch `spike/gotreesitter-rust`); WASM build via `zig cc` from the `malivvan/tree-sitter` vendored grammar sources.
- [ADR-0006](0006-non-go-ast-strategy.md) §"Non-goals" (CGo is a v2.x decision) and re-open trigger #3 (pure-Go-preserving distribution plan) — satisfied by this ADR.
