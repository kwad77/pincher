// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// Ditto compression in trace hops (compact=true): consecutive nodes
// within a depth block that share a file_path omit the repeated field;
// the first occurrence carries it. Deterministic and locally decodable
// — the consumer scans up the nodes array to the nearest entry that
// carries file_path. Default (non-compact) shape is untouched.

func seedDittoGraph(t *testing.T, store *db.Store, projectID string) {
	t.Helper()
	// Hub A called from three same-file callers + one other-file caller
	// — guarantees at least one consecutive same-file pair at depth 1
	// regardless of row ordering (any arrangement of {b,b,b,c} has an
	// adjacent b,b).
	syms := []db.Symbol{
		{ID: "a.go::pkg.A#Function", ProjectID: projectID,
			FilePath: "a.go", Name: "A", QualifiedName: "pkg.A",
			Kind: "Function", Language: "Go", ExtractionConfidence: 1.0},
		{ID: "b.go::pkg.B1#Function", ProjectID: projectID,
			FilePath: "b.go", Name: "B1", QualifiedName: "pkg.B1",
			Kind: "Function", Language: "Go", ExtractionConfidence: 1.0},
		{ID: "b.go::pkg.B2#Function", ProjectID: projectID,
			FilePath: "b.go", Name: "B2", QualifiedName: "pkg.B2",
			Kind: "Function", Language: "Go", ExtractionConfidence: 1.0},
		{ID: "b.go::pkg.B3#Function", ProjectID: projectID,
			FilePath: "b.go", Name: "B3", QualifiedName: "pkg.B3",
			Kind: "Function", Language: "Go", ExtractionConfidence: 1.0},
		{ID: "c.go::pkg.C#Function", ProjectID: projectID,
			FilePath: "c.go", Name: "C", QualifiedName: "pkg.C",
			Kind: "Function", Language: "Go", ExtractionConfidence: 1.0},
	}
	mustUpsertSymbols(t, store, syms)
	edges := []db.Edge{}
	for _, from := range []string{"b.go::pkg.B1#Function", "b.go::pkg.B2#Function", "b.go::pkg.B3#Function", "c.go::pkg.C#Function"} {
		edges = append(edges, db.Edge{FromID: from, ToID: "a.go::pkg.A#Function", Kind: "CALLS", Confidence: 1.0})
	}
	mustUpsertEdges(t, store, projectID, edges)
}

// nodesOf flattens a trace response's hops into per-depth node lists.
func nodesOf(t *testing.T, body map[string]any) [][]map[string]any {
	t.Helper()
	hops, _ := body["hops"].([]any)
	var out [][]map[string]any
	for _, h := range hops {
		raw, _ := h.(map[string]any)["nodes"].([]any)
		nodes := make([]map[string]any, 0, len(raw))
		for _, n := range raw {
			nodes = append(nodes, n.(map[string]any))
		}
		out = append(out, nodes)
	}
	return out
}

// Round-trip: ditto-compressed compact hops decode (scan-up) back to
// exactly the file_paths the default shape carries, and at least one
// repeat was actually omitted.
func TestHandleTrace_Compact_DittoRoundTrip(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	mustUpsertProject(t, store, "p-ditto", "/tmp/p-ditto", "ditto")
	srv.sessionID = "p-ditto"
	srv.sessionRoot = "/tmp/p-ditto"
	seedDittoGraph(t, store, "p-ditto")

	// Ground truth: default (non-compact) shape — every node carries
	// file_path.
	fullRes, err := srv.handleTrace(context.Background(), makeReq(map[string]any{
		"name": "A", "direction": "inbound",
	}))
	if err != nil {
		t.Fatalf("handleTrace full: %v", err)
	}
	truth := map[string]string{} // id -> file_path
	for _, nodes := range nodesOf(t, decode(t, fullRes)) {
		for _, n := range nodes {
			fp, ok := n["file_path"].(string)
			if !ok || fp == "" {
				t.Fatalf("default shape must carry file_path on every node; got %v", n)
			}
			truth[n["id"].(string)] = fp
		}
	}
	if len(truth) != 4 {
		t.Fatalf("fixture must yield 4 inbound hops; got %d", len(truth))
	}

	// Compact: ditto applies.
	compactRes, err := srv.handleTrace(context.Background(), makeReq(map[string]any{
		"name": "A", "direction": "inbound", "compact": true,
	}))
	if err != nil {
		t.Fatalf("handleTrace compact: %v", err)
	}
	compactBody := decode(t, compactRes)
	omitted := 0
	for _, nodes := range nodesOf(t, compactBody) {
		current := ""
		for i, n := range nodes {
			if fp, ok := n["file_path"].(string); ok {
				current = fp
			} else {
				omitted++
				if current == "" {
					t.Fatalf("node %d omits file_path with no prior carrier in its depth block — not locally decodable: %v", i, n)
				}
			}
			// Scan-up decode must reproduce the ground truth.
			id, _ := n["id"].(string)
			if want := truth[id]; current != want {
				t.Errorf("ditto decode for %s: got %q, want %q", id, current, want)
			}
		}
	}
	if omitted == 0 {
		t.Errorf("fixture has 3 same-file callers — at least one consecutive repeat must be ditto-omitted")
	}

	// Measure the saving for the changelog: the compact hops array as
	// returned (with ditto) vs the identical array with every omitted
	// file_path restored. Marshal the ditto form FIRST — the
	// restoration below mutates the same node maps in place.
	dittoJSON, _ := json.Marshal(compactBody["hops"])
	for _, nodes := range nodesOf(t, compactBody) {
		current := ""
		for _, n := range nodes {
			if fp, ok := n["file_path"].(string); ok {
				current = fp
			} else {
				n["file_path"] = current
			}
		}
	}
	restoredJSON, _ := json.Marshal(compactBody["hops"])
	t.Logf("ditto on 4-hop hub trace: %d tokens vs %d without ditto (%d vs %d bytes, %d file_path omissions)",
		db.ApproxTokens(string(dittoJSON)), db.ApproxTokens(string(restoredJSON)),
		len(dittoJSON), len(restoredJSON), omitted)
}

// Zero compat risk: default (compact omitted) never ditto-compresses —
// every node carries file_path even when consecutive nodes share one.
func TestHandleTrace_Default_NoDitto(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	mustUpsertProject(t, store, "p-ditto-def", "/tmp/p-ditto-def", "dittodef")
	srv.sessionID = "p-ditto-def"
	srv.sessionRoot = "/tmp/p-ditto-def"
	seedDittoGraph(t, store, "p-ditto-def")

	res, err := srv.handleTrace(context.Background(), makeReq(map[string]any{
		"name": "A", "direction": "inbound",
	}))
	if err != nil {
		t.Fatalf("handleTrace: %v", err)
	}
	for _, nodes := range nodesOf(t, decode(t, res)) {
		for _, n := range nodes {
			if _, ok := n["file_path"].(string); !ok {
				t.Errorf("default shape must carry file_path on every node; got %v", n)
			}
		}
	}
}
