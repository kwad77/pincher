# Releasing pincherMCP

This document describes how releases are cut and what guarantees they make.
The contract here is what a downstream packager (Homebrew, the Claude plugin,
Docker users) can rely on.

## Versioning

pincherMCP follows [SemVer](https://semver.org/) with the following promises:

- **MAJOR** — breaking schema changes that aren't safe via additive migration,
  removal of MCP tools, or changes that require downstream callers to adapt.
- **MINOR** — additive schema migrations, new MCP tools, new fields on
  existing tool responses, deprecations.
- **PATCH** — bug fixes, performance improvements, internal refactors with
  no observable surface change.

### Schema-freeze policy (post-1.0)

Once 1.0 is tagged, the SQLite schema becomes part of the public contract:

- The `1.x` line ships only **additive** migrations: new tables, new columns
  with defaults, new triggers, new FTS5 indexes. A 1.x build can read every
  prior 1.x DB after running `migrate()` on Open.
- Breaking schema changes (column type changes, dropped tables, renamed
  columns) require a `2.0` major bump and a documented migration path.
- The `pincher` binary refuses to open a DB at a higher schema version than
  it understands (#10 — downgrade-safety guard, shipped in v0.2.1). This
  is load-bearing for the multi-binary scenarios (plugin + Homebrew + stray
  download all sharing one `pincher.db`).

### Tool-contract policy (post-1.0)

The 29 MCP tools documented in `docs/reference/tools.md` and their JSON Schemas
are pinned by golden-file tests in `internal/server/`. After 1.0:

- **Adding** a new tool, or new fields on an existing tool's input or output,
  is a **MINOR** bump.
- **Removing** a tool, or removing/renaming a field, is a **MAJOR** bump.
- **Behaviour changes** that callers can observe (default value flips,
  filter additions like `min_confidence`'s default move from 0.0 to 0.7)
  are MINOR if the change is opt-out (callers can pass the old default
  explicitly), MAJOR if it isn't.

A failing golden-file diff is the gate — reviewers see exactly what changed
and whether the version bump matches.

## Release-prep checklist

The release-prep PR (the one before tagging) MUST touch all items below.
CHANGELOG-only is the historical mistake — the README is what users hit first
via the GitHub repo landing page, and stale roadmap claims erode trust faster
than missing CHANGELOG entries.

1. **`/codex:adversarial-review` (master vs previous tag, manual)** — BEFORE
   opening the release-prep PR, run `/codex:adversarial-review` against
   `git diff <previous-tag>..master` and triage findings:
   - **sev-1 (canonical workflow break / silent-confidently-wrong /
     supply-chain risk)** → fix or open a blocking issue before tagging.
     Do not tag with sev-1 open.
   - **sev-2 (real bug, doesn't break a canonical workflow)** → file with
     `dogfood-found` + `severity-2` + appropriate `axis-*` label; route per
     the dogfood routing table.
   - **sev-3 (polish / docs / UX gap)** → file with `severity-3`; let the
     next reserve slot pick it up.
   - **Waived findings** → note inline in the release-prep PR body under a
     "Codex review: waived" subsection, with one-line rationale per finding.

   Rationale: per-PR review catches per-PR defects; release-window review
   catches cross-PR emergent shape (three orthogonal PRs that each pass
   review individually but together produce a confused tool surface).
   Codex's adversarial mode is the cheapest second-opinion gate available;
   running it once per release ~10-30 min vs. shipping a sev-1
   silent-wrong to v1.0 dogfood users. Manual / human-in-the-loop by
   design.

2. **`CHANGELOG.md`** — run `bash scripts/changelog-assemble.sh --apply`
   to fold per-PR `CHANGELOG.d/<num>.<type>.md` stubs into the
   `[Unreleased]` section, then convert `[Unreleased]` → versioned heading
   with the release's theme one-liner. Stub-file convention shipped #694;
   legacy direct-edit still works for in-flight PRs that predate it.

3. **GitHub release "What's New"** — draft the release body with a clear
   "What's New" section describing the actual work landed since the
   previous tag. This is separate from the terse changelog: the changelog
   is the audit trail; "What's New" is the human summary users see on the
   release page.

4. **`README.md` roadmap table** — bump the previous `🚧 in flight` row to
   `✅ shipped`, add a new row for the version about to ship with its theme
   one-liner, optionally add the next `🚧 in flight` row.

5. **`README.md` Known limitations** — rewrite any item whose fix lands in
   this version into past tense; recommend the upgrade.

6. **Version-sensitive claims in README leading paragraph** — tool count,
   schema version, coverage badge if it moved meaningfully (>1%).

7. **`docs/reference/README.md` — leading metadata line** (`**Schema
   version:** vN · **MCP tools:** N · **Languages detected:** ~N`). Bump
   every release that moves any of those numbers. Per #688: the leading
   line is what users see first when they click into the reference doc
   from README; stale numbers there make every subsequent claim look
   distrust-by-default. Drift was 12 schema versions before #698 caught
   it.

8. **`docs/` (GitHub Pages site)** — audit `docs/index.html`,
   `docs/release-channels.md`, `docs/streamable-http.md`,
   `docs/troubleshooting.md`, `docs/deployment/*.md`, `docs/tutorials/*.md`
   for version-sensitive claims. The grep that catches drift:
   `grep -rnE "v0\.[0-9]+|pincher-v0\.[0-9]+|[0-9]+ MCP tools|schema.{0,15}v[0-9]+" docs/`
   against the previous release version. Pages renders polished landing
   copy from `docs/` — install tarball filenames, savings-stat
   parentheticals, badge value ranges, forward-looking copy about
   features that did/didn't ship — all higher-visibility than README to
   search-engine traffic. v0.67 release-prep missed `docs/index.html`
   "v0.66" parenthetical + `pincher-v0.66.0-linux-amd64.tar.gz` install
   snippet; caught next morning in a catch-up PR. Don't repeat.

9. **Bench baseline decision** — decide whether the release refreshes
   `testdata/bench/{index,server}.bench.txt`. **Default: skip** for patch
   releases and feature releases that don't intentionally change perf
   shape. **Refresh** for `.x9` hardening releases (workstream 2 of the
   hardening umbrella — see `#672` shape) and for any release that ships
   a deliberate perf-affecting refactor whose new numbers ARE the
   rationale. Mechanism: trigger `.github/workflows/bench-baseline.yml`
   via `workflow_dispatch` on the Actions UI; download the artifact;
   copy `*.bench.txt` files into `testdata/bench/`; commit. Wrong call:
   refreshing on every release silently absorbs regressions and defeats
   the gate's purpose (per the v0.79 prep audit, the committed baseline
   drifted 8 minors with no enforcement because every release
   auto-refreshed without justification).

10. **`DOGFOOD:` CHANGELOG section** — every release-prep PR adds a
    `DOGFOOD:` subsection to the release's CHANGELOG entry, bullet-listing
    friction found AND fixed in that window. Friction found but NOT fixed
    gets filed as an issue with the `dogfood-found` label, never silently
    dropped. Skipping this section is a release defect (same severity as
    skipping the README roadmap-table bump). Rationale: dogfood-found
    work is the dominant source of pre-1.0 fixes; making it auditable
    per release lets us see the planned-vs-discovery ratio and trigger
    the volume-based axis-escalation rule.

If a release ships without README touched, the user's first reaction is
"the README didn't say anything about it" and follow-up cleanup PRs read
as forgetting, not catching up. Do it inline.

After tag pushes, the auto-bump workflow handles the Homebrew formula and
Docker image — those don't go in the release-prep PR itself.

**Post-tag install validation (#1337).** `.github/workflows/install-validation.yml`
fires automatically on every published release: it downloads each
platform's release artifact (tarball/zip/docker) and asserts
`pincher --version` matches the tag. After tagging, confirm the
`Install validation` run went green — `direct` (6 cells) + `docker`
(2 cells) gate every release; `brew` + `scoop` cells run only on stable
tags (channel-gated) and are skipped on dev releases. A red
`direct`/`docker` cell means the released binary for that platform
doesn't run — treat as a release-blocking defect. The harness is also
`workflow_dispatch`-able against any past tag from the Actions UI.

## Release procedure

This is the manual procedure for cutting a release. Once we set up automated
release notes from CHANGELOG.md, the human steps shrink to "tag and push".

### 1. Pre-flight (master)

- All in-flight PRs that should ship in this release are merged.
- `go test ./...` is green on master.
- `make corpus-test` is green (pinned-corpus snapshots match).
- `make corpus-bench` (advisory) — surface any regressions for review;
  not a blocker pre-1.0.
- CHANGELOG.md `[Unreleased]` section is populated and ready to be
  promoted to a versioned section.

### 2. Update CHANGELOG.md

- Move `[Unreleased]` content under a new versioned heading with the
  release date.
- Add the new version at the bottom of the link-reference table.
- Recreate an empty `[Unreleased]` section at the top.
- Commit with message `release: prep CHANGELOG for vX.Y.Z`.

### 3. Tag

```bash
# Annotated tag with release notes inline
git tag -a vX.Y.Z -m "$(awk '/^## \[vX.Y.Z\]/,/^## \[/' CHANGELOG.md | head -n -1)"
git push origin vX.Y.Z
```

The `release` GitHub Actions workflow picks up the tag, builds binaries
for `linux/darwin/windows × amd64/arm64`, builds the multi-arch Docker
image, and publishes the GitHub Release with auto-generated artifacts.

### 4. Verify artifacts

- GitHub Releases page shows the new version with binaries + SHA256SUMS.
- `ghcr.io/kwad77/pincher:X.Y.Z` and `:latest` resolve.
- Homebrew formula update PR auto-opens (the `homebrew-auto-bump`
  workflow runs on tag push).

### 5. Verify the Homebrew bump

After the formula PR opens, run the local smoke test:

```bash
brew uninstall pincher
brew untap kwad77/pincher
brew tap kwad77/pincher
brew install pincher
pincher --version
```

Once verified, merge the formula PR.

## Branch policy

### Pre-1.0 (current)

Direct merges to `master` are allowed. PRs are still preferred for non-trivial
changes and remain the historical record (CHANGELOG.md links by PR number).

### Post-1.0

Master is protected:

- All work happens on `feat/*`, `fix/*`, `chore/*`, `docs/*`, `test/*`,
  `perf/*`, `release/*` branches.
- PR + green CI required to merge.
- Squash-merge by default; merge-commit only for cross-cutting refactors
  where the per-commit history is informative.
- Tags only from `master`.
- Force-push to master is forbidden.

## Hotfix procedure

If a critical bug ships in `vX.Y.Z` and master has unrelated work in flight:

1. Branch `hotfix/vX.Y.(Z+1)` from `vX.Y.Z` (not master).
2. Fix the bug, add a regression test, get PR + green CI.
3. Merge to master (or a `release/X.Y` branch if master has diverged).
4. Cherry-pick the fix back to master if needed.
5. Tag `vX.Y.(Z+1)` from the hotfix point and follow the regular release
   procedure from step 2.

For pre-1.0, hotfixes are usually just a regular patch release off master
since master moves slowly enough.
