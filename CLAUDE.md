# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Pincher Usage Policy

This project ships pincherMCP — its own product — and dogfoods it. Prefer pincher tools over `Read`/`Grep`/`Glob` for any code-navigation task.

**Workflow:** `architecture` (orient) → `search` (find) → `context` or `symbol` (read) → `trace` (impact) → edit → `changes` (verify before push).

**Fall back to `Read`/`Grep` when:**
- Pincher returns no result (rare for code; common for config/text files).
- You need exact-byte inspection (whitespace audits).
- The file isn't indexable (binaries, large lockfiles).
- You're authoring a new file.
- **The pincher freshness check fires** (see below).

If `mcp__pincher__*` tools aren't in the registry at session start, surface a one-line warning before the first response and fall back to `Read`/`Grep`.

### Pincher freshness check (this repo specifically)

This is pincher's own repo, so the running MCP server is frequently stale relative to master. **Once per session, call `health`. If `running_binary_version` differs from the project's `schema_version_at_index` for `sessionRoot`, treat byte-offset tools (`symbol`, `context`, `neighborhood with include_source=true`) as untrusted — bytes may point at the wrong span. Discovery tools (`search`, `query`, `trace`, `changes`) stay reliable.** Use `Read` for the untrusted reads until the binary is rebuilt and `/mcp` reconnects.

## Release process

- **Minor** (`0.X.0`) — features, schema migrations, new CLI surface.
- **Patch** (`0.X.Y`) — bug fixes only. No features, no schema changes.
- **Major** — reserved for 1.0+.

**Every PR must be assigned to a milestone at PR-create time.** Milestones live at https://github.com/kwad77/pincher/milestones. Default to the next milestone; don't leave a PR unassigned. A release ships when its milestone hits 100% closed.

The full **10-item release-prep checklist** — codex adversarial review, CHANGELOG assembly, GitHub release notes, README roadmap/limitations/leading-paragraph updates, `docs/reference/README.md` metadata, `docs/` Pages-site audit, bench baseline decision, DOGFOOD subsection, plus post-tag install validation — lives in [`RELEASING.md` → Release-prep checklist](RELEASING.md#release-prep-checklist). Don't skip.

## Dogfood routing

When a probe surfaces net-new work mid-flight, route by **type**, not just severity. The full routing table, decision-authority rules, buffer/overflow policy, volume-based axis escalation, and issue-tagging discipline live in [`docs/process/dogfood-routing.md`](docs/process/dogfood-routing.md). Long-form roadmap context: [`.planning-roadmap-to-v1.md`](.planning-roadmap-to-v1.md).

## CI conventions (AI-loop-relevant)

- **Wakeup timing:** Windows test queues 4–7 min behind ubuntu/mac. When polling CI, schedule a 270s wakeup (not 60s) — fits inside the 5-min cache TTL twice.
- **Stub-file convention for CHANGELOG (#694):** drop `CHANGELOG.d/<num>.<type>.md` with one bullet (no leading dash; assembler adds it). `<type>` ∈ {added, changed, fixed, removed}.

Full gate inventory + skip rules in [`CONTRIBUTING.md` → What CI gates require](CONTRIBUTING.md#what-ci-gates-require).

## Where the rest lives

- **Test gates + JSON response invariants + idioms** (lockstep-update rules, `[]T{}` nil-slice rule, slog / reader-pool / `MakeSymbolID` / InputSchema-backticks gotchas) → [`CONTRIBUTING.md` → Test conventions / JSON response invariants / Idioms](CONTRIBUTING.md#test-conventions).
- **Architecture** (data flow, three-layer storage, package responsibilities, symbol-ID format, schema migration pattern, key invariants, dependencies) → [`docs/reference/architecture.md`](docs/reference/architecture.md). Canonical source — don't duplicate here.
- **Known architectural limitations** (regex gap, YAML/JSON sequence-rename, single-user SQLite, HTTP auth posture, batch caps) → [`README.md` → Known limitations](README.md#known-limitations).
