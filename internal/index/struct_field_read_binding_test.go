// SPDX-License-Identifier: MIT

package index

import (
	"context"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// #760: the resolveReads binding pass (#565) converts a READS edge that
// resolves to a project Function/Method into a confidence-0.4 CALLS
// edge — that's how function-value bindings (`w.doFn = w.defaultDo`,
// `T{Handler: fn}`) stay reachable in the call graph. But the same path
// can't tell `e.Confidence` (a struct-field read) from `w.defaultDo` (a
// method value) on name alone: when the trailing selector collides with
// a same-named project Method, the field read false-binds to it.
//
// The fix threads the base expression's declared type through
// ExtractedEdge.BaseType (extractor) and pending_edges.base_type
// (schema v26). The binding pass now drops the edge when BaseType names
// a project struct that has a field of the READS edge's ToName — a
// positive confirmation that the AST node was a field access.
//
// This test pins both directions: the false bind is gone, AND a genuine
// method-value reference still produces the #565 binding edge (no
// over-suppression).
func TestResolveReads_StructFieldRead_NoFalseMethodBinding(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "myproj/edge.go", `package myproj

type Edge struct {
	Confidence float64
	Handler    func()
}

// scorer is a fieldless struct whose method name collides with Edge's
// Confidence field — the exact #760 shape (e.Confidence false-binding
// to *hclExtractor.Confidence in the real repro).
type scorer struct{}

func (s *scorer) Confidence() float64 { return 1.0 }

// consume ranges over a []Edge param and reads e.Confidence — a
// struct-field read. Pre-#760 the binding pass false-bound it to
// (*scorer).Confidence.
func consume(edges []Edge) float64 {
	var total float64
	for _, e := range edges {
		total += e.Confidence
	}
	return total
}

// realBinding takes a genuine method value — *scorer has a Confidence
// METHOD (not field), so this must still produce the #565 binding edge.
func realBinding() func() float64 {
	s := &scorer{}
	fn := s.Confidence
	return fn
}
`)
	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("Index: %v", err)
	}
	pid := db.ProjectIDFromPath(dir)

	methodSyms, err := store.GetSymbolsByQN(pid, "myproj.*scorer.Confidence")
	if err != nil || len(methodSyms) == 0 {
		t.Fatalf("expected myproj.*scorer.Confidence Method, got %d (err=%v)", len(methodSyms), err)
	}
	methodID := methodSyms[0].ID

	// #760: e.Confidence is a field read of Edge.Confidence — no CALLS
	// edge to the same-named *scorer.Confidence method.
	consumeSyms, err := store.GetSymbolsByQN(pid, "myproj.consume")
	if err != nil || len(consumeSyms) == 0 {
		t.Fatalf("expected myproj.consume, got %d (err=%v)", len(consumeSyms), err)
	}
	consumeEdges, err := store.EdgesFrom(consumeSyms[0].ID, []string{"CALLS"})
	if err != nil {
		t.Fatalf("EdgesFrom consume: %v", err)
	}
	for _, e := range consumeEdges {
		if e.ToID == methodID {
			t.Errorf("consume false-bound e.Confidence (struct-field read) to *scorer.Confidence — #760 regression; edge=%+v", e)
		}
	}

	// Control: `fn := s.Confidence` is a genuine method value — the
	// #565 binding pass must still emit its CALLS edge.
	rbSyms, err := store.GetSymbolsByQN(pid, "myproj.realBinding")
	if err != nil || len(rbSyms) == 0 {
		t.Fatalf("expected myproj.realBinding, got %d (err=%v)", len(rbSyms), err)
	}
	rbEdges, err := store.EdgesFrom(rbSyms[0].ID, []string{"CALLS"})
	if err != nil {
		t.Fatalf("EdgesFrom realBinding: %v", err)
	}
	var bound bool
	for _, e := range rbEdges {
		if e.ToID == methodID {
			bound = true
		}
	}
	if !bound {
		t.Errorf("realBinding's `fn := s.Confidence` method-value reference lost its #565 binding CALLS edge — #760 over-suppressed; edges=%+v", rbEdges)
	}
}
