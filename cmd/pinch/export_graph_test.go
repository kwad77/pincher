package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
	"github.com/kwad77/pincher/internal/index"
)

// export-graph fixture: two symbols, one real edge, one dangling edge
// (its target is not in the symbol set).
func exportFixture() ([]db.Symbol, []db.Edge) {
	syms := []db.Symbol{
		{ID: "a.go::pkg.Alpha#Function", Name: "Alpha", QualifiedName: "pkg.Alpha",
			Kind: "Function", Language: "Go", FilePath: "a.go", StartLine: 1, EndLine: 5,
			IsExported: true, ExtractionConfidence: 1.0},
		{ID: "a.go::pkg.beta#Function", Name: "beta", QualifiedName: "pkg.beta",
			Kind: "Function", Language: "Go", FilePath: "a.go", StartLine: 7, EndLine: 9,
			ExtractionConfidence: 1.0},
	}
	edges := []db.Edge{
		{FromID: "a.go::pkg.Alpha#Function", ToID: "a.go::pkg.beta#Function",
			Kind: "CALLS", Source: "resolve_pass", Confidence: 1.0},
		{FromID: "a.go::pkg.Alpha#Function", ToID: "external::fmt.Println#Function",
			Kind: "CALLS", Source: "per_file", Confidence: 0.8},
	}
	return syms, edges
}

func TestWriteGraphJSON(t *testing.T) {
	syms, edges := exportFixture()
	var b strings.Builder
	if err := writeGraphJSON(&b, db.Project{ID: "p1", Name: "demo", Path: "/p"}, syms, edges); err != nil {
		t.Fatalf("writeGraphJSON: %v", err)
	}
	var g exportGraph
	if err := json.Unmarshal([]byte(b.String()), &g); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, b.String())
	}
	if g.Project["name"] != "demo" {
		t.Errorf("project.name = %q, want demo", g.Project["name"])
	}
	if len(g.Symbols) != 2 {
		t.Errorf("symbols = %d, want 2", len(g.Symbols))
	}
	// JSON keeps every edge, including the dangling one — it's a raw dump.
	if len(g.Edges) != 2 {
		t.Errorf("edges = %d, want 2 (JSON keeps dangling edges)", len(g.Edges))
	}
	if g.GeneratedAt == "" {
		t.Error("generated_at not stamped")
	}
}

func TestWriteGraphML(t *testing.T) {
	syms, edges := exportFixture()
	var b strings.Builder
	if err := writeGraphML(&b, db.Project{Name: "demo"}, syms, edges); err != nil {
		t.Fatalf("writeGraphML: %v", err)
	}
	var doc graphMLDoc
	if err := xml.Unmarshal([]byte(b.String()), &doc); err != nil {
		t.Fatalf("output is not valid XML: %v\n%s", err, b.String())
	}
	if len(doc.Graph.Nodes) != 2 {
		t.Errorf("graphml nodes = %d, want 2", len(doc.Graph.Nodes))
	}
	// GraphML drops the dangling edge — every endpoint must be a node.
	if len(doc.Graph.Edges) != 1 {
		t.Errorf("graphml edges = %d, want 1 (dangling edge dropped)", len(doc.Graph.Edges))
	}
}

func TestWriteGraphDOT(t *testing.T) {
	syms, edges := exportFixture()
	var b strings.Builder
	if err := writeGraphDOT(&b, db.Project{Name: "demo"}, syms, edges); err != nil {
		t.Fatalf("writeGraphDOT: %v", err)
	}
	out := b.String()
	if !strings.Contains(out, "digraph pincher {") {
		t.Errorf("DOT output missing digraph header:\n%s", out)
	}
	if !strings.Contains(out, "->") {
		t.Errorf("DOT output has no edges:\n%s", out)
	}
	// The dangling edge's external target must not appear as an edge.
	if strings.Contains(out, "external::fmt.Println") {
		t.Errorf("DOT output should drop the dangling edge:\n%s", out)
	}
}

func TestDropDanglingEdges(t *testing.T) {
	syms, edges := exportFixture()
	kept, dropped := dropDanglingEdges(syms, edges)
	if len(kept) != 1 {
		t.Errorf("kept = %d, want 1", len(kept))
	}
	if dropped != 1 {
		t.Errorf("dropped = %d, want 1", dropped)
	}
}

