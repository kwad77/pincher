package index

import (
	"context"
	"testing"
)

// #1859: design-rationale comments survive the full index pass —
// extracted, persisted, and searchable — and route to the code corpus
// (no schema change; the docs-corpus split is a tracked follow-up).
func TestIndex_RationaleSymbols_PersistedAndSearchable(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "login.go", `package demo

func Login() error {
	// HACK: skip TLS verification until the staging cert is reissued.
	return nil
}
`)
	res, err := idx.Index(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("index: %v", err)
	}

	// Persisted as a Rationale symbol in the file.
	syms, err := store.GetSymbolsForFile(res.ProjectID, "login.go")
	if err != nil {
		t.Fatalf("GetSymbolsForFile: %v", err)
	}
	var rationale *struct{ name, parent string }
	for _, s := range syms {
		if s.Kind == "Rationale" {
			rationale = &struct{ name, parent string }{s.Name, s.Parent}
		}
	}
	if rationale == nil {
		t.Fatalf("no Rationale symbol persisted; file symbols: %+v", syms)
	}
	if rationale.parent != "demo.Login" {
		t.Errorf("Rationale parent = %q, want demo.Login", rationale.parent)
	}

	// Searchable: BM25 over the code corpus finds it by a body word.
	hits, err := store.SearchSymbolsByCorpus(res.ProjectID, "TLS", "", "", "code", 10)
	if err != nil {
		t.Fatalf("SearchSymbolsByCorpus: %v", err)
	}
	found := false
	for _, h := range hits {
		if h.Symbol.Kind == "Rationale" {
			found = true
		}
	}
	if !found {
		t.Errorf("Rationale symbol not found by search for a word in its body; got %d hits", len(hits))
	}
}
