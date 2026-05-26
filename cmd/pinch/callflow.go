package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"strings"

	"github.com/kwad77/pincher/internal/db"
)

// callflow.go — `pincher callflow` renders a Mermaid call-flow diagram
// for a symbol: who calls it, what it calls, out to a bounded depth.
// pincher's CALLS edges are deterministic AST facts, so the diagram is
// exact — no inference. Output pastes straight into Markdown, a
// GitHub comment, or the Mermaid live editor.
//
// Read-only; reuses scoped edge lookups. No schema or MCP change.

// callflowNodeCap bounds the diagram so a hub symbol doesn't render a
// 500-node unreadable graph.
const callflowNodeCap = 150

// runCallflowCLI implements `pincher callflow`.
func runCallflowCLI(args []string) {
	os.Exit(callflowCLI(args, os.Stdout, os.Stderr))
}

// callflowCLI is the testable core of runCallflowCLI: it writes to the
// supplied streams and returns the process exit code instead of calling
// os.Exit, so every branch is unit-testable.
func callflowCLI(args []string, stdout, stderr io.Writer) int {
	log.SetOutput(io.Discard)

	fs := flag.NewFlagSet("callflow", flag.ContinueOnError)
	fs.SetOutput(stderr)
	symbolFlag := fs.String("symbol", "", "Symbol name or id to anchor the diagram on (required)")
	projectFlag := fs.String("project", "", "Project name or id (default: the current directory's project)")
	depth := fs.Int("depth", 2, "How many call hops to follow (1-4)")
	direction := fs.String("direction", "both", "callers | callees | both")
	outPath := fs.String("out", "", "Write to this file (default: stdout)")
	dataDir := fs.String("data-dir", "", "Override data directory")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: pincher callflow --symbol=NAME [--depth=2] [--direction=both] [--out=FILE]")
		fmt.Fprintln(stderr, "  Renders a Mermaid call-flow diagram (callers + callees) for a symbol.")
		fmt.Fprintln(stderr, "  Paste the output into Markdown or https://mermaid.live .")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *symbolFlag == "" {
		fmt.Fprintln(stderr, "pincher callflow: --symbol is required")
		fs.Usage()
		return 1
	}
	dir := strings.ToLower(*direction)
	switch dir {
	case "callers", "callees", "both":
	default:
		fmt.Fprintf(stderr, "pincher callflow: unknown --direction %q (want callers, callees, or both)\n", *direction)
		return 1
	}
	d := *depth
	if d < 1 {
		d = 1
	}
	if d > 4 {
		d = 4
	}

	store, _, err := openProjectStore(*dataDir)
	if err != nil {
		fmt.Fprintf(stderr, "pincher callflow: %v\n", err)
		return 1
	}
	defer store.Close()

	project, err := resolveExportProject(store, *projectFlag)
	if err != nil {
		fmt.Fprintf(stderr, "pincher callflow: %v\n", err)
		return 1
	}

	seedID, err := resolveCallflowSeed(store, project.ID, *symbolFlag)
	if err != nil {
		fmt.Fprintf(stderr, "pincher callflow: %v\n", err)
		return 1
	}

	nodes, edges, truncated := collectCallflow(store, project.ID, seedID, dir, d)
	mermaid, err := renderCallflowMermaid(store, project.ID, seedID, nodes, edges, truncated)
	if err != nil {
		fmt.Fprintf(stderr, "pincher callflow: %v\n", err)
		return 1
	}

	out := stdout
	if *outPath != "" {
		f, err := os.Create(*outPath)
		if err != nil {
			fmt.Fprintf(stderr, "pincher callflow: create %s: %v\n", *outPath, err)
			return 1
		}
		defer f.Close()
		out = f
	}
	if _, err := io.WriteString(out, mermaid); err != nil {
		fmt.Fprintf(stderr, "pincher callflow: write: %v\n", err)
		return 1
	}
	if *outPath != "" {
		fmt.Fprintf(stderr, "wrote call-flow for %s (%d nodes) to %s\n",
			shortNameFromID(seedID), len(nodes), *outPath)
	}
	return 0
}

// resolveCallflowSeed turns a --symbol value (an id or a short name)
// into a concrete symbol id within projectID.
func resolveCallflowSeed(store *db.Store, projectID, symbol string) (string, error) {
	if strings.Contains(symbol, "::") {
		sym, err := store.GetSymbolScoped(projectID, symbol)
		if err != nil {
			return "", fmt.Errorf("lookup %s: %w", symbol, err)
		}
		if sym == nil {
			return "", fmt.Errorf("symbol id %q not found in this project", symbol)
		}
		return sym.ID, nil
	}
	matches, err := store.GetSymbolsByName(projectID, symbol, 50)
	if err != nil {
		return "", fmt.Errorf("search %q: %w", symbol, err)
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("no symbol named %q in this project — pass a full id (file::qn#Kind) if the name is ambiguous", symbol)
	}
	// Prefer a callable, non-test symbol — that's what a call-flow is for.
	for _, want := range []bool{false, true} {
		for _, m := range matches {
			if m.IsTest != want {
				continue
			}
			if m.Kind == "Function" || m.Kind == "Method" {
				return m.ID, nil
			}
		}
	}
	return matches[0].ID, nil
}

