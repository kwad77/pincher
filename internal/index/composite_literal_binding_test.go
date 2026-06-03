// SPDX-License-Identifier: MIT

package index

import (
	"context"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// #576: function values bound at file scope inside composite literals
// (`var X = T{Field: fn}`) didn't get a CALLS edge because the binding
// pass (#565) only ran on FuncDecl bodies, not on top-level GenDecl
// initializer expressions. This is the canonical "registry of handlers"
// pattern (e.g. pincher's own `var CodexTarget = Target{DetectFn:
// detectCodex, ...}`); pre-fix the bound function false-flagged as
// dead.
//
// The fix walks identifier references inside top-level var/const
// blocks via `extractGoFileLevelReads`, mirroring `extractGoReads` for
// FuncDecl bodies. The same resolveReads binding pass converts
// function-targeted READS to confidence-0.4 CALLS edges.
func TestResolveCalls_VarDeclCompositeLiteralBinding_NotDead(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "registry/registry.go", `package registry

type Handler struct {
	Name     string
	DetectFn func(string) bool
	WriteFn  func() error
}

// HandlerRegistry binds two functions via top-level composite literal
// — the canonical pincher CodexTarget shape. Pre-fix detectFoo and
// writeFoo would false-flag as dead because the file-scope binding
// produced no edge.
var HandlerRegistry = Handler{
	Name:     "foo",
	DetectFn: detectFoo,
	WriteFn:  writeFoo,
}

func detectFoo(cwd string) bool { return cwd != "" }
func writeFoo() error           { return nil }
`)
	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("Index: %v", err)
	}
	pid := db.ProjectIDFromPath(dir)

	dead, err := store.GetDeadCode(pid, []string{"Function"}, "Go", 1.0, 100)
	if err != nil {
		t.Fatalf("GetDeadCode: %v", err)
	}
	for _, s := range dead {
		if s.Name == "detectFoo" || s.Name == "writeFoo" {
			t.Errorf("%s in %s flagged dead — composite-literal binding regression (#576)",
				s.Name, s.FilePath)
		}
	}

	// Both bound functions must have ≥1 inbound CALLS edge from the
	// file-level Module symbol (the binding pass converts the READS
	// edge to CALLS at confidence 0.4).
	for _, name := range []string{"detectFoo", "writeFoo"} {
		syms, err := store.GetSymbolsByName(pid, name, 5)
		if err != nil {
			t.Fatalf("GetSymbolsByName %s: %v", name, err)
		}
		if len(syms) == 0 {
			t.Fatalf("expected to find %s symbol", name)
		}
		results, err := store.TraceViaCTEScoped(pid, syms[0].ID, "inbound", []string{"CALLS"}, 3)
		if err != nil {
			t.Fatalf("TraceViaCTEScoped %s: %v", name, err)
		}
		if len(results) == 0 {
			t.Errorf("%s has 0 inbound CALLS edges — binding pass missed the composite-literal field assignment (#576)",
				name)
		}
	}
}
