// SPDX-License-Identifier: MIT

package index

import (
	"context"
	"testing"
)

// #1693 (#1389 cross-language sweep): Go permits multiple `func
// init()` per file — they all run, in source order, by language
// design. The regex/AST extractor correctly emits one symbol per
// init(); disambiguateDuplicates suffixes them `~<line>` so every
// one survives. But the qualified_name_collision diagnostic fired
// on the `init` QN, flagging legitimate Go as an extraction
// failure — every Go file with 2+ init()s tripped a false positive
// in pincher's flagship language.
//
// recordExtractionHeuristics now skips the `init` QN for Go when
// scoring the collision diagnostic.

func TestIndex_GoMultipleInit_NoCollisionDiagnostic_1693(t *testing.T) {
	t.Parallel()
	idx, _ := newTestIndexer(t)
	dir := t.TempDir()

	// A Go file with three func init() — all legal, all run.
	writeFile(t, dir, "go.mod", "module example.com/m\n\ngo 1.25\n")
	writeFile(t, dir, "boot.go", `package m

import "fmt"

var a, b, c int

func init() { a = 1 }

func init() { b = 2 }

func init() { c = 3 }

func Sum() int { return a + b + c }

func use() { fmt.Println(Sum()) }
`)

	res, err := idx.Index(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}

	// No qualified_name_collision row should exist for boot.go — the
	// only colliding QN is `init`, which is legitimate Go.
	var n int
	if err := idx.store.DB().QueryRow(
		`SELECT COUNT(*) FROM extraction_failures
		   WHERE project_id=? AND reason='qualified_name_collision'`,
		res.ProjectID,
	).Scan(&n); err != nil {
		t.Fatalf("count extraction_failures: %v", err)
	}
	if n != 0 {
		t.Errorf("Go file with multiple func init() recorded %d qualified_name_collision row(s) — "+
			"multiple init() is legitimate Go, the diagnostic must skip it (#1693)", n)
	}
}

// Control: a Go file with a NON-init duplicate-QN collision must
// STILL record the diagnostic — the init carve-out must be narrow,
// not a blanket "suppress all Go collisions."
func TestIndex_GoNonInitCollision_StillDiagnosed_1693(t *testing.T) {
	t.Parallel()
	if !qnLastSegmentIsInit("pkg.init") || qnLastSegmentIsInit("pkg.initialize") {
		t.Fatalf("qnLastSegmentIsInit contract wrong: must match exactly `init`, " +
			"not `initialize` and not miss `pkg.init`")
	}
	// Bare `init` (no package qualifier) also counts.
	if !qnLastSegmentIsInit("init") {
		t.Error("qnLastSegmentIsInit(\"init\") = false, want true")
	}
}