// callflowEdge is a directed (caller → callee) pair.
type callflowEdge struct{ from, to string }

// collectCallflow runs a bounded BFS over CALLS edges from the seed and
// returns the reachable node-id set, the deduped edge set, and whether
// the node cap truncated the walk.
func collectCallflow(store *db.Store, projectID, seedID, direction string, depth int) (map[string]bool, []callflowEdge, bool) {
	nodes := map[string]bool{seedID: true}
	edgeSet := map[callflowEdge]bool{}
	truncated := false

	walkCallees := direction == "callees" || direction == "both"
	walkCallers := direction == "callers" || direction == "both"

	frontier := []string{seedID}
	for hop := 0; hop < depth && len(frontier) > 0; hop++ {
		var next []string
		for _, id := range frontier {
			if walkCallees {
				out, err := store.EdgesFromScoped(projectID, id, []string{"CALLS"})
				if err == nil {
					for _, e := range out {
						edgeSet[callflowEdge{e.FromID, e.ToID}] = true
						if !nodes[e.ToID] {
							if len(nodes) >= callflowNodeCap {
								truncated = true
								continue
							}
							nodes[e.ToID] = true
							next = append(next, e.ToID)
						}
					}
				}
			}
			if walkCallers {
				in, err := store.EdgesToScoped(projectID, id, []string{"CALLS"})
				if err == nil {
					for _, e := range in {
						edgeSet[callflowEdge{e.FromID, e.ToID}] = true
						if !nodes[e.FromID] {
							if len(nodes) >= callflowNodeCap {
								truncated = true
								continue
							}
							nodes[e.FromID] = true
							next = append(next, e.FromID)
						}
					}
				}
			}
		}
		frontier = next
	}

	// Keep only edges whose endpoints both survived the node cap.
	edges := make([]callflowEdge, 0, len(edgeSet))
	for e := range edgeSet {
		if nodes[e.from] && nodes[e.to] {
			edges = append(edges, e)
		}
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].from != edges[j].from {
			return edges[i].from < edges[j].from
		}
		return edges[i].to < edges[j].to
	})
	return nodes, edges, truncated
}

// shortNameFromID extracts a human label from a symbol id
// (`file::qualified.name#Kind` → `name`). Used as a fallback when the
// symbol's row isn't in the metadata batch (e.g. an external callee).
func shortNameFromID(id string) string {
	s := id
	if i := strings.Index(s, "::"); i >= 0 {
		s = s[i+2:]
	}
	if i := strings.LastIndex(s, "#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndex(s, "."); i >= 0 {
		s = s[i+1:]
	}
	if s == "" {
		return id
	}
	return s
}

// mermaidLabel escapes a string for use inside a Mermaid ["..."] label.
func mermaidLabel(s string) string {
	return strings.NewReplacer(`"`, "#quot;", "\n", " ").Replace(s)
}

// renderCallflowMermaid turns the node/edge sets into a Mermaid
// flowchart. The seed node is highlighted via a classDef.
func renderCallflowMermaid(store *db.Store, projectID, seedID string, nodes map[string]bool, edges []callflowEdge, truncated bool) (string, error) {
	ids := make([]string, 0, len(nodes))
	for id := range nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	meta, err := store.GetSymbolsByIDs(projectID, ids)
	if err != nil {
		return "", fmt.Errorf("load node metadata: %w", err)
	}

	// Stable id → Mermaid-safe handle (n0, n1, …).
	handle := make(map[string]string, len(ids))
	for i, id := range ids {
		handle[id] = fmt.Sprintf("n%d", i)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%%%% pincher callflow — %s\n", shortNameFromID(seedID))
	b.WriteString("flowchart LR\n")
	for _, id := range ids {
		label := shortNameFromID(id)
		if sym, ok := meta[id]; ok && sym != nil {
			label = sym.Name
			if sym.Kind == "Method" || sym.Kind == "Function" {
				label += "()"
			}
		}
		line := fmt.Sprintf("  %s[\"%s\"]", handle[id], mermaidLabel(label))
		if id == seedID {
			line += ":::seed"
		}
		b.WriteString(line + "\n")
	}
	for _, e := range edges {
		fmt.Fprintf(&b, "  %s --> %s\n", handle[e.from], handle[e.to])
	}
	b.WriteString("  classDef seed fill:#ffd24a,stroke:#333,stroke-width:2px;\n")
	if truncated {
		fmt.Fprintf(&b, "%%%% note: graph truncated at the %d-node cap — narrow --depth or --direction\n", callflowNodeCap)
	}
	return b.String(), nil
}
