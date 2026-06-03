// SPDX-License-Identifier: MIT

package index

import (
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/ast"
	"github.com/kwad77/pincher/internal/db"
)

// #1203 v0.66 DOGFOOD: recordExtractionHeuristics flooded
// extraction_failures with byte_range_negative rows for empty
// __init__.py files. Python's Module symbol on a zero-byte file
// lands at StartByte=0, EndByte=0 — a legitimate zero-span Module
// representing an empty package marker. The pre-fix invariant
// (`EndByte <= StartByte`) caught this as a failure when in fact
// it's the only correct representation.
//
// Fix scope: carve out Module kind specifically. Other kinds
// (Function/Method/Class/etc.) with zero-or-negative span are
// still real bugs.
//
// Table-from-the-start (#1152):
//   - Positive: empty Module symbol does NOT record a failure.
//   - Negative: zero-span Function symbol STILL records a failure
//     (the carve-out is Module-only).
//   - Control: a real negative-span Module (EndByte < StartByte)
//     STILL records — the carve-out is for equal-only, not for
//     genuinely inverted ranges.
//   - Cross-check: a healthy Module symbol with a real span
//     emits no failure (no regression on the common case).

func TestRecordExtractionHeuristics_EmptyModule_NoFailure(t *testing.T) {
	store := newDBStore(t)
	if err := store.UpsertProject(db.Project{ID: "em-proj", Path: "/em", Name: "em", IndexedAt: time.Now()}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	idx := New(store)

	empty := &ast.FileResult{
		Symbols: []ast.ExtractedSymbol{
			// Empty __init__.py: Module symbol with zero span.
			{Name: "__init__", QualifiedName: "pkg.helpers.__init__", Kind: "Module",
				StartByte: 0, EndByte: 0},
		},
	}
	recordExtractionHeuristics(idx, "em-proj", "Python", "pkg/helpers/__init__.py", empty)

	failures, err := store.ListExtractionFailures("em-proj", 10)
	if err != nil {
		t.Fatalf("ListExtractionFailures: %v", err)
	}
	if len(failures) != 0 {
		t.Errorf("empty Module triggered %d failure(s); want 0 (legitimate zero-span shape)\nrows: %+v",
			len(failures), failures)
	}
}

// Negative: zero-span on a non-Module kind is still a real bug
// (a Function with no body bytes means the extractor anchored
// nothing). The carve-out must NOT generalize past Module.
func TestRecordExtractionHeuristics_ZeroSpanFunction_StillFails(t *testing.T) {
	store := newDBStore(t)
	if err := store.UpsertProject(db.Project{ID: "zf-proj", Path: "/zf", Name: "zf", IndexedAt: time.Now()}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	idx := New(store)

	bad := &ast.FileResult{
		Symbols: []ast.ExtractedSymbol{
			{Name: "Bad", QualifiedName: "pkg.Bad", Kind: "Function",
				StartByte: 42, EndByte: 42},
		},
	}
	recordExtractionHeuristics(idx, "zf-proj", "Go", "bad.go", bad)

	failures, err := store.ListExtractionFailures("zf-proj", 10)
	if err != nil {
		t.Fatalf("ListExtractionFailures: %v", err)
	}
	if len(failures) != 1 {
		t.Fatalf("zero-span Function: got %d failures, want 1", len(failures))
	}
	if failures[0].Reason != "byte_range_negative" {
		t.Errorf("reason = %q, want byte_range_negative", failures[0].Reason)
	}
}

// Control: a genuinely inverted (negative) span on a Module is
// still a real bug — the extractor wrote nonsense ordering. The
// equality carve-out applies only to EndByte == StartByte, not
// EndByte < StartByte.
func TestRecordExtractionHeuristics_NegativeSpanModule_StillFails(t *testing.T) {
	store := newDBStore(t)
	if err := store.UpsertProject(db.Project{ID: "nm-proj", Path: "/nm", Name: "nm", IndexedAt: time.Now()}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	idx := New(store)

	bad := &ast.FileResult{
		Symbols: []ast.ExtractedSymbol{
			// Inverted span — extractor bug.
			{Name: "broken", QualifiedName: "pkg.broken", Kind: "Module",
				StartByte: 100, EndByte: 50},
		},
	}
	recordExtractionHeuristics(idx, "nm-proj", "Python", "broken.py", bad)

	failures, err := store.ListExtractionFailures("nm-proj", 10)
	if err != nil {
		t.Fatalf("ListExtractionFailures: %v", err)
	}
	if len(failures) != 1 {
		t.Fatalf("inverted-span Module: got %d failures, want 1", len(failures))
	}
	if failures[0].Reason != "byte_range_negative" {
		t.Errorf("reason = %q, want byte_range_negative", failures[0].Reason)
	}
}

// Cross-check: a healthy Module symbol with a real positive span
// emits no failure. Pin the no-regression case.
func TestRecordExtractionHeuristics_HealthyModule_NoFailure(t *testing.T) {
	store := newDBStore(t)
	if err := store.UpsertProject(db.Project{ID: "hm-proj", Path: "/hm", Name: "hm", IndexedAt: time.Now()}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	idx := New(store)

	healthy := &ast.FileResult{
		Symbols: []ast.ExtractedSymbol{
			{Name: "mod", QualifiedName: "pkg.mod", Kind: "Module",
				StartByte: 0, EndByte: 500},
		},
	}
	recordExtractionHeuristics(idx, "hm-proj", "Python", "mod.py", healthy)

	failures, err := store.ListExtractionFailures("hm-proj", 10)
	if err != nil {
		t.Fatalf("ListExtractionFailures: %v", err)
	}
	if len(failures) != 0 {
		t.Errorf("healthy Module triggered %d failure(s); want 0", len(failures))
	}
}
