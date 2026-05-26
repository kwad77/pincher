# v1.0 launch placeholder audit

Date: 2026-05-26

Scope: [#676](https://github.com/kwad77/pincher/issues/676) v0.99 final
hardening.

This file records the `docs/launch/` placeholder audit for the v0.99 gate.
Launch drafts may keep publish-time slots that cannot be known before the tag,
blog post, demo uploads, or final RC benchmark runs exist. They must not keep
stale placeholders for links or values that are already knowable.

## Audit Command

```bash
rg -n '<<[^>]+>>|TODO|TBD|PLACEHOLDER|FIXME|XXX' docs/launch
```

## Resolved During Audit

The launch drafts now inline stable repo URLs for:

- README: `https://github.com/kwad77/pincher#readme`
- Issues: `https://github.com/kwad77/pincher/issues`
- Migration guide:
  `https://github.com/kwad77/pincher/blob/master/docs/migration/v0.4-to-v1.0.md`
- Frozen surface ADR:
  `https://github.com/kwad77/pincher/blob/master/docs/adr/0002-v1-frozen-surface.md`

## Intentional Publish-Time Slots

These slots are allowed to remain until the named artifact exists.

| Slot | Files | Resolution source |
|---|---|---|
| `<<blog_url>>` | `twitter-thread.md`, `reddit-golang.md`, `internal-slack.md` | Published v1.0 announcement URL |
| `<<release_url>>` / `<<v1.0_release_url>>` | `reddit-golang.md`, `internal-slack.md`, `v1.0-announcement.md` | GitHub `v1.0.0` release URL after tag |
| `<<landing_page_url>>` | `v1.0-announcement.md` | Final Pages URL after launch-page rewrite deploys |
| `<<fresh_clone_demo_url>>` | `twitter-thread.md`, `reddit-golang.md`, `internal-slack.md`, `v1.0-announcement.md` | Published fresh-clone demo URL |
| `<<edit_confidence_demo_url>>` | `twitter-thread.md`, `internal-slack.md`, `v1.0-announcement.md` | Published edit-confidence demo URL |
| `2026-<<MM-DD>>` | `v1.0-changelog-hero.md` | Actual `v1.0.0` tag date |

## Intentional Measured-Value Slots

`landing-page-outline.md` keeps measured-value slots until the final RC
benchmark artifacts are selected for publication:

| Slot | Resolution source |
|---|---|
| `<<bytes_ratio_baseline>>` | FILE-B comparator artifact |
| `<<context_avg_bytes>>` | per-tool latency and savings-methodology evidence |
| `<<find_pincher_ms>>` / `<<find_pincher_bytes>>` / `<<find_raw_ms>>` / `<<find_raw_bytes>>` | FILE-B comparator artifact |
| `<<ttfs_ms>>` | FILE-Q time-to-first-success artifact |
| `<<peak_rss_50k>>` | FILE-I resource-pressure artifact |

## Result

No stale launch placeholders remain as of this audit. Remaining slots are
explicitly tied to publish-time artifacts or final RC measurement artifacts.
