// SPDX-License-Identifier: MIT

package index

import (
	"context"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// #1778: a method call through a package-level singleton var declared in
// a *sibling file* of the same package dropped its CALLS edge. #1747's
// extract-time pre-pass is file-scoped, so the call carried no
// ReceiverType and resolveByReceiverType had nothing to bind. The
// extractor now stamps the inferred singleton type into the Variable
// symbol's Signature and resolveCalls recovers it cross-file.

const crossFileSingletonRunnerSrc = `package store

type runner struct{}

func (r *runner) execute(s string) string { return s }

var defaultRunner = &runner{}
`

const crossFileSingletonCallerSrc = `package store

func DoWork(s string) string {
	return defaultRunner.execute(s)
}
`

func methodID(t *testing.T, store *db.Store, pid, name string) string {
	t.Helper()
	syms, err := store.GetSymbolsByName(pid, name, 5)
	if err != nil {
		t.Fatalf("GetSymbolsByName %s: %v", name, err)
	}
	for _, s := range syms {
		if s.Kind == "Method" {
			return s.ID
		}
	}
	t.Fatalf("expected a Method named %s; got %d symbols, none Method", name, len(syms))
	return ""
}

func hasInboundCaller(t *testing.T, store *db.Store, pid, methodSymID, callerName string) bool {
	t.Helper()
	results, err := store.TraceViaCTEScoped(pid, methodSymID, "inbound", []string{"CALLS"}, 3)
	if err != nil {
		t.Fatalf("TraceViaCTEScoped: %v", err)
	}
	for _, r := range results {
		if sym, err := store.GetSymbol(r.SymbolID); err == nil && sym != nil && sym.Name == callerName {
			return true
		}
	}
	return false
}

func TestResolveCalls_CrossFileSingletonReceiver_1778(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "store/runner.go", crossFileSingletonRunnerSrc)
	writeFile(t, dir, "store/caller.go", crossFileSingletonCallerSrc)

	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("Index: %v", err)
	}
	pid := db.ProjectIDFromPath(dir)

	execID := methodID(t, store, pid, "execute")
	if !hasInboundCaller(t, store, pid, execID, "DoWork") {
		t.Errorf("DoWork calls defaultRunner.execute across files; expected it as an inbound caller of (*runner).execute (#1778 regression)")
	}
}

// Control: the #1747 same-file path must keep working — declaration and
// call site in one file. Guards against the cross-file fix masking a
// regression in the original pre-pass.
const sameFileSingletonSrc = `package store

type localRunner struct{}

func (r *localRunner) handle(s string) string { return s }

var localSingleton = &localRunner{}

func UseLocal(s string) string {
	return localSingleton.handle(s)
}
`

func TestResolveCalls_SameFileSingletonReceiver_StillResolves_1778(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "store/local.go", sameFileSingletonSrc)

	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("Index: %v", err)
	}
	pid := db.ProjectIDFromPath(dir)

	handleID := methodID(t, store, pid, "handle")
	if !hasInboundCaller(t, store, pid, handleID, "UseLocal") {
		t.Errorf("UseLocal calls localSingleton.handle in the same file; #1747 path should still resolve it")
	}
}

// The extractor must stamp the inferred singleton type into the
// Variable symbol's Signature — that persisted type is what makes the
// resolve-time cross-file recovery possible.
func TestExtractor_SingletonVarSignatureStamped_1778(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "store/runner.go", crossFileSingletonRunnerSrc)

	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("Index: %v", err)
	}
	pid := db.ProjectIDFromPath(dir)

	syms, err := store.GetSymbolsByName(pid, "defaultRunner", 5)
	if err != nil {
		t.Fatalf("GetSymbolsByName defaultRunner: %v", err)
	}
	var v *db.Symbol
	for i := range syms {
		if syms[i].Kind == "Variable" {
			v = &syms[i]
			break
		}
	}
	if v == nil {
		t.Fatalf("expected a Variable symbol for defaultRunner; got %d symbols", len(syms))
	}
	if v.Signature != "*runner" {
		t.Errorf("defaultRunner signature = %q, want %q (inferred singleton type)", v.Signature, "*runner")
	}
}
