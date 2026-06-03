// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// Tests for the trace tool's `kinds` filter (follow-up to #264/#265).
// The filter scopes the BFS edge traversal to the given kinds; default
// behavior (no filter) preserves the original CALLS-family-only set.

// setupTraceKinds seeds a project with three symbols (Foo, Bar, Cache)
// and edges of multiple kinds:
//   Bar  -- CALLS  -->  Foo
//   Foo  -- READS  -->  Cache
// A trace inbound from Foo with default kinds returns Bar (via CALLS).
// A trace outbound from Foo with kinds=READS returns Cache (via READS),
// not anything else.
func setupTraceKinds(t *testing.T) (*Server, *db.Store, string) {
	t.Helper()
	srv, store, _ := newTestServer(t)
	pid := "trace-kinds"
	store.UpsertProject(db.Project{ID: pid, Path: "/tmp/" + pid, Name: pid, IndexedAt: time.Now()})
	srv.sessionID = pid
	srv.sessionRoot = "/tmp/" + pid

	mustUpsertSymbols(t, store, []db.Symbol{
		{ID: pid + "::pkg.Foo#Function", ProjectID: pid, FilePath: "main.go", Name: "Foo",
			QualifiedName: "pkg.Foo", Kind: "Function", Language: "Go", ExtractionConfidence: 1.0,
			StartByte: 0, EndByte: 50, StartLine: 1, EndLine: 5},
		{ID: pid + "::pkg.Bar#Function", ProjectID: pid, FilePath: "main.go", Name: "Bar",
			QualifiedName: "pkg.Bar", Kind: "Function", Language: "Go", ExtractionConfidence: 1.0,
			StartByte: 51, EndByte: 100, StartLine: 6, EndLine: 10},
		{ID: pid + "::pkg.Cache#Variable", ProjectID: pid, FilePath: "main.go", Name: "Cache",
			QualifiedName: "pkg.Cache", Kind: "Variable", Language: "Go", ExtractionConfidence: 1.0,
			StartByte: 101, EndByte: 130, StartLine: 11, EndLine: 11},
	})

	if err := store.BulkUpsertEdges([]db.Edge{
		{ProjectID: pid, FromID: pid + "::pkg.Bar#Function", ToID: pid + "::pkg.Foo#Function", Kind: "CALLS", Confidence: 1.0},
		{ProjectID: pid, FromID: pid + "::pkg.Foo#Function", ToID: pid + "::pkg.Cache#Variable", Kind: "READS", Confidence: 0.5},
	}); err != nil {
		t.Fatalf("BulkUpsertEdges: %v", err)
	}
	return srv, store, pid
}

// Default kinds traverse CALLS-family edges only — the existing
// behaviour. Trace inbound from Foo finds Bar; READS edges to Cache
// are NOT followed because outbound direction + Cache being the
// target of READS means it wouldn't show up regardless.
func TestHandleTrace_DefaultKindsTraversesCalls(t *testing.T) {
	t.Parallel()
	srv, _, _ := setupTraceKinds(t)
	result, err := srv.handleTrace(context.Background(), makeReq(map[string]any{
		"name":      "Foo",
		"direction": "inbound",
	}))
	if err != nil {
		t.Fatalf("handleTrace: %v", err)
	}
	body := decode(t, result)
	total, _ := body["total"].(float64)
	if total < 1 {
		t.Errorf("default trace inbound from Foo should find Bar via CALLS; got total=%v:\n%v", total, body)
	}
}

// Explicit kinds=READS traces only READS edges. Trace outbound from
// Foo finds Cache (via READS). Trace outbound with default (CALLS)
// would NOT find Cache because the edge isn't a CALLS.
func TestHandleTrace_KindsReadsTraversesDataFlow(t *testing.T) {
	t.Parallel()
	srv, _, _ := setupTraceKinds(t)
	result, err := srv.handleTrace(context.Background(), makeReq(map[string]any{
		"name":      "Foo",
		"direction": "outbound",
		"kinds":     "READS",
	}))
	if err != nil {
		t.Fatalf("handleTrace: %v", err)
	}
	body := decode(t, result)
	total, _ := body["total"].(float64)
	if total < 1 {
		t.Errorf("kinds=READS trace outbound from Foo should find Cache; got total=%v:\n%v", total, body)
	}
}

// kinds parsing tolerates whitespace, case differences, and trailing
// commas — agents pass varied formats and shouldn't fail on cosmetic
// differences.
func TestHandleTrace_KindsParsingTolerant(t *testing.T) {
	t.Parallel()
	srv, _, _ := setupTraceKinds(t)
	cases := []string{
		"READS",
		"reads",
		" READS ",
		"reads, writes",
		",READS,",
	}
	for _, k := range cases {
		t.Run(k, func(t *testing.T) {
			result, err := srv.handleTrace(context.Background(), makeReq(map[string]any{
				"name":      "Foo",
				"direction": "outbound",
				"kinds":     k,
			}))
			if err != nil {
				t.Fatalf("handleTrace: %v", err)
			}
			body := decode(t, result)
			total, _ := body["total"].(float64)
			if total < 1 {
				t.Errorf("kinds=%q should still match READS edge; got total=%v", k, total)
			}
		})
	}
}

// Default kinds (no `kinds` arg) explicitly does NOT traverse READS.
// Trace outbound from Foo finds nothing because the only outbound
// edge from Foo is a READS edge — back-compat gate against a future
// regression that adds READS to the default kinds list.
func TestHandleTrace_DefaultKindsExcludesReads(t *testing.T) {
	t.Parallel()
	srv, _, _ := setupTraceKinds(t)
	result, err := srv.handleTrace(context.Background(), makeReq(map[string]any{
		"name":      "Foo",
		"direction": "outbound",
	}))
	if err != nil {
		t.Fatalf("handleTrace: %v", err)
	}
	body := decode(t, result)
	total, _ := body["total"].(float64)
	if total != 0 {
		t.Errorf("default trace outbound from Foo should find 0 hops (only READS edge exists); got total=%v:\n%v", total, body)
	}
}

// Schema documents the kinds parameter. Pin so a future refactor
// can't drop the docs silently — without them, agents have to read
// source to discover the option.
func TestTraceToolSchema_DocumentsKindsFilter(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	tool, ok := srv.tools["trace"]
	if !ok {
		t.Fatal("trace tool not registered")
	}
	raw, err := json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatalf("marshal trace InputSchema: %v", err)
	}
	schema := string(raw) + "\n" + tool.Description
	for _, want := range []string{"kinds", "READS", "CALLS"} {
		if !strings.Contains(schema, want) {
			t.Errorf("trace schema/description must mention %q so callers can discover the kinds filter; got:\n%s", want, schema)
		}
	}
}
