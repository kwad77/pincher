# Tutorial: Pincher with Goose

About 10 minutes. Wires Pincher into [Goose](https://goose-docs.ai/) as a stdio MCP extension and adds a project-scoped Goose Open Plugins `PreToolUse` hook so Goose's built-in Developer tools get the same guardrails as the Claude hook path.

By the end, Goose can call Pincher tools (`search`, `context`, `trace`, `changes`, `architecture`, and friends) and the hook can steer `developer__shell|developer__text_editor` calls through `pincher hook-check` before broad shell/file-edit actions run.

For the long-form manual see [`docs/reference/`](../reference/README.md).

## What you need

- **Goose** installed ([goose-docs.ai](https://goose-docs.ai/))
- **Go 1.25+** on your `PATH` (or a release binary from [releases](https://github.com/kwad77/pincher/releases/latest))
- A Git repository to point Pincher at

## 1. Install Pincher

```bash
go install github.com/kwad77/pincher/cmd/pinch@latest
pincher --version
```

If you downloaded a release binary instead, use its absolute path in the Goose config examples below.

## 2. Index your project

```bash
cd ~/code/your-project
pincher index
# indexed 42 files, 1238 symbols, 6711 edges in 187ms
```

Incremental on every subsequent run — only changed files are re-parsed.

## 3. Add Pincher as a Goose MCP extension

Goose treats MCP servers as extensions. For a persistent install, edit `~/.config/goose/config.yaml` and add a stdio extension that runs Pincher's supervised provider:

```yaml
extensions:
  pincher:
    name: Pincher
    description: Local code graph search, symbol context, traces, and diff blast radius.
    cmd: pincher
    args: [supervised]
    enabled: true
    type: stdio
    timeout: 300
```

If `pincher` is not on Goose's `PATH`, use an absolute path:

```yaml
    cmd: /home/you/go/bin/pincher
    args: [supervised]
```

`supervised` keeps the MCP provider stable across crashes and binary upgrades. You can rebuild Pincher and the next tool call restarts the inner server instead of forcing a manual Goose reconnect.

For a one-off session without editing config, start Goose with Pincher enabled for just that session:

```bash
goose session --with-extension "pincher supervised"
```

For automated smoke tests in a busy dogfood environment, prefer the repository helper:

```bash
./scripts/goose-pincher-smoke.sh
```

The helper snapshots the Pincher SQLite database into a temporary `PINCHER_DATA_DIR` before launching Goose. That keeps the verification read-only and avoids false negatives when the live watcher/indexer is holding SQLite's write lock.

## 4. Install the project-scoped Goose hook extension

Run this from the repository root:

```bash
pincher init --target=goose
```

This writes `.agents/plugins/pincher/`:

```text
.agents/plugins/pincher/
├── README.md
├── plugin.json
├── hooks/
│   └── hooks.json
└── scripts/
    └── pincher-hook-check.sh
```

The hook registration is intentionally narrow:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "developer__shell|developer__text_editor",
        "hooks": [
          {
            "type": "command",
            "command": "${PLUGIN_ROOT}/scripts/pincher-hook-check.sh"
          }
        ]
      }
    ]
  }
}
```

The script receives Goose's raw hook JSON on stdin, appends a local debug copy to `.agents/plugins/pincher/last-event.log`, and pipes the payload to `pincher hook-check`.

Use `--dry-run` if you want to inspect the files before writing:

```bash
pincher init --target=goose --dry-run
```

Use `--no-hook` only if you want the project README/policy text without the runtime `PreToolUse` bridge.

## 5. Start Goose and verify Pincher tools are visible

Start or restart Goose after changing the extension config:

```bash
goose session
```

Ask Goose something that should be answered with code-navigation tools, not whole-file reads:

> *"Use Pincher to find the payment processing entry point, then trace its callers before editing anything."*

A good first response should call Pincher's MCP tools — typically `search`, then `context` or `trace` — before using broad shell commands or opening whole files. Each Pincher response carries a `_meta` envelope such as:

```json
"_meta": {
  "tokens_used": 312,
  "tokens_saved": 14500,
  "tokens_saved_pct": 97.9,
  "baseline_method": "full_file_read",
  "latency_ms": 2
}
```

## 6. Verify the hook path fires

Trigger a safe Developer-tool action from Goose, for example:

> *"List the files at the project root."*

Then check the project plugin log:

```bash
test -s .agents/plugins/pincher/last-event.log && tail -40 .agents/plugins/pincher/last-event.log
```

You should see a `PreToolUse` payload for `developer__shell` or `developer__text_editor`. If `pincher hook-check` blocks or redirects a risky raw read/grep/edit action, Goose receives that hook result before the tool call executes.

## Notes for Goose users specifically

- **Two pieces are required for the best loop.** The MCP extension gives Goose Pincher tools. `pincher init --target=goose` gives the project a runtime hook bridge for Goose Developer-tool calls. The init target does not edit `~/.config/goose/config.yaml` for you.
- **Project-scoped by design.** Goose Open Plugins live under `.agents/plugins/<name>/`, so the hook extension travels with the repo and can be reviewed in code review.
- **Matcher scope is deliberate.** The generated hook watches `developer__shell|developer__text_editor`, the built-in Goose Developer tools most likely to do broad filesystem/shell work that Pincher can narrow.
- **Debug locally.** `.agents/plugins/pincher/last-event.log` is written for troubleshooting and should usually be gitignored or deleted before committing.
- **PATH matters.** Goose launches extensions outside your interactive shell in some environments. If Goose cannot start Pincher, switch `cmd: pincher` to the absolute path from `command -v pincher`.

## Troubleshooting

**Goose does not show Pincher tools.** Confirm `~/.config/goose/config.yaml` has the `extensions.pincher` block, `enabled: true`, `type: stdio`, and `cmd` points to a working binary. Restart Goose after editing default extension config.

**Goose startup reports `database is locked` or `SQLITE_BUSY`.** A Pincher watcher/index pass can hold the live SQLite writer lock long enough for a new stdio extension process to fail startup. First run `pincher health-check`; if Pincher itself is healthy, use `./scripts/goose-pincher-smoke.sh` for a read-only verification snapshot, or retry the normal Goose session after the index pass completes. For persistent daily use, keep the `supervised` extension config so Goose reconnects cleanly after transient Pincher restarts.

**Goose appears to hang while connecting the Pincher extension.** Check the latest Goose CLI log under `~/.local/state/goose/logs/cli/` and look for repeated MCP `ToolListChangedNotification` warnings or a Pincher startup error. Clean up stale failed Goose extension child processes, verify `pincher health-check`, then rerun the snapshot smoke helper to separate Goose/provider health from live database lock contention.

**`pincher init --target=goose` succeeded but Goose still uses raw reads first.** The init target installs the hook extension and README/policy under `.agents/plugins/pincher/`; it does not add the MCP extension config. Add the stdio extension block above so Goose has Pincher tools available.

**Hook events never appear in `last-event.log`.** Make sure you started Goose from the repository root or a workspace where Goose discovers `.agents/plugins/pincher/`. Re-run `pincher init --target=goose --dry-run` to confirm the hook registration and script path.

**The hook script says `pincher: command not found`.** Put Pincher on Goose's `PATH` or edit `.agents/plugins/pincher/scripts/pincher-hook-check.sh` to call the absolute Pincher binary path.

## What to read next

- [Reference → MCP tools](../reference/tools.md) — every tool, every parameter
- [Integration benefits → Goose](../integrations/goose/benefits.md) — what Pincher changes for Goose users
- [Tutorial: Claude Code](claude-code.md) — same guardrail idea with Claude hooks
- [`docs/integrations/loop-leverage-layers.md`](../integrations/loop-leverage-layers.md) — the three-layer agent-leverage frame

---

_Last reviewed: Goose Open Plugins hooks and Pincher goose init target, 2026-06-09._
