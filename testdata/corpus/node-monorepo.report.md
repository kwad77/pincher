# Pincher report: node-monorepo

Generated: 2023-11-14T22:15:00Z

## Project

- ID: `/corpus/node-monorepo`
- Path: `/corpus/node-monorepo`
- Indexed: 2026-01-01T00:00:00Z
- Binary version: `unknown`
- Files: 5 · Symbols: 29 · Edges: 1

## Languages

- JSON: 11 symbols
- JavaScript: 10 symbols
- Markdown: 4 symbols
- TypeScript: 4 symbols

## Node kinds

- Setting: 11 symbols
- Method: 8 symbols
- Section: 4 symbols
- Class: 2 symbols
- Function: 2 symbols
- Variable: 2 symbols

## Edge kinds

- CALLS: 1 edges

## Advanced graph export

- Escape hatch: run `pincher export-graph --project "/corpus/node-monorepo" --format json` for deterministic node/edge JSON that round-trips against the indexed DB counts.
- Tool: `mcp_pincher_context`
  - Args: `{"project":"/corpus/node-monorepo","id":"cmd/pinch/export_graph.go::main.writeGraphJSON#Function"}`
  - Why: inspect the export-graph JSON writer before building advanced external graph analysis.

## Entry points

- none found in the current index

## Hotspots

- `Greeter` Class — `src/index.ts` (incoming calls: 1)
  - Risk score: 3 (inputs: incoming=1, outgoing=0, degree=1, test-adjacent=0, confidence=0.98)

## Rationale / design intent

- none found in the current index

## Surprising connections

- none found in the current index

## Suggested next Pincher calls

- Tool: `mcp_pincher_context`
  - Args: `{"project":"/corpus/node-monorepo","id":"src/index.ts::src.index.Greeter#Class"}`
  - Why: inspect the top hotspot before editing it.
  - Expected value: reduces risky raw reads and grounds edits in symbol provenance.
- Tool: `mcp_pincher_trace`
  - Args: `{"project":"/corpus/node-monorepo","id":"src/index.ts::src.index.Greeter#Class","direction":"inbound"}`
  - Why: map callers for the highest-incoming hotspot before behavior changes.
  - Expected value: exposes blast-radius risk for planning and routing escalation.
- Tool: `mcp_pincher_changes`
  - Args: `{"project":"/corpus/node-monorepo","scope":"all"}`
  - Why: run before finalizing edits to map changed-symbol blast radius.
  - Expected value: turns the report into an execution loop with measurable impact checks.

## Provenance

This report is generated from Pincher's existing symbol and edge index. Missing data is reported as missing rather than inferred.
