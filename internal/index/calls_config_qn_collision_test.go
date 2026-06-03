// SPDX-License-Identifier: MIT

package index

import (
	"context"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// #762: a bare Go call name can collide with a config-file key whose QN
// is literally that string. `resolveCalls`'s `lookupQN("build")` matched
// the cross-language JSON `build#Setting`, and `pickCanonical` (lex-
// smallest ID, no kind/language filter) resolved the call to the Setting
// — emitting a false `Go func -> Setting` CALLS edge and starving the
// real `func build()` of its inbound edge. resolveCalls has no
// #436-style language guard, so `preferCodeSyms` drops non-code
// candidates from the QN lookup instead.
//
// The call must be CROSS-FILE: a same-file call resolves during per-file
// extraction and never reaches resolveCalls. `func build()` lives in
// build.go; `Run()` calls it from run.go — that deferred edge is what
// resolveCalls resolves, and `lookupQN("build")` hits the Setting
// because the function's QN is `zpkg.build`, not `build`.
func TestIndex_CallEdge_BareNameDoesNotBindToConfigKey(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "zpkg/build.go",
		"package zpkg\n\nfunc build() {}\n")
	writeFile(t, dir, "zpkg/run.go",
		"package zpkg\n\nfunc Run() {\n\tbuild()\n}\n")
	// Top-level JSON key `build` extracts as a Setting with QN "build".
	writeFile(t, dir, "a_config.json",
		"{\n  \"name\": \"demo\",\n  \"build\": \"go build\"\n}\n")

	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("Index: %v", err)
	}

	pid := db.ProjectIDFromPath(dir)
	runID := db.MakeSymbolID("zpkg/run.go", "zpkg.Run", "Function")
	buildFnID := db.MakeSymbolID("zpkg/build.go", "zpkg.build", "Function")

	// The cross-file call must resolve to the Go function.
	inboundFn, err := store.EdgesTo(buildFnID, nil)
	if err != nil {
		t.Fatalf("EdgesTo zpkg.build: %v", err)
	}
	if !hasEdge(inboundFn, runID, "CALLS") {
		t.Errorf("expected CALLS edge Run -> zpkg.build (the Go function):\n  inbound: %v", inboundFn)
	}

	// And must NOT false-bind to the a_config.json `build` Setting.
	syms, err := store.GetSymbolsByName(pid, "build", 10)
	if err != nil {
		t.Fatalf("GetSymbolsByName build: %v", err)
	}
	for _, s := range syms {
		if s.Kind != "Setting" {
			continue
		}
		inbound, err := store.EdgesTo(s.ID, nil)
		if err != nil {
			t.Fatalf("EdgesTo %s: %v", s.ID, err)
		}
		if hasEdge(inbound, runID, "CALLS") {
			t.Errorf("build() call false-bound to config key %s — a CALLS target is never a Setting", s.ID)
		}
	}
}
