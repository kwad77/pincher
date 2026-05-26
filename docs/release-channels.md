# Release channels

> Operational reference for the pincher release-channel scheme (#642). Defines what each channel means, how to install from each, and the cadence rule that promotes a release from `dev` to `stable`.

## Channels at a glance

| Channel | When it ships | Stability commitment | Install path |
|---|---|---|---|
| **stable** | Every `vX.Y.Z` where `Y % 10 == 0` (v0.60, v0.70, ..., v1.0, v1.10) | Hardened. Sign-off doc on the `.X9` hardening release. Promoted with confidence. | `brew install kwad77/pincher/pincher` · `docker pull ghcr.io/kwad77/pincher:latest` · `go install github.com/kwad77/pincher/cmd/pinch@latest` |
| **dev** | Every other plain semver tag (v0.53, v0.54, ..., v0.59, v0.61, ...) | New features land. CI gates green but no extended hardening pass. Suitable for evaluation, opt-in early adoption. | `docker pull ghcr.io/kwad77/pincher:dev` · `go install github.com/kwad77/pincher/cmd/pinch@v0.53.0` (pin the tag) |
| **beta** | Tags with `-beta.N` suffix (v0.54.0-beta.1, v0.90.0-beta.2) | Feature-complete; collecting field feedback before stable promotion. | `docker pull ghcr.io/kwad77/pincher:beta` · `go install github.com/kwad77/pincher/cmd/pinch@v0.54.0-beta.1` |
| **alpha** | Tags with `-alpha.N` suffix | Experimental. Behind feature flags. Expected to break. | `docker pull ghcr.io/kwad77/pincher:alpha` · `go install` against the specific tag |
| **rc** | Tags with `-rc.N` suffix | Release-candidate iteration. Bug fixes only; surface frozen. | `docker pull ghcr.io/kwad77/pincher:rc` |

## The promotion rule

Stable channel = `(minor) % 10 == 0` AND no pre-release suffix.

This is enforced by `scripts/release-channel.sh`, which the release workflow consults to decide:

- Whether to bump the Homebrew formula (only for stable promotions)
- Whether to push the Docker `latest` tag (only for stable promotions)
- Which Docker channel tag to push (`dev`, `beta`, `alpha`, `rc`, or `latest`/`stable`)
- Whether to mark the GitHub release as a pre-release (everything not stable)

Patch releases inherit the channel of their minor:

- `v0.60.1` → stable (patch on a stable promotion)
- `v0.53.1` → dev (patch on a dev release)
- `v0.60.0-beta.2` → beta (pre-release suffix wins over the modulo rule)

## When to install from each channel

- **stable** — production deployments, CI integrations, anything that values predictability over freshness. The README install snippet always points here.
- **dev** — you want to evaluate the bedrock-layer features that landed since the last stable promotion. You're comfortable upgrading on each dev tag and reporting bugs back.
- **beta** — you've been invited to validate a release candidate; the team is collecting field feedback before promoting to stable.
- **alpha** — you're contributing to a feature behind a flag and want the latest experimental build. Expect breakage.
- **rc** — you're testing a v1.0-shape candidate during the final RC iteration phase.

## Cadence map

The channel-promotion pattern lines up with the phase structure documented in [#638 v1.0 roadmap](https://github.com/kwad77/pincher/issues/638):

```
v0.52  ──┐
v0.53    │
v0.54    │  Phase 1 — dev channel
v0.55    │  (.55 = dogfood iteration)
v0.56    │
v0.57    │
v0.58    │  ← TESTING release (no features; coverage push)
v0.59    │  ← HARDENING release (bug fixes, sign-off, next-phase backlog)
v0.60  ──┘  ← STABLE PROMOTION

v0.61  ──┐
...      │  Phase 2 (same shape)
v0.70  ──┘  ← STABLE PROMOTION

(repeat through v0.80, v0.90, v1.0)
```

Every `.X9` release publishes a sign-off doc as a comment on its hardening issue (#664 for v0.59, #670 for v0.69, #672 for v0.79, #674 for v0.89, #676 for v0.99). The `.X0` promotion only happens when the sign-off confirms all gates green.

## Reading the channel from a running pincher

The `_meta.capabilities` field on every tool response includes a tag indicating the binary's source channel — currently `schema_v36` and friends advertise feature support, but a future release may add `release_channel` for symmetry with the install-time signal. For now, `pincher --version` plus the [Releases page](https://github.com/kwad77/pincher/releases) is the canonical signal.

Routers and aggregators consuming pincher behind their own surface (zelos, bifrost, detour) can use the `_meta.capabilities` advertisement to decide whether to require the running pincher meets a minimum capability set, regardless of channel.

## Changing channels

There's no in-place channel switch. To change channels, install via the new channel's path:

```bash
# Switch from stable to dev
brew uninstall kwad77/pincher/pincher
docker pull ghcr.io/kwad77/pincher:dev
# (or specific tag via `go install ...@v0.53.0`)

# Switch from dev to stable
docker rmi ghcr.io/kwad77/pincher:dev
brew install kwad77/pincher/pincher
```

## See also

- [#642](https://github.com/kwad77/pincher/issues/642) — release channels design
- [#638](https://github.com/kwad77/pincher/issues/638) — v1.0 roadmap with phase + channel discipline
- `scripts/release-channel.sh` — the canonical detection rule
- `.github/workflows/release.yml` — workflow integration
