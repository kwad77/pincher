// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// `mcp__pincher__schema project=X` returned `{symbols:0, edges:0,
// node_kinds:{}, edge_kinds:{}}` silently when the resolved project
// had 0 indexed symbols. The most common cause is a name-collision
// with a stale project — `project="pincher"` matching a dead row
// instead of `pincher-repo`. Now an _meta.diagnosis names the
// possibility and the next_steps point at the recovery paths.

func TestHandleSchema_EmptyProjectEmitsDiagnosis(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)

	store.UpsertProject(db.Project{
		ID: "p-empty", Path: "/tmp/p-empty", Name: "p-empty",
		IndexedAt: time.Now(), FileCount: 0, SymCount: 0, EdgeCount: 0,
	})

	res, err := srv.handleSchema(context.Background(), makeReq(map[string]any{
		"project": "p-empty",
	}))
	if err != nil {
		t.Fatalf("handleSchema: %v", err)
	}
	body := decode(t, res)

	if got := body["symbols"].(float64); got != 0 {
		t.Errorf("symbols = %v, want 0", got)
	}
	meta, _ := body["_meta"].(map[string]any)
	if meta == nil {
		t.Fatal("_meta missing on empty-project response")
	}
	diag, _ := meta["diagnosis"].(string)
	if !strings.Contains(diag, "0 indexed symbols") {
		t.Errorf("diagnosis should name the empty state; got %q", diag)
	}
	if !strings.Contains(diag, "name-collision") {
		t.Errorf("diagnosis should name the collision hypothesis; got %q", diag)
	}
	steps, _ := meta["next_steps"].([]any)
	if len(steps) < 2 {
		t.Fatalf("expected list + index next_steps; got %d (%v)", len(steps), steps)
	}
	// Recovery list must mention include_dead and force-reindex.
	wantArgs := []string{`"include_dead":true`, `"force":true`}
	for _, want := range wantArgs {
		found := false
		for _, s := range steps {
			step, _ := s.(map[string]any)
			if args, _ := step["args"].(string); strings.Contains(args, want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected next_step args containing %q; got %v", want, steps)
		}
	}
}

// Non-empty project: schema response must NOT add the diagnosis.
// Pin the non-regression.
func TestHandleSchema_NonEmptyProjectNoDiagnosis(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)

	store.UpsertProject(db.Project{
		ID: "p-full", Path: "/tmp/p-full", Name: "p-full",
		IndexedAt: time.Now(), FileCount: 1, SymCount: 1, EdgeCount: 0,
	})
	store.BulkUpsertSymbols([]db.Symbol{{
		ID: "p-full::main.A#Function", ProjectID: "p-full", FilePath: "a.go",
		Name: "A", QualifiedName: "main.A", Kind: "Function", Language: "Go",
		ExtractionConfidence: 1.0,
	}})

	res, err := srv.handleSchema(context.Background(), makeReq(map[string]any{
		"project": "p-full",
	}))
	if err != nil {
		t.Fatalf("handleSchema: %v", err)
	}
	body := decode(t, res)
	meta, _ := body["_meta"].(map[string]any)
	// Diagnosis must not be present.
	if diag, present := meta["diagnosis"]; present {
		t.Errorf("non-empty project should not emit diagnosis; got %v", diag)
	}
}
