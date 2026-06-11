# Phase → cheapest-call map (v0.3, measured 2026-06-11)

> v0.2 of this file preached "composite-first." Five benchmark rounds falsified
> that: composites win when picking up a *cluster*; atomic calls win single
> facts; `trace` is the cheapest tool per fact for caller questions; native
> Read/Grep legitimately win raw-text/body-text work. The binding rule is now
> **the smallest call that answers the current phase** — and prescriptions that
> prove out here should migrate DOWN the stack (skill → server default → hook),
> not accumulate as prose.

## The map

| Phase / need | Cheapest call | Measured rationale |
|---|---|---|
| Session start on ongoing work | `loop resume` (bounded brief) | 149-token resume; verbatim cross-session recall |
| One symbol's location | `search` + `fields=id,name,file_path` | snippetless exact-identifier defaults |
| Caller/callee facts | `trace` (`compact`, depth 1-2; `count_only` for "how many") | 12 callers ≈ 500 tok; count ≈ 29 tok |
| Several facts at once | `batch` (shared `max_tokens`; `from`/`quiet` chains when N feeds N+1) | one envelope per N answers; chain = 0.67× two calls |
| Whole-investigation pickup | `context_for_task`, symbol-shaped task | `suggestions_only` floor caps a bad seed at ~500 tok |
| Pre-edit read | `context` + `max_tokens` 400-800 (+`detail=skeleton` to skim) | 68KB worst case → 1.8k; skeleton 0.21× |
| Re-read | just re-fetch | repeat `context` = 43 tok (`unchanged:true`) |
| Pre-edit risk | `plan_change` | caches prediction for verify |
| Post-edit gate | `verify_change` / `changes` → `pincher test-impacted` | predicted-vs-actual + executed evidence |
| Invariant check | `assert_graph` | 32-tok passing verdicts |
| Failure triage | `investigate_failure error_text=...` | ranked suspects with evidence |
| Raw text / body-text / string literals / configs | **native Grep/Read** | FTS indexes names/signatures/docstrings, NOT bodies |
| Mechanical multi-site edits | native sed/Edit after pincher locates sites | grep/sed is the densest editor |

(Tools marked `loop`/`batch`/`verify_change`/`assert_graph`/`count_only`/`skeleton`
exist from schema v40/v41 servers; on older servers fall back to the atomic
column.)

## Budget + trust rules

- Every source-carrying call gets `max_tokens`; every batch a shared budget.
- Trust the envelope's own signals: `seed_quality`, staleness warnings,
  `index_in_progress`, `plan_stale`, `dispatch_blind`, `watermark` drift. They
  exist so the loop never acts on silently-wrong data.
- Verify numbers against the CURRENT index generation before gating on them —
  a stale-index baseline corrupted a real audit (2026-06-11).

## Measured gotchas (kept from dogfood, updated)

- `context_for_task` matches callable symbols — keep task strings
  symbol-shaped; a prose task now degrades safely to `suggestions_only`
  (pick a `seed_id` or pass `expand=true`).
- Cross-project calls may under-report `tokens_saved` — judge by payload size.
- Prescription decays in the hot path (measured: 0/31 next_steps followed even
  by a motivated agent). If a rule here keeps being ignored, the fix is a
  server default or a hook, not bolder prose.

## Persistent memory (Capture)

`loop checkpoint` for in-flight work (self-contained decision text + reopen
triggers); `adr set` only for timeless conventions/recipes; refer to captured
material by pointer afterward (`<loop>#<seq>`, ADR key) — never re-paste.

## Carve-outs (use native tools without apology)

Test-assertion bodies · build/CI/lockfiles · exact-byte inspection ·
body-text search · brand-new files pincher can't know about.
