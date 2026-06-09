// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
	"github.com/kwad77/pincher/internal/index"
)

func reportFixture() ([]db.Symbol, []db.Edge) {
	syms := []db.Symbol{
		{ID: "cmd/app.go::main.main#Function", Name: "main", QualifiedName: "main.main",
			Kind: "Function", Language: "Go", FilePath: "cmd/app.go", StartLine: 10, EndLine: 20,
			IsEntryPoint: true, ExtractionConfidence: 1.0},
		{ID: "internal/server.go::server.Handle#Function", Name: "Handle", QualifiedName: "server.Handle",
			Kind: "Function", Language: "Go", FilePath: "internal/server.go", StartLine: 30, EndLine: 60,
			ExtractionConfidence: 1.0},
		{ID: "internal/server.go::server.rationale.L34#Rationale", Name: "WHY: preserve provenance for routing evidence",
			Kind: "Rationale", Language: "Go", FilePath: "internal/server.go", StartLine: 34, EndLine: 34,
			Parent: "server.Handle", ExtractionConfidence: 1.0},
		{ID: "internal/server.go::server.rationale.L6#Rationale", Name: "NOTE: file-level rationale stays explicit",
			Kind: "Rationale", Language: "Go", FilePath: "internal/server.go", StartLine: 6, EndLine: 6,
			ExtractionConfidence: 0.75},
		{ID: "README.md::overview#Section", Name: "Overview", QualifiedName: "Overview",
			Kind: "Section", Language: "Markdown", FilePath: "README.md", StartLine: 1, EndLine: 12,
			ExtractionConfidence: 0.9},
	}
	edges := []db.Edge{
		{FromID: "cmd/app.go::main.main#Function", ToID: "internal/server.go::server.Handle#Function", Kind: "CALLS", Source: "resolve_pass", Confidence: 1.0},
		{FromID: "cmd/app.go::main.main#Function", ToID: "README.md::overview#Section", Kind: "REFERENCES", Source: "docs", Confidence: 0.8},
	}
	return syms, edges
}

func TestWriteProjectReportMarkdown(t *testing.T) {
	syms, edges := reportFixture()
	project := db.Project{
		ID: "p-demo", Name: "demo", Path: "/tmp/demo", IndexedAt: time.Unix(1700000000, 0),
		FileCount: 3, SymCount: len(syms), EdgeCount: len(edges), BinaryVersion: "test-version",
	}
	var b strings.Builder
	if err := writeProjectReportMarkdown(&b, project, syms, edges, reportOptions{GeneratedAt: time.Unix(1700000100, 0).UTC()}); err != nil {
		t.Fatalf("writeProjectReportMarkdown: %v", err)
	}
	out := b.String()
	mustContain := []string{
		"# Pincher report: demo",
		"Generated: 2023-11-14T22:15:00Z",
		"Indexed: 2023-11-14T22:13:20Z",
		"Binary version: `test-version`",
		"Files: 3 · Symbols: 5 · Edges: 2",
		"## Languages",
		"- Go: 4 symbols",
		"- Markdown: 1 symbol",
		"## Advanced graph export",
		"Escape hatch: run `pincher export-graph --project \"p-demo\" --format json` for deterministic node/edge JSON that round-trips against the indexed DB counts.",
		"Args: `{\"project\":\"p-demo\",\"id\":\"cmd/pinch/export_graph.go::main.writeGraphJSON#Function\"}`",
		"Why: inspect the export-graph JSON writer before building advanced external graph analysis.",
		"## Entry points",
		"- `main` — `cmd/app.go:10`",
		"## Hotspots",
		"- `Handle` Function — `internal/server.go` (incoming calls: 1)",
		"## Rationale / design intent",
		"- Attached rationale: 1 · unattached/file-level: 1",
		"- Attachment: `server.Handle` (1 rationale)",
		"  - `WHY: preserve provenance for routing evidence` — `internal/server.go:34` (confidence: 1.00)",
		"- Attachment: `unattached/file-level` (1 rationale)",
		"  - `NOTE: file-level rationale stays explicit` — `internal/server.go:6` (confidence: 0.75)",
		"## Surprising connections",
		"- `cmd` → `internal`: 1 edge",
		"## Suggested next Pincher calls",
		"- Tool: `mcp_pincher_context`",
		"Args: `{\"project\":\"p-demo\",\"id\":\"internal/server.go::server.Handle#Function\"}`",
		"Why: inspect the top hotspot before editing it.",
		"Expected value: reduces risky raw reads and grounds edits in symbol provenance.",
		"- Tool: `mcp_pincher_trace`",
		"Args: `{\"project\":\"p-demo\",\"id\":\"internal/server.go::server.Handle#Function\",\"direction\":\"inbound\"}`",
		"Why: map callers for the highest-incoming hotspot before behavior changes.",
		"Expected value: exposes blast-radius risk for planning and routing escalation.",
		"- Tool: `mcp_pincher_search`",
		"Args: `{\"project\":\"p-demo\",\"query\":\"WHY: preserve provenance for routing evidence\"}`",
		"Why: follow rationale/design-intent evidence back into indexed symbols.",
		"Expected value: keeps design intent visible instead of relying on prose-only memory.",
		"- Tool: `mcp_pincher_changes`",
		"Args: `{\"project\":\"p-demo\",\"scope\":\"all\"}`",
		"Why: run before finalizing edits to map changed-symbol blast radius.",
	}
	for _, want := range mustContain {
		if !strings.Contains(out, want) {
			t.Fatalf("report missing %q\n--- report ---\n%s", want, out)
		}
	}
	if strings.Contains(strings.ToLower(out), "graphify") {
		t.Fatalf("report should preserve Pincher-native positioning and not mention Graphify:\n%s", out)
	}
}

