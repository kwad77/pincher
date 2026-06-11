# Grep interception feasibility (analysis only — nothing here is implemented)

Status: **design sketch, deliberately not shipped.** This document exists so the
next iteration can decide whether to build full Grep interception with the
trade-offs on the table, instead of rediscovering them. It accompanies the
hook-redirect-v2 work (budgeted Read redirects + honest savings measurement);
the Read side shipped, the Grep side stops at this analysis.

## Where Grep interception stands today

The PreToolUse hook (`pincher hook-check`, `cmd/pinch/hook_check.go`,
`decideGrepHook`) already intercepts Grep calls, but only advisorily and only
for one narrow shape:

- **Redirects (advisory):** a pattern that is a single identifier —
  `CamelCase`, `snake_case`, `pkg.Qualified`, `Class::method` — gets a
  systemMessage suggesting `search query="<pattern>"`. The Grep still runs
  (advisory mode since v0.86 / #1654, #1656).
- **Passes through:** regex metacharacters, quoted phrases, multi-word
  patterns, operator-only patterns (`->`), and everything else.

That split is correct given what the index can answer, which is the point of
this document.

## The fundamental blocker: FTS has no body text

Pincher's FTS5 corpora (`symbols_code_fts` / `symbols_config_fts` /
`symbols_docs_fts`, `internal/db/db.go ftsCorpusSchemaDDL`) index exactly four
columns per symbol:

```
name, qualified_name, signature, docstring
```

Symbol **bodies are never stored in the database** — `symbol`/`context` reads
them from disk at fetch time via byte offsets (`index.ReadSymbolSource`).
That is a deliberate architectural choice: it keeps `pincher.db` small (KBs of
metadata per file, not a copy of the repo) and makes results immune to
index/disk content skew for source retrieval.

The consequence: any Grep whose match would land **inside a body** — a string
literal, a comment, an error message, a config value embedded in code, an
arbitrary substring — is unanswerable from the index. `search` would return
zero rows for a pattern that grep finds hundreds of hits for. A redirect there
is not a savings, it is active misinformation (the same
"silent-confidently-wrong" family the codebase keeps stamping out: #317, #960,
#1030, #1031).

Measured against real agent behavior, body-content greps are the majority
class: agents grep for log messages, error strings, env-var names, and flag
literals at least as often as for identifiers. The identifier-shaped minority
is already covered by the advisory redirect.

## What full interception would require

### Option A: body-text FTS corpus (`symbols_body_fts`)

Add an opt-in fourth corpus indexing each symbol's source body, populated at
index time from the same bytes the extractor already reads.

- **Answers:** word-granularity body matches (`unicode61` tokenizer), e.g.
  "which symbols mention `ECONNREFUSED`."
- **Does not answer:** substring/regex matches (`conn refused`, `->`,
  partial-token hits). FTS5 unicode61 matches whole tokens only — grep
  semantics are substring semantics, so even this corpus mis-answers a large
  fraction of real greps unless the hook restricts redirects to
  whole-token-shaped patterns.
- **Index cost (estimate):** body text is the dominant share of source bytes.
  An FTS5 index over it typically lands at 0.5–1.5x the indexed text size
  (postings + content duplication is avoidable with `content=''` external
  content, at the cost of no snippet support). For this repo (~10 MB of
  source) expect roughly **+5–15 MB** of DB; for the large dogfood corpora
  (400k+ symbols) it's **hundreds of MB** — a different product profile than
  today's metadata-only DB.
- **Freshness:** bodies churn far more than signatures. Every edit dirties the
  body corpus; incremental reindex cost per edit rises proportionally.

### Option B: trigram tokenizer corpus

SQLite ≥3.34 ships an FTS5 `trigram` tokenizer that supports true substring
matching (and `LIKE`/`GLOB` acceleration).

- **Answers:** substring matches — the closest to real grep semantics short of
  regex.
- **Still does not answer:** regex patterns (the largest pass-through class
  today). A regex grep can only be served by running a regex engine over the
  candidate set; trigram indexes can pre-filter candidates (the ripgrep/
  Zoekt model) but pincher would be reimplementing a code-search engine.
- **Index cost (estimate):** trigram postings are the expensive end of FTS:
  typically **2–4x the indexed text size**. On the metadata-only baseline this
  is a 10–50x DB size multiplier. Strictly opt-in territory
  (`PINCHER_BODY_CORPUS=trigram` at index time), default off.
- **Constraint:** trigram FTS5 requires queries ≥3 chars; short patterns fall
  back to scans.

### Either option also needs

1. **Hook-side pattern classifier** — extending `decideGrepHook`'s current
   identifier/regex/phrase triage to "token-shaped" (Option A) or
   "literal-substring-shaped" (Option B) vs "regex" (always pass through).
2. **Honest savings telemetry** — the same est-served vs realistic-baseline
   accounting the Read path now records (`est_tokens_served` /
   `baseline_tokens`, schema v40). Baseline for a Grep is its match output
   size, which the hook cannot know pre-flight; it would need post-hoc joining
   against the session's actual Grep results, or a much weaker stat-ed-size
   proxy.
3. **Staleness honesty** — body corpora go stale between index passes; a
   redirect that misses a string added 30 seconds ago is worse than grep.
   The watcher narrows but does not close this window.

## Risks, summarized

| Risk | Severity | Notes |
|---|---|---|
| Wrong answers on substring/regex patterns | High | FTS token semantics ≠ grep substring semantics; mis-redirect = agent silently misses matches |
| DB size blow-up | High (trigram) / Medium (unicode61) | 2–4x text size for trigram; breaks the "metadata-only, tiny DB" contract |
| Index freshness lag | Medium | bodies churn every edit; stale corpus → missed matches |
| Reindex latency regression | Medium | body tokenization dominates incremental passes |
| Marginal win over advisory status quo | Medium | identifier-shaped greps (the answerable class) are already redirected to `search` |

## Recommendation

Do not build Grep interception beyond the existing advisory identifier
redirect until there is field evidence (hook-stats `by_tool` Grep conversion +
override rates) that agents *want* to take Grep redirects and are blocked by
coverage, not by trust. If that evidence arrives, Option A
(`unicode61` body corpus, opt-in env flag, whole-token patterns only,
hook redirects gated on corpus presence) is the smallest honest step;
Option B (trigram) should be considered only with a per-project opt-in and a
documented DB-size warning in `pincher doctor`.

What would change this calculus: a measured benchmark round showing
body-content greps dominating agent token spend on indexed projects, the way
the round-5 benchmarks showed whole-file Reads dominating (which justified the
Read redirect this document accompanies).
