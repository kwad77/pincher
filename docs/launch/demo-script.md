# v1.0 demo scripts

Draft recording scripts for [#1538](https://github.com/kwad77/pincher/issues/1538)
(FILE-T). These are recording-ready shot lists, not final video files. Record
against the v0.99 RC or v1.0 tag so the binary, help text, and output envelopes
match what users install.

## Shared Recording Setup

- Terminal: 1280x720 minimum capture area, high-contrast theme, 16-18 px font.
- Shell: clean prompt with repo path visible.
- Audio: USB mic, short room-tone check, no music bed.
- Capture: screen recording plus raw terminal transcript.
- Keep cuts honest: do not hide command runtime except for a visible jump cut
  labeled "indexing completed".
- Avoid dollar figures and named-product comparisons.

## Demo 1: Fresh Clone To First Useful Query

Target length: 90-120 seconds.

### Goal

Show a new user going from a fresh checkout to a concrete answer without
knowing Pincher internals.

### Preflight

- Use a throwaway temp directory.
- Use the release binary on `PATH`.
- Pick a target repo with stable output. Default: `https://github.com/kwad77/pincher`.
- Pick one query that has a durable answer. Default: `Index`.

### Shot List

| Time | Visual | Command / action | Narration |
|---|---|---|---|
| 0:00-0:08 | Empty terminal | `pincher --version` | "This is the v1.0 binary." |
| 0:08-0:22 | Fresh checkout | `git clone --depth=1 https://github.com/kwad77/pincher demo-pincher && cd demo-pincher` | "Start from a normal fresh clone." |
| 0:22-0:38 | Host setup | `pincher init --target=codex` | "Pincher can seed host rules, or you can wire it directly as an MCP server." |
| 0:38-0:58 | First index | `pincher index .` | "The first index builds the local symbol graph and search corpora." |
| 0:58-1:15 | First answer | `pincher web` or MCP `search` demo | "The first useful query should return a specific symbol, not a file list to manually inspect." |
| 1:15-1:35 | Source context | MCP `context` on the selected symbol | "From there the agent can ask for the focused source and nearby dependencies." |
| 1:35-1:50 | Close | show docs links | "The same local server works across the documented host integrations." |

### Acceptance Checks

- Viewer sees the exact release version.
- Viewer sees a real index completion line.
- Viewer sees one useful result before any manual file read.
- Final cut includes links to README, migration guide, and host tutorials.

## Demo 2: Edit-Confidence Loop

Target length: 2-3 minutes.

### Goal

Show how an agent can reduce edit risk before touching code: plan the change,
inspect blast radius, and investigate a failing test.

### Preflight

- Use an indexed Pincher checkout.
- Pick a small code path with stable callers. Default target: a CLI or server
  helper with tests.
- Prepare one synthetic failing test output that names a real test and file.

### Shot List

| Time | Visual | Tool call / action | Narration |
|---|---|---|---|
| 0:00-0:15 | Agent prompt | Ask: "I need to change this helper; what should I inspect first?" | "Before editing, ask for the blast radius." |
| 0:15-0:45 | `plan_change` output | MCP `plan_change` scoped to the file or symbol | "Pincher returns affected symbols, callers, and related tests in a bounded response." |
| 0:45-1:10 | Focused context | MCP `context_for_task` or `context` on top affected symbol | "The agent reads the relevant source instead of walking the whole package." |
| 1:10-1:35 | Edit placeholder | Show a small diff or staged change | "Now the edit has a concrete reading path behind it." |
| 1:35-2:05 | Failing output | Paste failing test output into `investigate_failure` | "If a test fails, Pincher ranks likely suspects from the failure text and graph." |
| 2:05-2:30 | Close | Show `trace` or relevant caller list | "The loop is plan, edit, investigate, then narrow the next read." |

### Acceptance Checks

- `plan_change` output includes bounded affected-symbol/caller sections.
- `investigate_failure` output points at real symbols from the repo.
- The final cut does not claim the agent is always right; it shows how the
  graph narrows the next read.

## Publish-Time TODO

- Replace the default target repo/query if v0.99 RC output changes.
- Capture raw transcripts beside the final video files.
- Add final video URLs to `docs/launch/v1.0-announcement.md` and the landing page.