func TestWriteProjectReportJSON_HasStructuredNextCallsAndLegacyArgs(t *testing.T) {
	syms, edges := reportFixture()
	project := db.Project{
		ID: "p-demo", Name: "demo", Path: "/tmp/demo", IndexedAt: time.Unix(1700000000, 0),
		FileCount: 3, SymCount: len(syms), EdgeCount: len(edges), BinaryVersion: "test-version",
	}
	var b strings.Builder
	if err := writeProjectReportJSON(&b, project, syms, edges, reportOptions{GeneratedAt: time.Unix(1700000100, 0).UTC()}); err != nil {
		t.Fatalf("writeProjectReportJSON: %v", err)
	}
	var payload struct {
		Format      string `json:"format"`
		GeneratedAt string `json:"generated_at"`
		Project     struct {
			ID            string `json:"id"`
			BinaryVersion string `json:"binary_version"`
			Symbols       int    `json:"symbols"`
			Edges         int    `json:"edges"`
		} `json:"project"`
		NextPincherCalls []struct {
			Tool          string         `json:"tool"`
			ArgsLegacy    string         `json:"args_legacy"`
			Args          map[string]any `json:"args"`
			Why           string         `json:"why"`
			ExpectedValue string         `json:"expected_value"`
		} `json:"next_pincher_calls"`
	}
	if err := json.Unmarshal([]byte(b.String()), &payload); err != nil {
		t.Fatalf("json unmarshal: %v\n%s", err, b.String())
	}
	if payload.Format != "pincher_report.v1" || payload.GeneratedAt != "2023-11-14T22:15:00Z" {
		t.Fatalf("unexpected report identity: %#v", payload)
	}
	if payload.Project.ID != "p-demo" || payload.Project.BinaryVersion != "test-version" || payload.Project.Symbols != 5 || payload.Project.Edges != 2 {
		t.Fatalf("project fields changed or missing: %#v", payload.Project)
	}
	if len(payload.NextPincherCalls) != 4 {
		t.Fatalf("next call count = %d, want 4; payload=%s", len(payload.NextPincherCalls), b.String())
	}
	first := payload.NextPincherCalls[0]
	if first.Tool != "mcp_pincher_context" || first.Args["project"] != "p-demo" || first.Args["id"] != "internal/server.go::server.Handle#Function" {
		t.Fatalf("first next call is not machine-actionable context guidance: %#v", first)
	}
	if first.ArgsLegacy != `{"project":"p-demo","id":"internal/server.go::server.Handle#Function"}` {
		t.Fatalf("legacy args string changed: %q", first.ArgsLegacy)
	}
	if payload.NextPincherCalls[1].Args["direction"] != "inbound" {
		t.Fatalf("trace direction missing from structured args: %#v", payload.NextPincherCalls[1])
	}
	if payload.NextPincherCalls[3].Args["scope"] != "all" {
		t.Fatalf("changes scope missing from structured args: %#v", payload.NextPincherCalls[3])
	}
}

func TestReportCLI_EndToEnd(t *testing.T) {
	dataDir := t.TempDir()
	projDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(projDir, "m.go"), []byte("package main\n\n// WHY: CLI fixtures preserve report provenance.\nfunc main() { Helper() }\n\nfunc Helper() {}\n"), 0o644); err != nil {
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
	store.Close()

	var out, errb strings.Builder
	code := reportCLI([]string{"--data-dir", dataDir, "--project", project.Name}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "# Pincher report: "+project.Name) {
		t.Fatalf("stdout missing report title:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "## Suggested next Pincher calls") {
		t.Fatalf("stdout missing next-call guidance:\n%s", out.String())
	}

	out.Reset()
	errb.Reset()
	code = reportCLI([]string{"--data-dir", dataDir, "--project", project.Name, "--format", "json"}, &out, &errb)
	if code != 0 {
		t.Fatalf("json exit = %d, want 0; stderr=%s", code, errb.String())
	}
	var payload struct {
		Format  string `json:"format"`
		Project struct {
			Name string `json:"name"`
		} `json:"project"`
		NextPincherCalls []struct {
			Tool string         `json:"tool"`
			Args map[string]any `json:"args"`
		} `json:"next_pincher_calls"`
	}
	if err := json.Unmarshal([]byte(out.String()), &payload); err != nil {
		t.Fatalf("json report is not parseable: %v\n%s", err, out.String())
	}
	if payload.Format != "pincher_report.v1" || payload.Project.Name != project.Name {
		t.Fatalf("json report identity mismatch: %#v", payload)
	}
	if len(payload.NextPincherCalls) == 0 || payload.NextPincherCalls[len(payload.NextPincherCalls)-1].Args["scope"] != "all" {
		t.Fatalf("json report missing structured next-call guidance: %#v", payload.NextPincherCalls)
	}
}
