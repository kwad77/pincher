# Pincher report: go-project

Generated: 2023-11-14T22:15:00Z

## Project

- ID: `/corpus/go-project`
- Path: `/corpus/go-project`
- Indexed: 2026-01-01T00:00:00Z
- Binary version: `unknown`
- Files: 3 · Symbols: 12 · Edges: 7

## Languages

- Go: 8 symbols
- External: 2 symbols
- Markdown: 2 symbols

## Node kinds

- Function: 4 symbols
- Module: 4 symbols
- Section: 2 symbols
- Class: 1 symbols
- Method: 1 symbols

## Edge kinds

- CALLS: 4 edges
- IMPORTS: 3 edges

## Advanced graph export

- Escape hatch: run `pincher export-graph --project "/corpus/go-project" --format json` for deterministic node/edge JSON that round-trips against the indexed DB counts.
- Tool: `mcp_pincher_context`
  - Args: `{"project":"/corpus/go-project","id":"cmd/pinch/export_graph.go::main.writeGraphJSON#Function"}`
  - Why: inspect the export-graph JSON writer before building advanced external graph analysis.

## Entry points

- `main` — `cmd/cli/main.go:12`

## Hotspots

- `run` Function — `cmd/cli/main.go` (incoming calls: 1)
  - Risk score: 6 (inputs: incoming=1, outgoing=3, degree=4, test-adjacent=0, confidence=1.00)
- `Greet` Function — `cmd/cli/main.go` (incoming calls: 1)
  - Risk score: 3 (inputs: incoming=1, outgoing=0, degree=1, test-adjacent=0, confidence=1.00)
- `Open` Function — `internal/auth/auth.go` (incoming calls: 1)
  - Risk score: 3 (inputs: incoming=1, outgoing=0, degree=1, test-adjacent=0, confidence=1.00)
- `User` Method — `internal/auth/auth.go` (incoming calls: 1)
  - Risk score: 3 (inputs: incoming=1, outgoing=0, degree=1, test-adjacent=0, confidence=1.00)

## Rationale / design intent

- none found in the current index

## Surprising connections

- `cmd/cli` → `@external`: 1 edge
  - Triage: cross-package edge is uncommon in the current graph and may deserve review; boundary=cross-package coupling; action=inspect the representative edge with Pincher context/trace before changing either package; example `cmd/cli/main.go::cmd/cli#Module` → `@external/fmt::fmt#Module` (IMPORTS, confidence=1.00, source=resolve_pass)
- `internal/auth` → `@external`: 1 edge
  - Triage: cross-package edge is uncommon in the current graph and may deserve review; boundary=cross-package coupling; action=inspect the representative edge with Pincher context/trace before changing either package; example `internal/auth/auth.go::internal/auth#Module` → `@external/errors::errors#Module` (IMPORTS, confidence=1.00, source=resolve_pass)
- `cmd/cli` → `internal/auth`: 3 edges
  - Triage: CLI package reaches across an internal package boundary; boundary=CLI/internal coupling; action=check whether the CLI should use a narrower internal facade before adding more calls; example `cmd/cli/main.go::cmd/cli#Module` → `internal/auth/auth.go::internal/auth#Module` (IMPORTS, confidence=1.00, source=resolve_pass)

## Suggested next Pincher calls

- Tool: `mcp_pincher_context`
  - Args: `{"project":"/corpus/go-project","id":"cmd/cli/main.go::main.run#Function"}`
  - Why: inspect the top hotspot before editing it.
  - Expected value: reduces risky raw reads and grounds edits in symbol provenance.
- Tool: `mcp_pincher_trace`
  - Args: `{"project":"/corpus/go-project","id":"cmd/cli/main.go::main.run#Function","direction":"inbound"}`
  - Why: map callers for the highest-incoming hotspot before behavior changes.
  - Expected value: exposes blast-radius risk for planning and routing escalation.
- Tool: `mcp_pincher_changes`
  - Args: `{"project":"/corpus/go-project","scope":"all"}`
  - Why: run before finalizing edits to map changed-symbol blast radius.
  - Expected value: turns the report into an execution loop with measurable impact checks.

## Provenance

This report is generated from Pincher's existing symbol and edge index. Missing data is reported as missing rather than inferred.
