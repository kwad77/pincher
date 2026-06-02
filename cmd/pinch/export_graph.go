package main

import (
	"encoding/json"
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// export_graph.go — `pincher export-graph` dumps a project's symbol +
// edge graph to a standard format (JSON, GraphML, DOT) so external
// graph tooling (Gephi, Cytoscape, Graphviz, Neo4j importers) can
// consume pincher's knowledge graph. Read-only; no schema change.
//
// pincher's graph is deterministic and AST-derived — exporting it is a
// pure SELECT over symbols + edges, no recomputation.

// exportSymbol is the per-node record in the JSON export.
type exportSymbol struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	QualifiedName string  `json:"qualified_name"`
	Kind          string  `json:"kind"`
	Language      string  `json:"language"`
	FilePath      string  `json:"file_path"`
	StartLine     int     `json:"start_line"`
	EndLine       int     `json:"end_line"`
	Signature     string  `json:"signature,omitempty"`
	IsExported    bool    `json:"is_exported"`
	IsTest        bool    `json:"is_test"`
	IsEntryPoint  bool    `json:"is_entry_point"`
	Complexity    int     `json:"complexity"`
	Confidence    float64 `json:"extraction_confidence"`
}

// exportEdge is the per-edge record in the JSON export.
type exportEdge struct {
	From       string  `json:"from"`
	To         string  `json:"to"`
	Kind       string  `json:"kind"`
	Source     string  `json:"source,omitempty"`
	Confidence float64 `json:"confidence"`
}

// exportGraph is the top-level JSON envelope.
type exportGraph struct {
	Project     map[string]string `json:"project"`
	GeneratedAt string            `json:"generated_at"`
	Symbols     []exportSymbol    `json:"symbols"`
	Edges       []exportEdge      `json:"edges"`
}

func runExportGraphCLI(args []string) {
	os.Exit(exportGraphCLI(args, os.Stdout, os.Stderr))
}

// exportGraphCLI is the testable core of runExportGraphCLI: it writes
// to the supplied streams and returns the process exit code instead of
// calling os.Exit, so every branch is unit-testable.
func exportGraphCLI(args []string, stdout, stderr io.Writer) int {
	log.SetOutput(io.Discard)

	fs := flag.NewFlagSet("export-graph", flag.ContinueOnError)
	fs.SetOutput(stderr)
	format := fs.String("format", "json", "Output format: json | graphml | dot")
	projectFlag := fs.String("project", "", "Project name, id, or substring (default: the current directory's project)")
	outPath := fs.String("out", "", "Write to this file (default: stdout)")
	dataDir := fs.String("data-dir", "", "Override data directory")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: pincher export-graph [--format=json|graphml|dot] [--project NAME|ID|SUBSTR] [--out=FILE]")
		fmt.Fprintln(stderr, "  Dumps a project's symbol + edge graph for external graph tooling.")
		fmt.Fprintln(stderr, "    json     — full record, every field (default)")
		fmt.Fprintln(stderr, "    graphml  — GraphML XML (Gephi, Cytoscape, yEd)")
		fmt.Fprintln(stderr, "    dot      — Graphviz DOT")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}

	formatVal := strings.ToLower(*format)
	switch formatVal {
	case "json", "graphml", "dot":
	default:
		fmt.Fprintf(stderr, "pincher export-graph: unknown format %q (want json, graphml, or dot)\n", *format)
		return 1
	}

	store, _, err := openProjectStore(*dataDir)
	if err != nil {
		fmt.Fprintf(stderr, "pincher export-graph: %v\n", err)
		return 1
	}
	defer store.Close()

	project, err := resolveExportProject(store, *projectFlag)
	if err != nil {
		fmt.Fprintf(stderr, "pincher export-graph: %v\n", err)
		return 1
	}

	symbols, err := store.ListSymbolsForProject(project.ID)
	if err != nil {
		fmt.Fprintf(stderr, "pincher export-graph: load symbols: %v\n", err)
		return 1
	}
	edges, err := store.ListEdgesForProject(project.ID)
	if err != nil {
		fmt.Fprintf(stderr, "pincher export-graph: load edges: %v\n", err)
		return 1
	}

	out := stdout
	if *outPath != "" {
		f, err := os.Create(*outPath)
		if err != nil {
			fmt.Fprintf(stderr, "pincher export-graph: create %s: %v\n", *outPath, err)
			return 1
		}
		defer f.Close()
		out = f
	}

	if err := writeGraph(out, formatVal, project, symbols, edges); err != nil {
		fmt.Fprintf(stderr, "pincher export-graph: write: %v\n", err)
		return 1
	}
	if *outPath != "" {
		fmt.Fprintf(stderr, "exported %d symbols + %d edges to %s (%s)\n",
			len(symbols), len(edges), *outPath, formatVal)
	}
	return 0
}

