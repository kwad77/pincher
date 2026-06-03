# Dogfood routing

When a probe surfaces net-new work mid-flight, route by **type**, not just
severity. Sloppy routing either silently bloats planned scope or loses
the finding entirely. Long-form roadmap context lives in
[`.planning-roadmap-to-v1.md`](../../.planning-roadmap-to-v1.md); the
rules below are the autonomous-loop quick-reference.

## Routing table

| Discovery shape | Lands in |
|---|---|
| **Regression in shipped code** — canonical workflow broken | Next patch (v0.81.x / v0.82.x). Ships within days, doesn't wait for the next minor. |
| **Bug in current in-flight PR** | Same PR or a sibling PR before merge. Never punted forward. |
| **Bug in shipped feature, silent-wrong / misleading** | Next minor's dogfood reserve slot. |
| **Net-new capability gap** — missing composite / doctor advisory / tool | Triage: v1.0 blocker → insert into nearest minor with reserve room. Otherwise → file with `v1.x` milestone. |
| **Perf gap crossing a published claim** | v1.0 blocker. Next `.x9` hardening minor. |
| **Schema / API surface issue** | **Before v0.84 API-freeze checkpoint:** next minor. **After v0.84:** slips v1.0 unless it's a corruption bug. |
| **Docs / UX polish** | Next reserve slot, or v0.95 final dogfood reserve, whichever closer. |

## Decision authority (no over-asking)

- **Severity-1 / canonical workflow break** → file + ship as patch without asking.
- **Bug in in-flight PR** → fix in same/sibling PR, mention in PR body.
- **Net-new capability surface** (new tool / composite / advisory) → file with recommended milestone, then ask before assigning.
- **API-surface change after v0.84 freeze** → ALWAYS ask, never assume.

## Buffer + overflow rules

- **Soft overflow:** when a minor's dogfood reserve fills, the next 2–3 items roll forward one minor. Planned items keep their slot; the polish scope of the receiving minor absorbs the overflow.
- **Hard overflow signal:** reserve overflows by >50% in two consecutive minors → planned-vs-discovery ratio is wrong. Pause planned work, drain dogfood first, update [`.planning-roadmap-to-v1.md`](../../.planning-roadmap-to-v1.md).
- **Dogfood beats planned:** dogfood-found work that compromises a v1.0 surface guarantee takes priority over any `FILE-X` item from the roadmap.

## Volume-based axis escalation

If the same axis (e.g., `axis-extractor-bash`, `axis-trace-bidirectional`) generates >3 `dogfood-found` issues in a release window, that axis gets a dedicated hardening slot in the next `.x9`, not patched piecemeal. Precedent: v0.56 resolver bug family (4 related fixes that should have been one batched session).

## Tagging discipline (makes routing auditable)

Every probe-surfaced issue gets:

- `dogfood-found` — distinct from user-reported, so we can audit "how much v1.0 work came from dogfood vs planning".
- `axis-{ast,doctor,cli,trace,fts,extractor-go,extractor-bash,...}` — feeds the >3-in-a-release escalation trigger.
- `severity-{1,2,3}` — drives the routing table above.
