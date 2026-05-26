# Packaging

Drop-in installers and service templates for running pincherMCP as a
managed process on each major platform. Everything here is optional —
the binary is a single static file and works with nothing but
`pincher --http 127.0.0.1:8080` at a shell.

## Layout

| Path | Target |
|---|---|
| `homebrew/pincher.rb` | Homebrew formula template. Drop into a `homebrew-pincher` tap repo at `Formula/pincher.rb` after a release. |
| `systemd/pincher.service` | Systemd user unit for Linux. Installs to `~/.config/systemd/user/`. |
| `launchd/com.pinchermcp.pincher.plist` | LaunchAgent for macOS. Installs to `~/Library/LaunchAgents/`. |
| `windows/install-service.ps1` | PowerShell installer that wraps the binary with `sc.exe` as a Windows service. |
| `scoop/pincher.json` | Scoop manifest. Install via `scoop install https://raw.githubusercontent.com/kwad77/pincher/master/packaging/scoop/pincher.json` until a dedicated `scoop-pincher` bucket is published (#1260 §1). |

## Per-platform quick start

**macOS (Homebrew tap, once a release is cut):**

```bash
brew tap kwad77/pincher https://github.com/kwad77/homebrew-pincher
brew install pincher
brew services start pincher
```

Without a tap (manual LaunchAgent install):

```bash
cp packaging/launchd/com.pinchermcp.pincher.plist ~/Library/LaunchAgents/
launchctl load ~/Library/LaunchAgents/com.pinchermcp.pincher.plist
```

**Linux (systemd user service):**

```bash
mkdir -p ~/.config/systemd/user
cp packaging/systemd/pincher.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now pincher
journalctl --user -u pincher -f      # tail logs
```

For a system-wide install, move the file to `/etc/systemd/system/` and drop
the `--user` flags.

**Windows (Scoop, #1260 §1):**

```powershell
# Install from the raw URL until a dedicated scoop-pincher bucket is published.
scoop install https://raw.githubusercontent.com/kwad77/pincher/master/packaging/scoop/pincher.json
pincher --version
```

Scoop persists the user's `.pincher/` data dir across upgrades. `scoop update pincher` picks up new releases via the manifest's checkver hook.

**Windows (service via sc.exe):**

```powershell
# In an elevated PowerShell:
cd packaging\windows
.\install-service.ps1 -BinaryPath "C:\tools\pincher.exe"
Start-Service pincher
```

Alternative: Task Scheduler with a logon trigger — no admin required, but
the server only runs while you're signed in.

### First-run OS gate (unsigned binary bypass)

Direct downloads of the binary hit the OS reputation gate on first run. Decision and rationale: [docs/adr/0001-binary-signing-decision.md](../docs/adr/0001-binary-signing-decision.md) (defer code-signing to v1.0+).

**macOS direct download:**

```bash
# Download, extract, and clear the Gatekeeper quarantine bit in one shot.
curl -fsSL https://github.com/kwad77/pincher/releases/download/v0.60.0/pincher-v0.60.0-darwin-arm64.tar.gz | tar xz
xattr -d com.apple.quarantine pincher-v0.60.0-darwin-arm64
./pincher-v0.60.0-darwin-arm64 --version
# Then mv to your PATH:
mv pincher-v0.60.0-darwin-arm64 /usr/local/bin/pincher
```

The `xattr -d` line removes the `com.apple.quarantine` extended attribute that Gatekeeper attaches to downloaded files. Without it, macOS shows *"pincher cannot be opened because the developer cannot be verified"* — single-click Allow via System Settings → Privacy & Security works too. Homebrew installs (above) clear the attribute automatically — no bypass needed there.

**Windows direct download:**

The downloaded `.exe` trips Windows SmartScreen. The dialog says *"Microsoft Defender SmartScreen prevented an unrecognized app from starting"*. Click **More info** → **Run anyway**. Scoop installs (above) avoid the gate entirely; that's the recommended Windows path.

**Docker (unchanged from repo root):**

```bash
PINCHER_HTTP_KEY=$(openssl rand -hex 16)
docker run -d --name pincher \
  -v pincher-data:/data \
  -p 8080:8080 \
  -e PINCHER_HTTP_ADDR=:8080 \
  -e PINCHER_HTTP_KEY \
  ghcr.io/kwad77/pincher:latest
```

## Configuration knobs

All three native-service templates accept the same environment variables
so config stays consistent across platforms:

| Variable | Purpose |
|---|---|
| `PINCHER_HTTP_ADDR` | HTTP listen address (native-service default `127.0.0.1:8080`; container examples use `:8080` with `PINCHER_HTTP_KEY`; use `:0` for OS-picked). |
| `PINCHER_HTTP_KEY` | Bearer token required on every HTTP request. Recommended for non-localhost. |
| `--data-dir` (flag) | Override the SQLite directory (default is platform-appropriate). |

## Release automation

`.github/workflows/release.yml` has a `homebrew` job that fires after every
tag push. It:

1. Downloads the release's `SHA256SUMS` file (produced by the same workflow
   a few steps earlier).
2. Rewrites `packaging/homebrew/pincher.rb` — version line + the four
   Darwin/Linux (arm64/amd64) `sha256` lines + the "Pinned to vX.Y.Z"
   comment — using a Python regex patcher. The patch is diff-verified by
   CI (`git diff --stat`) before committing.
3. Commits the updated formula back to `master` under the
   `github-actions[bot]` identity.
4. Mirrors the exact same file into the external tap repo at
   `kwad77/homebrew-pincher` (path: `Formula/pincher.rb`), so end users
   running `brew upgrade pincher` pick up the bump automatically.

### Required secret

Step 4 cross-repo push needs a PAT; `GITHUB_TOKEN` alone is scoped to the
current repo. The workflow expects a secret named `HOMEBREW_TAP_TOKEN`.

Setup (one-time):

1. Create a fine-grained personal access token at
   [github.com/settings/tokens?type=beta](https://github.com/settings/personal-access-tokens/new).
2. Scope it to **only** the `kwad77/homebrew-pincher` repository.
3. Permissions: `Contents: Read and write`. Nothing else.
4. Add it to the main repo:
   ```
   gh secret set HOMEBREW_TAP_TOKEN --repo kwad77/pincher
   ```

If the secret is missing, the mirror step emits a warning and exits
cleanly — the in-repo formula still gets bumped, but users on the tap
will have to wait for a manual push. The main-repo commit and the tap
push are intentionally independent so a tap outage never blocks a
release.

## Adding a package manager

Other package-manager formulae are welcome — AUR for Arch, Scoop for
Windows, Nix overlays, etc. They all follow the same shape: download the
matching binary archive from the GitHub release, unpack, install to a
PATH location, register a service if the OS has one. Open a PR with the
new manifest and a note in this README.
