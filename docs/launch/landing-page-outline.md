# v1.0 landing page rewrite — outline + measured-value slots

Scaffolding for [#1537](https://github.com/kwad77/pincher/issues/1537) (FILE-S). The actual landing-page rewrite (`docs/index.html`) is content-fill against the final RC numbers measured by the release gates. This doc holds the outline plus the measured-value slots that the final launch prep resolves.

## Why scaffold now (8 minors before the rewrite)

Two reasons:

1. **The numbers come from elsewhere.** Every claim in the v1.0 landing page lands here as a `<<placeholder>>` cross-linked to the gate that produces the real number. Final RC prep is "fill in the numbers" — not "design from scratch" — which keeps the rewrite predictable when launch pressure arrives.
2. **Inconsistent claims are the failure mode.** v0.66/v0.67's `docs/index.html` is stale (the v0.79 release-prep audit caught it). Pinning every numeric claim to a methodology link prevents drift.

## Outline (target order on the landing page)

### Above the fold (hero section)

- **Tagline**: one sentence about the product.
- **Headline metric**: `<<bytes_ratio_baseline>>` × less context disk to find code (anchor: [FILE-A methodology](../methodology/token-savings.md) + [FILE-B comparator](../methodology/external-comparator.md)).
- **Primary CTA**: install snippet (Homebrew + Scoop + Docker), pinned to current `latest` channel.

### Section 1 — "What it does"

Concrete capability tour, no marketing adjectives. Pick 3–4 of these and 1-line each with a screenshot or short transcript:
- `search({"query":"..."})` — BM25 over symbol corpus.
- `symbol({"id":"..."})` — byte-offset O(1) source read.
- `context({"id":"..."})` — symbol + its dependencies (`<<context_avg_bytes>>` per call typical).
- `trace({"id":"...","direction":"inbound"})` — caller graph with risk labels.
- `investigate_failure({"error_text":"..."})` — stack-trace → ranked suspects.

### Section 2 — "What it costs"

Three claims, every number a `<<placeholder>>`:

| Workload | Pincher | Without pincher | Source |
|---|---|---|---|
| Find a function | `<<find_pincher_ms>>` ms / `<<find_pincher_bytes>>` B | `<<find_raw_ms>>` ms / `<<find_raw_bytes>>` B | [FILE-B](../methodology/external-comparator.md) |
| First useful query, fresh clone | `<<ttfs_ms>>` ms total | n/a | [FILE-Q](../methodology/time-to-first-success.md) |
| Memory at 50k indexed files | `<<peak_rss_50k>>` MiB | n/a | [FILE-I](../methodology/resource-pressure.md) |

### Section 3 — "What's stable"

- **Frozen v1.0 surface** — link to [ADR-0002](../adr/0002-v1-frozen-surface.md) (PR #1547).
- **Semver promise** — link to [CONTRIBUTING § Semver](../../CONTRIBUTING.md) (FILE-L).
- **Supported languages table** — 22 extractors, confidence tiers — link to [`docs/reference/languages.md`](../reference/languages.md) FILE-N rows.

### Section 4 — "Hosts"

Per-host install snippet and link to per-host tutorial. Show the host-conformance status badge from [FILE-M](../methodology/host-conformance.md) per host. Hosts: Claude Code, Cursor, Codex, JetBrains, VS Code Copilot, Zed.

### Section 5 — "Security + supply chain"

Three bullets:
- **Signed releases** — link to [release-signing.md](../security/release-signing.md) (FILE-E #1524).
- **Vulnerability scanning** — link to dep-upgrade procedure (FILE-F #1525).
- **Threat model** — link to [threat-model.md](../security/threat-model.md) (FILE-D #1523).

### Section 6 — "Migration"

Two paths:
- Upgrading from v0.x to 1.0 — link to migration guide (#1390 external review pre-1.0).
- First-time install — link to per-host tutorial.

### Footer

License (MIT), repo link, issues link, releases page link.

## Numeric placeholders (full list)

This is the final launch-prep checklist. Every `<<placeholder>>` resolves to a number measured by a release gate. The release-prep PR walks this list top-to-bottom and records the source run next to every published number.

| Placeholder | Source | Released by |
|---|---|---|
| `<<bytes_ratio_baseline>>` | FILE-B comparator JSON | final RC gate |
| `<<context_avg_bytes>>` | per-tool latency budget + savings methodology | final RC gate |
| `<<find_pincher_ms>>` / `<<find_pincher_bytes>>` / `<<find_raw_ms>>` / `<<find_raw_bytes>>` | FILE-B | final RC gate |
| `<<ttfs_ms>>` | FILE-Q time-to-first-success baseline | final RC gate |
| `<<peak_rss_50k>>` | FILE-I resource-pressure dispatch run | final RC gate |
| `<<supported_languages_count>>` | `docs/reference/languages.md` FILE-N table count | live |
| `<<frozen_surface_count>>` | ADR-0002 frozen-status row count | v0.84 (live) |
| `<<host_count>>` | host-conformance corpus directory count | v0.91 |

The v0.97 release-prep PR replaces every placeholder with the measured number. Numbers older than the previous .x9 release-prep cycle are re-measured before being published.

## Don't do this in this PR

- Author actual landing-page HTML — that's the v0.97 work, not now.
- Publish any numeric claim — every number is a placeholder until the gate that produces it has soaked.
- Compare against named alternatives — per `feedback_no_comparisons_pre_1_0.md`, no head-to-head vs Sourcegraph / Cody / Copilot / Cursor until v1.x+.

## Related

- [#1537](https://github.com/kwad77/pincher/issues/1537) FILE-S — the v0.97 landing-page rewrite.
- [#1538](https://github.com/kwad77/pincher/issues/1538) FILE-T — launch artifacts (sibling).
- `docs/index.html` — the current GitHub Pages landing (last rewritten v0.66/v0.67, audited stale).
