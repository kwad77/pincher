# External migration-guide review packet

This packet is for #1390 reviewers validating
[`v0.4-to-v1.0.md`](v0.4-to-v1.0.md) against a real pre-v1.0 install.
It is intentionally checklist-shaped so the review result can be pasted
back into the issue without repo context.

## Reviewer requirements

- You have an existing pincherMCP install older than v1.0.
- You have at least one project that was indexed before the upgrade.
- You can stop and restart the host or MCP session that normally runs
  pincher.
- You are comfortable sharing command output with secrets redacted.

## Before upgrading

Record the current state:

```bash
pincher --version
pincher doctor --json > pincher-doctor-before.json
pincher stats --json > pincher-stats-before.json
```

If your old binary does not support `--json`, paste the human-readable
`pincher doctor` / `pincher stats` output instead.

Back up the DB before the first run with the new binary:

```bash
cp "${XDG_DATA_HOME:-$HOME/.local/share}/pincherMCP/pincher.db" \
  ~/pincher.db.before-v1-review
```

Use the OS-specific data-dir path from the migration guide if you are
not on Linux or if you run with `--data-dir` / `PINCHER_DATA_DIR`.

## Review steps

1. Read the guide's 30-second walkthrough and choose the row matching
   your starting release.
2. Follow only the actions in that row and the common-path walkthroughs
   it references.
3. Install the latest release candidate or stable tag being reviewed.
4. Run the first new-binary command and let migrations complete without
   interruption.
5. Re-index the same project only if the guide told you to.
6. Restart your host or MCP client.
7. Run the post-upgrade probes below.

## Post-upgrade probes

```bash
pincher --version
pincher doctor --json > pincher-doctor-after.json
pincher stats --json > pincher-stats-after.json
pincher verify
```

From your agent host, run one normal code lookup that used to work for
that project. If you use the HTTP gateway, this is enough:

```bash
pincher --http 127.0.0.1:18080 --no-stdio &
PINCHER_PID=$!
curl -fsSL http://127.0.0.1:18080/v1/health
curl -fsSL -X POST -H 'Content-Type: application/json' \
  -d '{"query":"main","project":"<your-project-id-or-path>","limit":5}' \
  http://127.0.0.1:18080/v1/search
kill "$PINCHER_PID"
```

## Report template

Paste this into #1390:

```markdown
### External migration review

- Reviewer:
- Starting pincher version:
- Target pincher version:
- OS / arch:
- Install method:
- Data dir default or custom:
- Project size:
- Starting guide row used:
- Did migrations complete on first new-binary command? yes/no
- Did the guide tell you to re-index? yes/no
- Did you actually need to re-index? yes/no
- Host restarted and able to call pincher? yes/no
- `pincher doctor` advisories after upgrade:

#### Unclear guide sections

#### Undocumented gotchas

#### Command/output excerpts
```

## Maintainer triage

For every reviewer finding:

- If the guide was unclear, patch `v0.4-to-v1.0.md`.
- If behavior is correct but surprising, add a known-limitation or FAQ
  note to the guide.
- If the migration or tool call failed, file a blocking bug for the next
  patch/minor and link it from #1390.
- Do not close #1390 until at least two distinct external reviews are
  linked and every finding has a disposition.
