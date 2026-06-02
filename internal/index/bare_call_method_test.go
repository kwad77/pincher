package index

import (
	"context"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// #754: resolveCalls's bare-name fallback (lookupName) is only reached
// for unqualified calls like `Process()`. A bare call in Go is never a
// method invocation — methods require a receiver. When a project has
// both `func Process()` and `(x).Process()` and the method's symbol ID
// sorts first, pickCanonical over the mixed set picked the METHOD,
// funnelling the bare call onto it and starving the real function.
//
// Repro shape (deterministic — the method's file sorts before the
// function's, so pickCanonical picked the method pre-fix):
//
//	proc/aaa_worker.go : package proc — func (w *worker) Process() {}
//	proc/fn.go         : package proc — func Process() {}            (real target)
//	proc/run.go        : package proc — func Caller() { Process() }
func TestIndex_BareCall_ResolvesToFunctionNotMethod(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "proc/aaa_worker.go", "package proc\n\ntype worker struct{}\n\nfunc (w *worker) Process() {}\n")
	writeFile(t, dir, "proc/fn.go", "package proc\n\nfunc Process() {}\n")
	writeFile(t, dir, "proc/run.go", "package proc\n\nfunc Caller() {\n\tProcess()\n}\n")

	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("Index: %v", err)
	}

	callerID := db.MakeSymbolID("proc/run.go", "proc.Caller", "Function")
	funcID := db.MakeSymbolID("proc/fn.go", "proc.Process", "Function")
	methodID := db.MakeSymbolID("proc/aaa_worker.go", "proc.*worker.Process", "Method")

	// The bare call must resolve to the package-level Function.
	inboundFunc, err := store.EdgesTo(funcID, nil)
	if err != nil {
		t.Fatalf("EdgesTo func Process: %v", err)
	}
	if !hasEdge(inboundFunc, callerID, "CALLS") {
		t.Errorf("expected CALLS edge Caller → proc.Process (the Function — a bare call can't be a method):\n  inbound: %v", inboundFunc)
	}

	// It must NOT resolve to the same-named Method.
	inboundMethod, err := store.EdgesTo(methodID, nil)
	if err != nil {
		t.Fatalf("EdgesTo method Process: %v", err)
	}
	if hasEdge(inboundMethod, callerID, "CALLS") {
		t.Errorf("bare call Process() false-bound to a Method — methods need a receiver:\n  inbound: %v", inboundMethod)
	}
}

func TestIndex_BareCall_DoesNotBindAcrossGoPackageDirs(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "internal/job/job.go", `package job

func Caller(run func() int) int {
	return run()
}
`)
	writeFile(t, dir, "cmd/tool/main.go", `package main

func run() int {
	return 0
}
`)

	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("Index: %v", err)
	}

	callerID := db.MakeSymbolID("internal/job/job.go", "job.Caller", "Function")
	cmdRunID := db.MakeSymbolID("cmd/tool/main.go", "main.run", "Function")
	inbound, err := store.EdgesTo(cmdRunID, nil)
	if err != nil {
		t.Fatalf("EdgesTo cmd run: %v", err)
	}
	if hasEdge(inbound, callerID, "CALLS") {
		t.Errorf("local function value run() false-bound to cmd/tool run():\n  inbound: %v", inbound)
	}
}

func TestExcludeMethodSyms(t *testing.T) {
	fn1 := db.Symbol{ID: "f1", Kind: "Function"}
	m1 := db.Symbol{ID: "m1", Kind: "Method"}
	v1 := db.Symbol{ID: "v1", Kind: "Variable"}
	m2 := db.Symbol{ID: "m2", Kind: "Method"}

	got := excludeMethodSyms([]db.Symbol{m1, fn1, m2, v1})
	if len(got) != 2 {
		t.Fatalf("got %d syms, want 2 (both methods dropped)", len(got))
	}
	for _, s := range got {
		if s.Kind == "Method" {
			t.Errorf("Method %q survived excludeMethodSyms", s.ID)
		}
	}

	// All-method set → empty (caller must treat as unresolved, not fall back).
	if got := excludeMethodSyms([]db.Symbol{m1, m2}); len(got) != 0 {
		t.Errorf("all-method set: got %d, want 0 (no fallback to a method)", len(got))
	}

	// No methods → unchanged.
	if got := excludeMethodSyms([]db.Symbol{fn1, v1}); len(got) != 2 {
		t.Errorf("no-method set: got %d, want 2", len(got))
	}
}