func TestDotQuote(t *testing.T) {
	cases := []struct{ in, want string }{
		{`plain`, `"plain"`},
		{`has"quote`, `"has\"quote"`},
		{`back\slash`, `"back\\slash"`},
		{"two\nlines", `"two\nlines"`},
	}
	for _, c := range cases {
		if got := dotQuote(c.in); got != c.want {
			t.Errorf("dotQuote(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestExportGraphCLI_EndToEnd indexes a project into a temp data dir,
// then drives exportGraphCLI through the real flag/open/resolve/write
// path and checks the exit code + emitted output.
func TestExportGraphCLI_EndToEnd(t *testing.T) {
	dataDir := t.TempDir()
	projDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(projDir, "m.go"),
		[]byte("package m\n\nfunc Helper() int { return 1 }\n\nfunc Use() int { return Helper() }\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	store, err := db.Open(dataDir)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	res, err := index.New(store).Index(context.Background(), projDir, false)
	if err != nil {
		store.Close()
		t.Fatalf("index: %v", err)
	}
	project, _ := store.GetProject(res.ProjectID)
	store.Close() // exportGraphCLI re-opens the same data dir

	t.Run("json to stdout", func(t *testing.T) {
		var out, errb strings.Builder
		code := exportGraphCLI([]string{"--data-dir", dataDir, "--project", project.Name}, &out, &errb)
		if code != 0 {
			t.Fatalf("exit = %d, want 0; stderr=%s", code, errb.String())
		}
		var g exportGraph
		if err := json.Unmarshal([]byte(out.String()), &g); err != nil {
			t.Fatalf("stdout is not valid JSON: %v", err)
		}
		if len(g.Symbols) == 0 {
			t.Error("expected symbols in the export")
		}
	})

	t.Run("graphml to file", func(t *testing.T) {
		var out, errb strings.Builder
		dest := filepath.Join(t.TempDir(), "g.graphml")
		code := exportGraphCLI([]string{"--data-dir", dataDir, "--project", project.Name,
			"--format", "graphml", "--out", dest}, &out, &errb)
		if code != 0 {
			t.Fatalf("exit = %d, want 0; stderr=%s", code, errb.String())
		}
		blob, err := os.ReadFile(dest)
		if err != nil {
			t.Fatalf("read export file: %v", err)
		}
		if !strings.Contains(string(blob), "<graphml") {
			t.Errorf("export file is not GraphML:\n%s", blob)
		}
		if !strings.Contains(errb.String(), "exported") {
			t.Errorf("stderr should print a receipt; got %s", errb.String())
		}
	})

	t.Run("unknown format exits 1", func(t *testing.T) {
		var out, errb strings.Builder
		if code := exportGraphCLI([]string{"--data-dir", dataDir, "--format", "yaml"}, &out, &errb); code != 1 {
			t.Errorf("exit = %d, want 1", code)
		}
		if !strings.Contains(errb.String(), "unknown format") {
			t.Errorf("stderr should name the bad format; got %s", errb.String())
		}
	})

	t.Run("unknown project exits 1", func(t *testing.T) {
		var out, errb strings.Builder
		if code := exportGraphCLI([]string{"--data-dir", dataDir, "--project", "no-such-proj"}, &out, &errb); code != 1 {
			t.Errorf("exit = %d, want 1", code)
		}
	})
}

func TestResolveExportProject(t *testing.T) {
	store, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer store.Close()
	if err := store.UpsertProject(db.Project{
		ID: "p-export", Path: "/tmp/p-export", Name: "exporter", IndexedAt: time.Now(),
	}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}

	// Match by exact name.
	if p, err := resolveExportProject(store, "exporter"); err != nil || p.ID != "p-export" {
		t.Errorf("resolve by name: got %+v err=%v", p, err)
	}
	// Match by id.
	if p, err := resolveExportProject(store, "p-export"); err != nil || p.Name != "exporter" {
		t.Errorf("resolve by id: got %+v err=%v", p, err)
	}
	// Case-insensitive name.
	if p, err := resolveExportProject(store, "EXPORTER"); err != nil || p.ID != "p-export" {
		t.Errorf("resolve case-insensitive: got %+v err=%v", p, err)
	}
	// No match — error names the available projects.
	if _, err := resolveExportProject(store, "nonexistent"); err == nil {
		t.Error("expected error for unknown project")
	} else if !strings.Contains(err.Error(), "exporter") {
		t.Errorf("error should list available projects; got: %v", err)
	}
}
