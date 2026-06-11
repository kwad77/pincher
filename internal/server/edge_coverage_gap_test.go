// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// #858: trace and dead_code are silent no-ops on non-Go/non-Python
// corpora — those languages extract symbols fine but
// resolveImports/Calls/Reads doesn't cover them, so the edge graph is
// empty. An empty trace / dead_code result then reads like "no callers"
// / "no dead code" when it actually means "this language has no edge
// graph." edgeCoverageGap turns that into an explicit `_meta.diagnosis`.
func TestEdgeCoverageGap_NonGoProject_DiagnosesOnEmptyResult(t *testing.T) {
	srv, store, _ := newTestServer(t)
	srv.sessionID = "cproj"
	store.UpsertProject(db.Project{ID: "cproj", Path: "/tmp/cproj", Name: "cproj", IndexedAt: time.Now()})

	// A C project: symbols extract (regex tier, confidence ~0.70) but
	// zero edges — the exact #858 shape.
	mustUpsertSymbols(t, store, []db.Symbol{
		{ID: "cproj::helper#Function", ProjectID: "cproj", FilePath: "src/main.c",
			Name: "helper", QualifiedName: "helper", Kind: "Function", Language: "C",
			ExtractionConfidence: 0.70},
		{ID: "cproj::run#Function", ProjectID: "cproj", FilePath: "src/main.c",
			Name: "run", QualifiedName: "run", Kind: "Function", Language: "C",
			ExtractionConfidence: 0.70},
	})

	// dead_code: empty result must carry the coverage-gap diagnosis, not
	// the misleading "lower min_confidence" advice.
	dcRes, err := srv.handleDeadCode(context.Background(), makeReq(map[string]any{}))
	if err != nil {
		t.Fatalf("handleDeadCode: %v", err)
	}
	dcDiag := metaDiagnosis(t, dcRes)
	if !strings.Contains(dcDiag, "#858") || !strings.Contains(dcDiag, "predominantly C") {
		t.Errorf("dead_code diagnosis on C project should name the #858 coverage gap; got %q", dcDiag)
	}
	if strings.Contains(dcDiag, "min_confidence") {
		t.Errorf("dead_code on a zero-edge language must not advise lowering min_confidence; got %q", dcDiag)
	}

	// trace: empty trace on a C symbol must carry the same diagnosis.
	trRes, err := srv.handleTrace(context.Background(), makeReq(map[string]any{"name": "helper"}))
	if err != nil {
		t.Fatalf("handleTrace: %v", err)
	}
	trDiag := metaDiagnosis(t, trRes)
	if !strings.Contains(trDiag, "#858") || !strings.Contains(trDiag, "predominantly C") {
		t.Errorf("trace diagnosis on C project should name the #858 coverage gap; got %q", trDiag)
	}
}

// Control: on a Go project, an empty trace is a genuine leaf result —
// Go HAS edge resolution, so edgeCoverageGap must stay silent and not
// mislabel a real leaf as a coverage gap.
func TestEdgeCoverageGap_GoProject_SilentOnGenuineLeaf(t *testing.T) {
	srv, store, _ := newTestServer(t)
	srv.sessionID = "goproj"
	store.UpsertProject(db.Project{ID: "goproj", Path: "/tmp/goproj", Name: "goproj", IndexedAt: time.Now()})

	mustUpsertSymbols(t, store, []db.Symbol{
		{ID: "goproj::pkg.caller#Function", ProjectID: "goproj", FilePath: "svc.go",
			Name: "caller", QualifiedName: "pkg.caller", Kind: "Function", Language: "Go",
			ExtractionConfidence: 1.0},
		{ID: "goproj::pkg.leaf#Function", ProjectID: "goproj", FilePath: "svc.go",
			Name: "leaf", QualifiedName: "pkg.leaf", Kind: "Function", Language: "Go",
			ExtractionConfidence: 1.0},
	})
	// One real edge so the project's edge graph is non-empty.
	if err := store.BulkUpsertEdges([]db.Edge{
		{ProjectID: "goproj", FromID: "goproj::pkg.caller#Function", ToID: "goproj::pkg.leaf#Function",
			Kind: "CALLS", Confidence: 1},
	}); err != nil {
		t.Fatal(err)
	}

	// `leaf` has no outbound edges — an empty outbound trace. That's a
	// genuine leaf, not a coverage gap; diagnosis must be absent.
	trRes, err := srv.handleTrace(context.Background(), makeReq(map[string]any{"name": "leaf", "direction": "outbound"}))
	if err != nil {
		t.Fatalf("handleTrace: %v", err)
	}
	if diag := metaDiagnosis(t, trRes); strings.Contains(diag, "#858") {
		t.Errorf("Go project has edge resolution — an empty trace is a real leaf, not a coverage gap; got diagnosis %q", diag)
	}
}

func TestSparseGraphWarning_TypeScriptProject_WarnsTraceAndDeadCode(t *testing.T) {
	srv, store, _ := newTestServer(t)
	srv.sessionID = "tsproj"
	store.UpsertProject(db.Project{ID: "tsproj", Path: "/tmp/tsproj", Name: "tsproj", IndexedAt: time.Now()})

	syms := make([]db.Symbol, 0, 120)
	for i := 0; i < 120; i++ {
		name := fmt.Sprintf("fn%d", i)
		if i == 0 {
			name = "entry"
		}
		syms = append(syms, db.Symbol{ID: fmt.Sprintf("tsproj::%s#Function", name), ProjectID: "tsproj", FilePath: "src/app.ts",
			Name: name, QualifiedName: name, Kind: "Function", Language: "TypeScript", ExtractionConfidence: 0.95})
	}
	mustUpsertSymbols(t, store, syms)
	if err := store.BulkUpsertEdges([]db.Edge{{ProjectID: "tsproj", FromID: syms[0].ID, ToID: syms[1].ID, Kind: "CALLS", Confidence: 1}}); err != nil {
		t.Fatal(err)
	}

	dcRes, err := srv.handleDeadCode(context.Background(), makeReq(map[string]any{"language": "TypeScript", "min_confidence": 0.9}))
	if err != nil {
		t.Fatalf("handleDeadCode: %v", err)
	}
	if got := strings.Join(metaWarnings(t, decode(t, dcRes)), "\n"); !strings.Contains(got, "GRAPH SPARSITY WARNING") || !strings.Contains(got, "TypeScript") || !strings.Contains(got, "results are unreliable") {
		t.Fatalf("dead_code missing prominent TypeScript sparse-graph warning; got %q", got)
	}

	trRes, err := srv.handleTrace(context.Background(), makeReq(map[string]any{"name": "entry"}))
	if err != nil {
		t.Fatalf("handleTrace: %v", err)
	}
	if got := strings.Join(metaWarnings(t, decode(t, trRes)), "\n"); !strings.Contains(got, "GRAPH SPARSITY WARNING") || !strings.Contains(got, "TypeScript") || !strings.Contains(got, "trace/dead_code results are unreliable") {
		t.Fatalf("trace missing prominent TypeScript sparse-graph warning; got %q", got)
	}
}

// metaDiagnosis pulls _meta.diagnosis from a tool result, "" if absent.
func metaDiagnosis(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	body := decode(t, result)
	meta, _ := body["_meta"].(map[string]any)
	diag, _ := meta["diagnosis"].(string)
	return diag
}
