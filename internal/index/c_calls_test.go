// SPDX-License-Identifier: MIT

package index

import (
	"context"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// #858: regex-tier languages produced a zero-edge graph — trace /
// dead_code were silent no-ops on C / TS / etc. The regex extractor now
// runs a per-file CALLS pass for C (opts.extractCalls): each function
// body is scanned for `name(` call sites and CALLS edges are emitted.
// Same-file targets resolve; everything else (keywords, cross-file
// names, undefined symbols) drops at the per-file resolver.
//
// This test pins that a C file gets real CALLS edges and that the
// obvious false positives stay out:
//   - `if (...)` is a keyword, not a call → no edge
//   - `missing(...)` has no symbol in the file → dropped at resolve
//   - the function's own signature line doesn't self-match
func TestIndex_CCallsResolveSameFile(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "src/calc.c", `static int helper(int x) {
    return x + 1;
}

int compute(int n) {
    int a = helper(n);
    if (a > 0) {
        return helper(a);
    }
    return missing(a);
}

int main(void) {
    return compute(41);
}
`)
	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("Index: %v", err)
	}
	pid := db.ProjectIDFromPath(dir)

	idByName := func(name string) string {
		t.Helper()
		syms, err := store.GetSymbolsByName(pid, name, 5)
		if err != nil {
			t.Fatalf("GetSymbolsByName %s: %v", name, err)
		}
		for _, s := range syms {
			if s.Language == "C" && s.Name == name {
				return s.ID
			}
		}
		t.Fatalf("expected a C symbol named %q, got %d candidates", name, len(syms))
		return ""
	}
	computeID := idByName("compute")
	helperID := idByName("helper")
	mainID := idByName("main")

	// compute() calls helper() twice and missing() once. helper resolves
	// (same file, real symbol); missing doesn't (no symbol) and `if`
	// is a keyword — both drop. Expect exactly one CALLS edge: → helper.
	computeEdges, err := store.EdgesFrom(computeID, []string{"CALLS"})
	if err != nil {
		t.Fatalf("EdgesFrom compute: %v", err)
	}
	var toHelper int
	for _, e := range computeEdges {
		if e.ToID == helperID {
			toHelper++
		}
	}
	if toHelper != 1 {
		t.Errorf("compute should have exactly 1 CALLS edge to helper (deduped); got %d. all edges=%+v", toHelper, computeEdges)
	}
	if len(computeEdges) != 1 {
		t.Errorf("compute should have exactly 1 outbound CALLS edge — `if` is a keyword and `missing` has no symbol, both must drop; got %d: %+v", len(computeEdges), computeEdges)
	}

	// main() calls compute().
	mainEdges, err := store.EdgesFrom(mainID, []string{"CALLS"})
	if err != nil {
		t.Fatalf("EdgesFrom main: %v", err)
	}
	var mainToCompute bool
	for _, e := range mainEdges {
		if e.ToID == computeID {
			mainToCompute = true
		}
	}
	if !mainToCompute {
		t.Errorf("expected CALLS edge main→compute; got %+v", mainEdges)
	}

	// helper()'s body has no call sites — no outbound CALLS edges.
	helperEdges, err := store.EdgesFrom(helperID, []string{"CALLS"})
	if err != nil {
		t.Fatalf("EdgesFrom helper: %v", err)
	}
	if len(helperEdges) != 0 {
		t.Errorf("helper has no call sites in its body; expected 0 outbound CALLS, got %+v", helperEdges)
	}
}
