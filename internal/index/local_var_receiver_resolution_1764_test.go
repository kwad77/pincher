// SPDX-License-Identifier: MIT

package index

import (
	"context"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// #1764: a method called through a local-variable receiver
// (`w := &Worker{}; w.Run()`) dropped its CALLS edge — `extractGoCalls`
// stamped ReceiverType only from the enclosing method's receiver and
// #1747's pre-pass covered package-level vars only, so a local-var
// receiver carried no hint and `resolveByReceiverType` could not bind
// it. The Go extractor now runs a function-body local-var type pre-pass
// and `resolveByReceiverType` case-2 tolerates the pointer/value
// receiver-form mismatch.

const localVarReceiverSrc = `package svc

type Worker struct{}

func (w *Worker) Run() int { return 1 }

func processPtr() int {
	w := &Worker{}
	return w.Run()
}

func processVal() int {
	w := Worker{}
	return w.Run()
}

func processVarDecl() int {
	var w Worker
	return w.Run()
}

func processVarAssign() int {
	var w = &Worker{}
	return w.Run()
}
`

func TestResolveCalls_LocalVarReceiver_1764(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "svc/worker.go", localVarReceiverSrc)

	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("Index: %v", err)
	}
	pid := db.ProjectIDFromPath(dir)

	runID := methodID(t, store, pid, "Run")

	// All four local-var declaration shapes must produce a resolved
	// CALLS edge into (*Worker).Run. processVal / processVarDecl use a
	// VALUE-typed local calling a pointer-receiver method — those
	// exercise the case-2 pointer/value tolerance.
	for _, caller := range []string{"processPtr", "processVal", "processVarDecl", "processVarAssign"} {
		if !hasInboundCaller(t, store, pid, runID, caller) {
			t.Errorf("#1764: %s calls w.Run() through a local-var receiver — expected a resolved CALLS edge into (*Worker).Run", caller)
		}
	}
}
