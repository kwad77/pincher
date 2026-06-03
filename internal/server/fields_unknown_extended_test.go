// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #914 extends #908: trace and changes also surface a warning when
// `fields=` names a key that doesn't exist on the response. Pre-fix
// both handlers used `projectFields` which silently dropped unknowns.

func TestHandleTrace_UnknownField_DroppedAndWarned(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p914a"
	store.UpsertProject(db.Project{ID: "p914a", Path: "/tmp/p914a", Name: "p914a", IndexedAt: time.Now()})

	mustUpsertSymbols(t, store, []db.Symbol{
		{ID: "p914a::pkg.A#Function", ProjectID: "p914a",
			FilePath: "f.go", Name: "A", QualifiedName: "pkg.A",
			Kind: "Function", Language: "Go", ExtractionConfidence: 1.0},
		{ID: "p914a::pkg.B#Function", ProjectID: "p914a",
			FilePath: "f.go", Name: "B", QualifiedName: "pkg.B",
			Kind: "Function", Language: "Go", ExtractionConfidence: 1.0},
	})
	if err := store.BulkUpsertEdges([]db.Edge{
		{ProjectID: "p914a", FromID: "p914a::pkg.A#Function",
			ToID: "p914a::pkg.B#Function", Kind: "CALLS", Confidence: 1},
	}); err != nil {
		t.Fatal(err)
	}

	result, err := srv.handleTrace(context.Background(), makeReq(map[string]any{
		"name":      "A",
		"direction": "outbound",
		"fields":    "hops,bogus_trace_field",
	}))
	if err != nil {
		t.Fatal(err)
	}
	body := decode(t, result)
	if _, present := body["bogus_trace_field"]; present {
		t.Errorf("bogus_trace_field must be absent; got %v", body["bogus_trace_field"])
	}
	if !fieldsHasUnknownWarning(t, body, "bogus_trace_field") {
		t.Errorf("trace response must warn about the unknown field; got _meta=%v", body["_meta"])
	}
}

// changes uses the same `projectAndCheckFields` call path as trace
// (both end with `data = projectAndCheckFields(data, parseFieldsArg(...))`),
// so the trace coverage above proves the helper. A dedicated changes
// end-to-end test needs a real git repo with staged/unstaged diffs and
// is exercised in the existing handleChanges tests; the dedicated
// fields= path is exercised here via the trace handler, which shares
// the same code.

// Control: trace with valid fields= produces no warning.
func TestHandleTrace_AllKnownFields_NoWarning(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p914c"
	store.UpsertProject(db.Project{ID: "p914c", Path: "/tmp/p914c", Name: "p914c", IndexedAt: time.Now()})
	mustUpsertSymbols(t, store, []db.Symbol{
		{ID: "p914c::pkg.X#Function", ProjectID: "p914c",
			FilePath: "f.go", Name: "X", QualifiedName: "pkg.X",
			Kind: "Function", Language: "Go", ExtractionConfidence: 1.0},
	})

	result, err := srv.handleTrace(context.Background(), makeReq(map[string]any{
		"name":   "X",
		"fields": "hops,total",
	}))
	if err != nil {
		t.Fatal(err)
	}
	body := decode(t, result)
	if meta, _ := body["_meta"].(map[string]any); meta != nil {
		switch w := meta["warnings"].(type) {
		case []string:
			for _, msg := range w {
				if strings.Contains(msg, "matched no keys") {
					t.Errorf("valid fields must not produce unknown-field warning; got %q", msg)
				}
			}
		case []any:
			for _, msg := range w {
				if s, _ := msg.(string); strings.Contains(s, "matched no keys") {
					t.Errorf("valid fields must not produce unknown-field warning; got %q", s)
				}
			}
		}
	}
}