// resolveExportProject picks the project to export. An empty flag, or ".",
// means the current directory's project. A non-empty flag uses the same
// tiered resolver as `pincher project rm`: exact id, exact name, then
// substring on name/path with ambiguity surfaced instead of guessed.
func resolveExportProject(store *db.Store, flagVal string) (db.Project, error) {
	if flagVal == "" || filepath.Clean(strings.TrimSpace(flagVal)) == "." {
		cwd, err := os.Getwd()
		if err != nil {
			return db.Project{}, fmt.Errorf("cwd: %w", err)
		}
		id := db.ProjectIDFromPath(cwd)
		p, err := store.GetProject(id)
		if err != nil {
			return db.Project{}, fmt.Errorf("lookup project for %s: %w", cwd, err)
		}
		if p == nil {
			return db.Project{}, fmt.Errorf("no indexed project for the current directory — run `pincher index .` first, or pass --project")
		}
		return *p, nil
	}
	projects, err := store.ListProjects()
	if err != nil {
		return db.Project{}, fmt.Errorf("list projects: %w", err)
	}
	matches, status := matchProject(projects, flagVal)
	switch status {
	case matchExact:
		return matches[0], nil
	case matchAmbiguous:
		labels := make([]string, 0, len(matches))
		for _, p := range matches {
			labels = append(labels, fmt.Sprintf("%s (id=%s)", p.Name, p.ID))
		}
		sort.Strings(labels)
		return db.Project{}, fmt.Errorf("%q is ambiguous, matches %d projects: %s",
			flagVal, len(matches), strings.Join(labels, "; "))
	}
	names := make([]string, 0, len(projects))
	for _, p := range projects {
		names = append(names, p.Name)
	}
	sort.Strings(names)
	return db.Project{}, fmt.Errorf("no project matches %q. Indexed projects: %s",
		flagVal, strings.Join(names, ", "))
}

// writeGraph dispatches to the per-format encoder.
func writeGraph(w io.Writer, format string, project db.Project, symbols []db.Symbol, edges []db.Edge) error {
	switch format {
	case "json":
		return writeGraphJSON(w, project, symbols, edges)
	case "graphml":
		return writeGraphML(w, project, symbols, edges)
	case "dot":
		return writeGraphDOT(w, project, symbols, edges)
	}
	return fmt.Errorf("unhandled format %q", format)
}

