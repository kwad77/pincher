# Pincher report: python-app

Generated: 2023-11-14T22:15:00Z

## Project

- ID: `/corpus/python-app`
- Path: `/corpus/python-app`
- Indexed: 2026-01-01T00:00:00Z
- Binary version: `unknown`
- Files: 5 · Symbols: 16 · Edges: 4

## Languages

- Python: 9 symbols
- TOML: 4 symbols
- Markdown: 3 symbols

## Node kinds

- Setting: 4 symbols
- Function: 3 symbols
- Module: 3 symbols
- Section: 3 symbols
- Method: 2 symbols
- Class: 1 symbols

## Edge kinds

- CALLS: 3 edges
- IMPORTS: 1 edges

## Advanced graph export

- Escape hatch: run `pincher export-graph --project "/corpus/python-app" --format json` for deterministic node/edge JSON that round-trips against the indexed DB counts.
- Tool: `mcp_pincher_context`
  - Args: `{"project":"/corpus/python-app","id":"cmd/pinch/export_graph.go::main.writeGraphJSON#Function"}`
  - Why: inspect the export-graph JSON writer before building advanced external graph analysis.

## Entry points

- none found in the current index

## Hotspots

- `open_session` Function — `app/auth.py` (incoming calls: 2)
  - Risk score: 7 (inputs: incoming=2, outgoing=1, degree=3, test-adjacent=0, confidence=1.00)
- `Session` Class — `app/auth.py` (incoming calls: 1)
  - Risk score: 3 (inputs: incoming=1, outgoing=0, degree=1, test-adjacent=0, confidence=1.00)

## Rationale / design intent

- none found in the current index

## Surprising connections

- none found in the current index

## Suggested next Pincher calls

- Tool: `mcp_pincher_context`
  - Args: `{"project":"/corpus/python-app","id":"app/auth.py::app.auth.open_session#Function"}`
  - Why: inspect the top hotspot before editing it.
  - Expected value: reduces risky raw reads and grounds edits in symbol provenance.
- Tool: `mcp_pincher_trace`
  - Args: `{"project":"/corpus/python-app","id":"app/auth.py::app.auth.open_session#Function","direction":"inbound"}`
  - Why: map callers for the highest-incoming hotspot before behavior changes.
  - Expected value: exposes blast-radius risk for planning and routing escalation.
- Tool: `mcp_pincher_changes`
  - Args: `{"project":"/corpus/python-app","scope":"all"}`
  - Why: run before finalizing edits to map changed-symbol blast radius.
  - Expected value: turns the report into an execution loop with measurable impact checks.

## Provenance

This report is generated from Pincher's existing symbol and edge index. Missing data is reported as missing rather than inferred.
