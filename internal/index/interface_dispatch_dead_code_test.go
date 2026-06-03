// SPDX-License-Identifier: MIT

package index

import (
	"context"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// #493: interface-dispatch dead_code precision. Pre-fix the cypher
// engine's whereExpr `eval` methods (condExpr, binaryExpr, notExpr)
// were flagged dead because the only caller goes through interface
// dispatch (`w.eval(...)`) — invisible to the static call graph.
//
// Cheap heuristic in v0.20: any Method whose name matches an
// interface method declared in the same project gets excluded from
// dead_code. Test mirrors the canonical repro from #493.
const interfaceDispatchSrc = `package eval

type whereExpr interface {
    eval(row map[string]any) bool
}

type condExpr struct{ value bool }

func (c *condExpr) eval(row map[string]any) bool {
    return c.value
}

type binaryExpr struct{ left, right whereExpr }

func (b *binaryExpr) eval(row map[string]any) bool {
    return b.left.eval(row) && b.right.eval(row)
}

func matchesWhere(row map[string]any, w whereExpr) bool {
    if w == nil {
        return true
    }
    return w.eval(row)
}
`

func TestDeadCode_InterfaceDispatchMethodsExcluded(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "eval/eval.go", interfaceDispatchSrc)

	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("Index: %v", err)
	}

	pid := db.ProjectIDFromPath(dir)

	// Sanity: confirm the interface_methods row exists for whereExpr.eval.
	methods, err := store.LoadInterfaceMethods(pid)
	if err != nil {
		t.Fatalf("LoadInterfaceMethods: %v", err)
	}
	var sawEval bool
	for _, m := range methods {
		if m.MethodName == "eval" {
			sawEval = true
			break
		}
	}
	if !sawEval {
		t.Fatalf("expected interface_methods row for 'eval'; got %v", methods)
	}

	// dead_code request shape: kind=Method, default min_confidence=1.0
	// for Go AST extractor.
	dead, err := store.GetDeadCode(pid, []string{"Method"}, "Go", 1.0, 100)
	if err != nil {
		t.Fatalf("GetDeadCode: %v", err)
	}
	for _, s := range dead {
		if s.Name == "eval" {
			t.Errorf("Method %q in %s flagged dead — interface-dispatch heuristic regression (#493). "+
				"The whereExpr interface declares eval(); concrete implementations must NOT be flagged dead.",
				s.QualifiedName, s.FilePath)
		}
	}
}

// Negative pin: methods whose names DON'T match any interface method
// should still surface as dead_code if they have no callers. Without
// this gate, the cheap heuristic would over-suppress and dead_code
// would lose its signal entirely.
func TestDeadCode_NonInterfaceMethodStillDead(t *testing.T) {
	src := `package nodead

type S struct{}

func (s *S) lonelyHelper() {}
`
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "nodead/n.go", src)
	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("Index: %v", err)
	}
	pid := db.ProjectIDFromPath(dir)
	dead, err := store.GetDeadCode(pid, []string{"Method"}, "Go", 1.0, 100)
	if err != nil {
		t.Fatalf("GetDeadCode: %v", err)
	}
	var found bool
	for _, s := range dead {
		if s.Name == "lonelyHelper" {
			found = true
		}
	}
	if !found {
		t.Errorf("lonelyHelper should still surface in dead_code; got %d entries", len(dead))
	}
}
