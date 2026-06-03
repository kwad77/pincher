// SPDX-License-Identifier: MIT

package index

import (
	"context"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// #750: name-fallback call/binding resolution must not bind an edge
// from real project code into an isolated testdata-fixture corpus.
//
// Repro shape (deterministic — the fixture path sorts BEFORE the real
// one, so pickCanonical picked it pre-fix):
//   app/run.go     : package app — func Run() { Widget() }
//   app/widget.go  : package app — func Widget() {}            (real)
//   __fixtures__/sample/widget.go : package sample — func Widget() {} (fixture)
//
// Run's bare same-package call to Widget() defers to resolveCalls,
// where lookupQN("Widget") fails and lookupName("Widget") sees both
// app.Widget and sample.Widget. Pre-fix pickCanonical picked the
// lex-smallest ID — `__fixtures__/...` < `app/...` — so Run bound to
// the fixture. Post-fix preferNonFixtureSyms drops the fixture.
func TestIndex_NameFallback_DoesNotBindIntoFixtureCorpus(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "app/run.go", "package app\n\nfunc Run() {\n\tWidget()\n}\n")
	writeFile(t, dir, "app/widget.go", "package app\n\nfunc Widget() {}\n")
	writeFile(t, dir, "__fixtures__/sample/widget.go", "package sample\n\nfunc Widget() {}\n")

	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("Index: %v", err)
	}

	runID := db.MakeSymbolID("app/run.go", "app.Run", "Function")
	realWidgetID := db.MakeSymbolID("app/widget.go", "app.Widget", "Function")
	fixtureWidgetID := db.MakeSymbolID("__fixtures__/sample/widget.go", "sample.Widget", "Function")

	// The real intra-package edge must resolve.
	inboundReal, err := store.EdgesTo(realWidgetID, nil)
	if err != nil {
		t.Fatalf("EdgesTo real Widget: %v", err)
	}
	if !hasEdge(inboundReal, runID, "CALLS") {
		t.Errorf("expected CALLS edge Run → app.Widget (real same-package target):\n  inbound: %v", inboundReal)
	}

	// The fixture symbol must NOT be a call target of real code.
	inboundFixture, err := store.EdgesTo(fixtureWidgetID, nil)
	if err != nil {
		t.Fatalf("EdgesTo fixture Widget: %v", err)
	}
	if hasEdge(inboundFixture, runID, "CALLS") {
		t.Errorf("Run bound a CALLS edge into the __fixtures__/ corpus — name-fallback must not cross the fixture boundary:\n  inbound: %v", inboundFixture)
	}
}

func TestIsFixturePath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"testdata/corpus/go-project/internal/auth/auth.go", true},
		{"internal/db/db.go", false},
		{"cmd/pinch/main.go", false},
		{"pkg/testdata/sample.go", true},
		{"__fixtures__/sample/widget.go", true},
		{"pkg/__fixtures__/x.go", true},
		{"internal/fixtures/seed.go", true},
		{"internal\\server\\testdata\\tool-contract.json", true}, // windows separators
		{"src/test_fixtures/a.go", true},
		{"src/test-fixtures/a.go", true},
		{"internal/index/indexer.go", false},
		// "fixtures" only matches as a path segment, not a substring.
		{"internal/fixturesloader/load.go", false},
	}
	for _, c := range cases {
		if got := isFixturePath(c.path); got != c.want {
			t.Errorf("isFixturePath(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestPreferNonFixtureSyms(t *testing.T) {
	real1 := db.Symbol{ID: "a", FilePath: "internal/db/db.go"}
	real2 := db.Symbol{ID: "b", FilePath: "cmd/pinch/main.go"}
	fix1 := db.Symbol{ID: "c", FilePath: "testdata/corpus/x/auth.go"}
	fix2 := db.Symbol{ID: "d", FilePath: "__fixtures__/y/w.go"}

	// Mixed set → fixtures dropped.
	got := preferNonFixtureSyms([]db.Symbol{real1, fix1, real2, fix2})
	if len(got) != 2 {
		t.Fatalf("mixed set: got %d syms, want 2 (fixtures dropped)", len(got))
	}
	for _, s := range got {
		if isFixturePath(s.FilePath) {
			t.Errorf("fixture %q survived the filter", s.FilePath)
		}
	}

	// All-fixture set → original returned (intra-fixture resolution still works).
	allFix := []db.Symbol{fix1, fix2}
	got = preferNonFixtureSyms(allFix)
	if len(got) != 2 {
		t.Errorf("all-fixture set: got %d syms, want 2 (original kept as fallback)", len(got))
	}

	// All-real set → unchanged.
	got = preferNonFixtureSyms([]db.Symbol{real1, real2})
	if len(got) != 2 {
		t.Errorf("all-real set: got %d syms, want 2", len(got))
	}
}
