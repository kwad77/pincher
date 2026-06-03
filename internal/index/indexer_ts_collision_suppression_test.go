// SPDX-License-Identifier: MIT

package index

import (
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/ast"
	"github.com/kwad77/pincher/internal/db"
)

// #1208 v0.66 DOGFOOD: after dropTSOverloadSignatures handles the
// dominant TS dup-source, the residual collisions (top-level const +
// object-property arrow with same name, JSX polymorphic variants,
// re-exports vs locals) are legitimate real-world shapes the regex
// extractor can't scope-resolve without an AST. The fix mirrors the
// Markdown #1207 carve-out: skip the qualified_name_collision
// diagnostic for TypeScript, since disambiguateDuplicates still
// `~<line>`-suffixes the second occurrence so every symbol survives.

// Positive: when a TS FileResult has a QNCollisions entry,
// recordExtractionHeuristics does NOT record a failure row.
func TestRecordExtractionHeuristics_TSCollisions_NoFailure(t *testing.T) {
	store := newDBStore(t)
	if err := store.UpsertProject(db.Project{ID: "ts-skip", Path: "/ts", Name: "ts", IndexedAt: time.Now()}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	idx := New(store)

	// Synthesize a TS FileResult that DID have a collision (the
	// extractor's pre-disambiguation count comes through this map).
	result := &ast.FileResult{
		QNCollisions: map[string]int{"skwad-app.lib.state-laws.law": 2},
	}
	recordExtractionHeuristics(idx, "ts-skip", "TypeScript", "lib/state-laws.ts", result)

	fails, err := store.ListExtractionFailures("ts-skip", 10)
	if err != nil {
		t.Fatalf("ListExtractionFailures: %v", err)
	}
	for _, f := range fails {
		if f.Reason == "qualified_name_collision" {
			t.Errorf("expected no qualified_name_collision row for TypeScript; got %+v", f)
		}
	}
}

// v0.71 dogfood: TSX files were falling through #1208's TypeScript
// carve-out because the language tag is "TSX" not "TypeScript". Codex-
// corpus TSX files produced 15+ collisions per file on object-property
// keys. Extend the carve-out.
func TestRecordExtractionHeuristics_TSXCollisions_NoFailure_v071Dogfood(t *testing.T) {
	store := newDBStore(t)
	if err := store.UpsertProject(db.Project{ID: "tsx-skip", Path: "/tsx", Name: "tsx", IndexedAt: time.Now()}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	idx := New(store)

	result := &ast.FileResult{
		QNCollisions: map[string]int{"app.src.renderer.tabs.GraphExplorerGPU.graph": 15},
	}
	recordExtractionHeuristics(idx, "tsx-skip", "TSX", "src/renderer/tabs/GraphExplorerGPU.tsx", result)

	fails, err := store.ListExtractionFailures("tsx-skip", 10)
	if err != nil {
		t.Fatalf("ListExtractionFailures: %v", err)
	}
	for _, f := range fails {
		if f.Reason == "qualified_name_collision" {
			t.Errorf("expected no qualified_name_collision row for TSX; got %+v", f)
		}
	}
}

// Control: a Go FileResult with the same collision shape SHOULD still
// produce a qualified_name_collision row. The TS suppression must be
// narrow to TypeScript; other-language collisions remain diagnostic
// signal.
func TestRecordExtractionHeuristics_GoCollisions_StillEmits(t *testing.T) {
	store := newDBStore(t)
	if err := store.UpsertProject(db.Project{ID: "go-ctrl", Path: "/go", Name: "go", IndexedAt: time.Now()}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	idx := New(store)

	result := &ast.FileResult{
		QNCollisions: map[string]int{"pkg.dup": 2},
	}
	recordExtractionHeuristics(idx, "go-ctrl", "Go", "pkg/file.go", result)

	fails, err := store.ListExtractionFailures("go-ctrl", 10)
	if err != nil {
		t.Fatalf("ListExtractionFailures: %v", err)
	}
	var sawCollision bool
	for _, f := range fails {
		if f.Reason == "qualified_name_collision" {
			sawCollision = true
		}
	}
	if !sawCollision {
		t.Errorf("Go collisions must still surface as qualified_name_collision; got fails: %+v", fails)
	}
}
