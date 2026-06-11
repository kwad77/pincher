// SPDX-License-Identifier: MIT

package index

import (
	"context"
	"testing"

	"github.com/kwad77/pincher/internal/ast"
	"github.com/kwad77/pincher/internal/db"
)

// Python unique-suffix CALLS fallback (#python-edge-resolution): when no
// source-root candidate matches a dotted Python to_name exactly, the
// resolver accepts the single project symbol whose qualified name ends
// with "."+to_name — and refuses to guess when two or more match.

// callsEdgeBetween reports whether a CALLS edge fromID→toID exists.
func callsEdgeBetween(t *testing.T, store *db.Store, fromID, toID string) bool {
	t.Helper()
	edges, err := store.EdgesFrom(fromID, []string{"CALLS"})
	if err != nil {
		t.Fatalf("EdgesFrom: %v", err)
	}
	for _, e := range edges {
		if e.ToID == toID {
			return true
		}
	}
	return false
}

func TestIndex_PythonCallsSuffixFallbackResolvesUnique(t *testing.T) {
	if !ast.PythonAvailable() {
		t.Skip("python3 not on PATH; Python CALLS resolution test skipped")
	}

	idx, store := newTestIndexer(t)
	dir := t.TempDir()

	// The real package root is src/app, but the script imports anchored
	// below it (`from pkg.util import ...`) — common when sys.path is
	// manipulated at runtime. Exact candidates ("pkg.util.helper",
	// "src.pkg.util.helper") all miss; only the unique-suffix fallback
	// can bind it to src.app.pkg.util.helper.
	writeFile(t, dir, "src/app/__init__.py", "")
	writeFile(t, dir, "src/app/pkg/__init__.py", "")
	writeFile(t, dir, "src/app/pkg/util.py", `def helper():
    return 1
`)
	writeFile(t, dir, "scripts/run.py", `from pkg.util import helper

def go():
    return helper()
`)

	if _, err := idx.Index(context.Background(), dir, true); err != nil {
		t.Fatalf("Index: %v", err)
	}
	projectID := db.ProjectIDFromPath(dir)

	goSyms, err := store.GetSymbolsByQN(projectID, "scripts.run.go")
	if err != nil || len(goSyms) == 0 {
		t.Fatalf("expected scripts.run.go symbol, got %d (err=%v)", len(goSyms), err)
	}
	helperSyms, err := store.GetSymbolsByQN(projectID, "src.app.pkg.util.helper")
	if err != nil || len(helperSyms) == 0 {
		t.Fatalf("expected src.app.pkg.util.helper symbol, got %d (err=%v)", len(helperSyms), err)
	}

	if !callsEdgeBetween(t, store, goSyms[0].ID, helperSyms[0].ID) {
		t.Errorf("expected unique-suffix fallback to resolve go→helper")
	}
}

func TestIndex_PythonCallsSuffixFallbackRefusesAmbiguous(t *testing.T) {
	if !ast.PythonAvailable() {
		t.Skip("python3 not on PATH; Python CALLS resolution test skipped")
	}

	idx, store := newTestIndexer(t)
	dir := t.TempDir()

	// Two distinct packages both end in pkg.util.helper — the suffix
	// match is ambiguous and the resolver must leave the call unresolved
	// rather than guess between them.
	writeFile(t, dir, "src/alpha/__init__.py", "")
	writeFile(t, dir, "src/alpha/pkg/__init__.py", "")
	writeFile(t, dir, "src/alpha/pkg/util.py", `def helper():
    return 1
`)
	writeFile(t, dir, "src/beta/__init__.py", "")
	writeFile(t, dir, "src/beta/pkg/__init__.py", "")
	writeFile(t, dir, "src/beta/pkg/util.py", `def helper():
    return 2
`)
	writeFile(t, dir, "scripts/run.py", `from pkg.util import helper

def go():
    return helper()
`)

	if _, err := idx.Index(context.Background(), dir, true); err != nil {
		t.Fatalf("Index: %v", err)
	}
	projectID := db.ProjectIDFromPath(dir)

	goSyms, err := store.GetSymbolsByQN(projectID, "scripts.run.go")
	if err != nil || len(goSyms) == 0 {
		t.Fatalf("expected scripts.run.go symbol, got %d (err=%v)", len(goSyms), err)
	}
	for _, qn := range []string{"src.alpha.pkg.util.helper", "src.beta.pkg.util.helper"} {
		syms, err := store.GetSymbolsByQN(projectID, qn)
		if err != nil || len(syms) == 0 {
			t.Fatalf("expected %s symbol, got %d (err=%v)", qn, len(syms), err)
		}
		if callsEdgeBetween(t, store, goSyms[0].ID, syms[0].ID) {
			t.Errorf("ambiguous suffix must stay unresolved, but go→%s edge exists", qn)
		}
	}
}

// Instance-method one-hop inference, end-to-end: the extractor rewrites
// `svc = Svc(); svc.get()` to the imported class path at confidence 0.6,
// and the resolver binds it across files through the src source root.
func TestIndex_PythonInstanceMethodCallsResolveAcrossFiles(t *testing.T) {
	if !ast.PythonAvailable() {
		t.Skip("python3 not on PATH; Python CALLS resolution test skipped")
	}

	idx, store := newTestIndexer(t)
	dir := t.TempDir()

	writeFile(t, dir, "src/myproj/__init__.py", "")
	writeFile(t, dir, "src/myproj/service.py", `class Svc:
    def get(self):
        return 1
`)
	writeFile(t, dir, "src/myproj/main.py", `from myproj.service import Svc

def run():
    svc = Svc()
    return svc.get()
`)

	if _, err := idx.Index(context.Background(), dir, true); err != nil {
		t.Fatalf("Index: %v", err)
	}
	projectID := db.ProjectIDFromPath(dir)

	runSyms, err := store.GetSymbolsByQN(projectID, "src.myproj.main.run")
	if err != nil || len(runSyms) == 0 {
		t.Fatalf("expected src.myproj.main.run symbol, got %d (err=%v)", len(runSyms), err)
	}
	getSyms, err := store.GetSymbolsByQN(projectID, "src.myproj.service.Svc.get")
	if err != nil || len(getSyms) == 0 {
		t.Fatalf("expected src.myproj.service.Svc.get symbol, got %d (err=%v)", len(getSyms), err)
	}

	edges, err := store.EdgesFrom(runSyms[0].ID, []string{"CALLS"})
	if err != nil {
		t.Fatalf("EdgesFrom: %v", err)
	}
	var found bool
	for _, e := range edges {
		if e.ToID == getSyms[0].ID {
			found = true
			// Inferred instance-method edges carry the lower 0.6
			// confidence end-to-end so consumers can distinguish them
			// from statically-written call paths (0.7).
			if e.Confidence != 0.6 {
				t.Errorf("inferred edge confidence = %v, want 0.6", e.Confidence)
			}
			break
		}
	}
	if !found {
		t.Errorf("expected CALLS edge run→Svc.get via one-hop inference; edges=%+v", edges)
	}
}