func writeGraphJSON(w io.Writer, project db.Project, symbols []db.Symbol, edges []db.Edge) error {
	g := exportGraph{
		Project: map[string]string{
			"id": project.ID, "name": project.Name, "path": project.Path,
		},
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Symbols:     make([]exportSymbol, 0, len(symbols)),
		Edges:       make([]exportEdge, 0, len(edges)),
	}
	for _, s := range symbols {
		g.Symbols = append(g.Symbols, exportSymbol{
			ID: s.ID, Name: s.Name, QualifiedName: s.QualifiedName, Kind: s.Kind,
			Language: s.Language, FilePath: s.FilePath, StartLine: s.StartLine,
			EndLine: s.EndLine, Signature: s.Signature, IsExported: s.IsExported,
			IsTest: s.IsTest, IsEntryPoint: s.IsEntryPoint, Complexity: s.Complexity,
			Confidence: s.ExtractionConfidence,
		})
	}
	for _, e := range edges {
		g.Edges = append(g.Edges, exportEdge{
			From: e.FromID, To: e.ToID, Kind: e.Kind, Source: e.Source, Confidence: e.Confidence,
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(g)
}

// dropDanglingEdges returns only edges whose endpoints both exist in the
// symbol set. GraphML and DOT require every edge endpoint to be a
// declared node; pincher's edge set can reference external/unresolved
// symbols. Returns the kept edges and the dropped count.
func dropDanglingEdges(symbols []db.Symbol, edges []db.Edge) ([]db.Edge, int) {
	ids := make(map[string]bool, len(symbols))
	for _, s := range symbols {
		ids[s.ID] = true
	}
	kept := make([]db.Edge, 0, len(edges))
	dropped := 0
	for _, e := range edges {
		if ids[e.FromID] && ids[e.ToID] {
			kept = append(kept, e)
		} else {
			dropped++
		}
	}
	return kept, dropped
}

// ── GraphML ─────────────────────────────────────────────────────────

type gmlData struct {
	Key   string `xml:"key,attr"`
	Value string `xml:",chardata"`
}
type gmlNode struct {
	ID   string    `xml:"id,attr"`
	Data []gmlData `xml:"data"`
}
type gmlEdge struct {
	Source string    `xml:"source,attr"`
	Target string    `xml:"target,attr"`
	Data   []gmlData `xml:"data"`
}
type gmlKey struct {
	ID       string `xml:"id,attr"`
	For      string `xml:"for,attr"`
	AttrName string `xml:"attr.name,attr"`
	AttrType string `xml:"attr.type,attr"`
}
type gmlGraph struct {
	ID          string    `xml:"id,attr"`
	EdgeDefault string    `xml:"edgedefault,attr"`
	Nodes       []gmlNode `xml:"node"`
	Edges       []gmlEdge `xml:"edge"`
}
type graphMLDoc struct {
	XMLName xml.Name `xml:"graphml"`
	Xmlns   string   `xml:"xmlns,attr"`
	Keys    []gmlKey `xml:"key"`
	Graph   gmlGraph `xml:"graph"`
}

func writeGraphML(w io.Writer, project db.Project, symbols []db.Symbol, edges []db.Edge) error {
	kept, _ := dropDanglingEdges(symbols, edges)
	doc := graphMLDoc{
		Xmlns: "http://graphml.graphdrawing.org/xmlns",
		Keys: []gmlKey{
			{"name", "node", "name", "string"},
			{"kind", "node", "kind", "string"},
			{"language", "node", "language", "string"},
			{"file", "node", "file_path", "string"},
			{"edgeKind", "edge", "kind", "string"},
		},
		Graph: gmlGraph{ID: project.Name, EdgeDefault: "directed"},
	}
	for _, s := range symbols {
		doc.Graph.Nodes = append(doc.Graph.Nodes, gmlNode{
			ID: s.ID,
			Data: []gmlData{
				{"name", s.Name}, {"kind", s.Kind},
				{"language", s.Language}, {"file", s.FilePath},
			},
		})
	}
	for _, e := range kept {
		doc.Graph.Edges = append(doc.Graph.Edges, gmlEdge{
			Source: e.FromID, Target: e.ToID,
			Data: []gmlData{{"edgeKind", e.Kind}},
		})
	}
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\n")
	return err
}

// ── DOT ─────────────────────────────────────────────────────────────

// dotQuote wraps s in double quotes, escaping the characters DOT treats
// specially inside a quoted string.
func dotQuote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return `"` + r.Replace(s) + `"`
}

func writeGraphDOT(w io.Writer, project db.Project, symbols []db.Symbol, edges []db.Edge) error {
	kept, _ := dropDanglingEdges(symbols, edges)
	var b strings.Builder
	fmt.Fprintf(&b, "// pincher export-graph — project %q\n", project.Name)
	b.WriteString("digraph pincher {\n  rankdir=LR;\n  node [shape=box];\n")
	for _, s := range symbols {
		label := s.Name + "\n" + s.Kind
		fmt.Fprintf(&b, "  %s [label=%s];\n", dotQuote(s.ID), dotQuote(label))
	}
	for _, e := range kept {
		fmt.Fprintf(&b, "  %s -> %s [label=%s];\n",
			dotQuote(e.FromID), dotQuote(e.ToID), dotQuote(e.Kind))
	}
	b.WriteString("}\n")
	_, err := io.WriteString(w, b.String())
	return err
}
