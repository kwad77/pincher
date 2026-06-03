// SPDX-License-Identifier: MIT

package index

import (
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/ast"
	"github.com/kwad77/pincher/internal/db"
)

// #1207 v0.66 DOGFOOD: Markdown sibling-heading dups are a COMMON
// real-world shape (reference docs, auto-generated indexes,
// tutorial scaffolds). The goldmark heading walker correctly emits
// one Section per H2/H3/etc; disambiguation by line ensures all
// sections survive in the DB. But the qualified_name_collision
// diagnostic — designed to surface regex-scope blindness in
// code — fired on every Markdown file with repeated subsection
// titles, flooding doctor.extraction_failures with noise.
//
// Fix scope: skip the qualified_name_collision diagnostic for
// Markdown kind only. Other languages (regex-tier code) still
// get the diagnostic because for them, collision IS the symptom.
//
// Table-from-the-start (#1152):
//   - Positive: Markdown sibling-heading dups emit NO
//     qualified_name_collision row.
//   - Negative: Python QN collision STILL emits the row (the
//     diagnostic still works for code).
//   - Cross-check: Markdown with no collisions emits no row
//     either (no spurious false-positive).

func TestRecordExtractionHeuristics_MarkdownSiblingDups_NoFailure(t *testing.T) {
	store := newDBStore(t)
	if err := store.UpsertProject(db.Project{ID: "md-proj", Path: "/md", Name: "md", IndexedAt: time.Now()}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	idx := New(store)

	// Simulate the post-disambiguation state: 1 surviving symbol +
	// QNCollisions recording 3 pre-disambiguation duplicates.
	r := &ast.FileResult{
		Symbols: []ast.ExtractedSymbol{
			{Name: "Dataset Formats", QualifiedName: "axolotl.dataset_formats",
				Kind: "Section", StartByte: 0, EndByte: 100},
		},
		QNCollisions: map[string]int{"axolotl.dataset_formats": 3},
	}
	recordExtractionHeuristics(idx, "md-proj", "Markdown", "axolotl.md", r)

	failures, err := store.ListExtractionFailures("md-proj", 10)
	if err != nil {
		t.Fatalf("ListExtractionFailures: %v", err)
	}
	for _, f := range failures {
		if f.Reason == "qualified_name_collision" {
			t.Errorf("Markdown sibling-heading dup triggered qualified_name_collision — should be skipped\n%+v", f)
		}
	}
}

// Negative: Python (or any non-Markdown code language) collision
// STILL emits the diagnostic. The carve-out is Markdown-specific;
// regex-scope blindness in code is still worth surfacing.
func TestRecordExtractionHeuristics_PythonQNCollision_StillEmits(t *testing.T) {
	store := newDBStore(t)
	if err := store.UpsertProject(db.Project{ID: "py-proj", Path: "/py", Name: "py", IndexedAt: time.Now()}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	idx := New(store)

	r := &ast.FileResult{
		Symbols: []ast.ExtractedSymbol{
			{Name: "helper", QualifiedName: "pkg.helper", Kind: "Function",
				StartByte: 0, EndByte: 50},
		},
		QNCollisions: map[string]int{"pkg.helper": 2},
	}
	recordExtractionHeuristics(idx, "py-proj", "Python", "pkg/helpers.py", r)

	failures, err := store.ListExtractionFailures("py-proj", 10)
	if err != nil {
		t.Fatalf("ListExtractionFailures: %v", err)
	}
	sawQN := false
	for _, f := range failures {
		if f.Reason == "qualified_name_collision" {
			sawQN = true
		}
	}
	if !sawQN {
		t.Error("Python qualified_name_collision should still emit — the carve-out is Markdown-specific")
	}
}

// Cross-check: Markdown with no collisions emits no row either.
// Pin the no-false-positive case (the Markdown skip shouldn't
// silently suppress legitimate failures of other kinds).
func TestRecordExtractionHeuristics_MarkdownNoCollisions_NoFailure(t *testing.T) {
	store := newDBStore(t)
	if err := store.UpsertProject(db.Project{ID: "mc-proj", Path: "/mc", Name: "mc", IndexedAt: time.Now()}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	idx := New(store)

	r := &ast.FileResult{
		Symbols: []ast.ExtractedSymbol{
			{Name: "Intro", QualifiedName: "doc.intro", Kind: "Section",
				StartByte: 0, EndByte: 50},
		},
		// QNCollisions empty — no dup detected.
	}
	recordExtractionHeuristics(idx, "mc-proj", "Markdown", "doc.md", r)

	failures, err := store.ListExtractionFailures("mc-proj", 10)
	if err != nil {
		t.Fatalf("ListExtractionFailures: %v", err)
	}
	if len(failures) != 0 {
		t.Errorf("clean Markdown file emitted %d failure(s); want 0", len(failures))
	}
}
