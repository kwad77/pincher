// SPDX-License-Identifier: MIT

package index

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTomlFixture lays out a tiny project with a Cargo.toml and a
// pyproject.toml so the indexer has something representative to chew
// through. Returns the project root path.
func writeTomlFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	mustWrite(t, filepath.Join(root, "Cargo.toml"), `[package]
name = "demo"
version = "1.2.3"

[dependencies]
serde = "1.0"
tokio = { version = "1", features = ["full"] }

[[bin]]
name = "server"
path = "src/main.rs"

[[bin]]
name = "worker"
path = "src/worker.rs"
`)

	mustWrite(t, filepath.Join(root, "pyproject.toml"), `[project]
name = "pkg"
version = "0.1.0"
description = """
Long
description.
"""
dependencies = [
  "click",
  "rich",
]

[tool.ruff]
line-length = 100
`)

	return root
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestTomlIntegration_IndexExtractsExpectedSymbols verifies the
// end-to-end path: walk → ast.Extract → DB persistence. Symbol IDs
// users would search for (Cargo dependencies, pyproject sections) MUST
// be retrievable from the DB by ID after the indexer finishes.
func TestTomlIntegration_IndexExtractsExpectedSymbols(t *testing.T) {
	root := writeTomlFixture(t)
	_, store, _ := indexFixture(t, root, true)

	wantIDs := []string{
		"Cargo.toml::package#Setting",
		"Cargo.toml::package.name#Setting",
		"Cargo.toml::package.version#Setting",
		"Cargo.toml::dependencies.serde#Setting",
		"Cargo.toml::dependencies.tokio#Setting",
		"Cargo.toml::bin.0.name#Setting",
		"Cargo.toml::bin.1.name#Setting",
		"pyproject.toml::project.name#Setting",
		"pyproject.toml::project.description#Setting",
		"pyproject.toml::project.dependencies#Setting",
		"pyproject.toml::tool.ruff.line-length#Setting",
	}
	for _, id := range wantIDs {
		sym, err := store.GetSymbol(id)
		if err != nil {
			t.Errorf("GetSymbol(%q): %v", id, err)
			continue
		}
		if sym == nil {
			t.Errorf("symbol %q not found in DB", id)
			continue
		}
		if sym.Language != "TOML" {
			t.Errorf("symbol %q language = %q, want TOML", id, sym.Language)
		}
		// Per #34 Phase 2 (PR #107) extraction_confidence is composed at
		// extract time, no longer a per-language constant. Setting
		// symbols compose to (BaseExtractor 1.0 + KindBaseline) / 2 ±
		// path/content penalties → typically 0.95–1.0 for clean TOML
		// configs. The invariant that matters: the composed score stays
		// above the default `min_confidence` threshold of 0.7.
		if sym.ExtractionConfidence < 0.7 {
			t.Errorf("symbol %q extraction_confidence = %v, want >= 0.7 (parser-backed should compose to high)", id, sym.ExtractionConfidence)
		}
	}
}

// TestTomlIntegration_ConfigCorpusRouting — TOML Settings MUST land
// in the `config` corpus FTS5 index, not `code` or `docs`. This is
// the parity gate from internal/db/corpus.go: ClassifyCorpus("TOML",
// "Setting") must return "config" so search with corpus=config picks
// up the symbols.
func TestTomlIntegration_ConfigCorpusRouting(t *testing.T) {
	root := writeTomlFixture(t)
	_, store, projectID := indexFixture(t, root, true)

	// A user searching "tokio" inside the config corpus should find
	// dependencies.tokio. Searching the code corpus (default) should
	// NOT find it (TOML is not code).
	configHits, err := store.SearchSymbolsByCorpus(projectID, "tokio", "", "", "config", 20)
	if err != nil {
		t.Fatalf("config search: %v", err)
	}
	if len(configHits) == 0 {
		t.Error("config corpus did not return TOML Setting matches for `tokio`")
	}

	codeHits, err := store.SearchSymbolsByCorpus(projectID, "tokio", "", "", "code", 20)
	if err != nil {
		t.Fatalf("code search: %v", err)
	}
	for _, h := range codeHits {
		if h.Symbol.Language == "TOML" {
			t.Errorf("TOML symbol %q leaked into code corpus", h.Symbol.QualifiedName)
		}
	}
}

// TestTomlIntegration_StaleSymbolCleanupOnEdit — when a key is
// removed from a TOML file and the file is re-parsed, the orphaned
// symbol row MUST be deleted. Mirrors the HCL stale-symbol-cleanup
// pattern (LATENT #15 fix); same indexer DELETE-then-extract path
// applies to all extractors.
func TestTomlIntegration_StaleSymbolCleanupOnEdit(t *testing.T) {
	root := writeTomlFixture(t)
	idx, store, _ := indexFixture(t, root, true)

	// `dependencies.tokio` must exist before the edit.
	id := "Cargo.toml::dependencies.tokio#Setting"
	sym, err := store.GetSymbol(id)
	if err != nil {
		t.Fatalf("GetSymbol pre-edit: %v", err)
	}
	if sym == nil {
		t.Fatalf("expected %s pre-edit, got nil", id)
	}

	// Remove the tokio dependency line and re-write Cargo.toml.
	cargoPath := filepath.Join(root, "Cargo.toml")
	original, err := os.ReadFile(cargoPath)
	if err != nil {
		t.Fatalf("read Cargo.toml: %v", err)
	}
	edited := strings.Replace(string(original),
		`tokio = { version = "1", features = ["full"] }`+"\n", "", 1)
	if edited == string(original) {
		t.Fatal("edit did not change Cargo.toml — fixture changed?")
	}
	if err := os.WriteFile(cargoPath, []byte(edited), 0o644); err != nil {
		t.Fatalf("write edited Cargo.toml: %v", err)
	}
	if _, err := idx.Index(context.Background(), root, false); err != nil {
		t.Fatalf("re-index after edit: %v", err)
	}

	// Orphan must be gone.
	sym, err = store.GetSymbol(id)
	if err != nil {
		t.Fatalf("GetSymbol post-edit: %v", err)
	}
	if sym != nil {
		t.Errorf("stale symbol %s still present after re-index — DeleteSymbolsForFile not called?", id)
	}

	// Sibling keys MUST still exist.
	if sym, err := store.GetSymbol("Cargo.toml::dependencies.serde#Setting"); err != nil || sym == nil {
		t.Errorf("expected dependencies.serde to still exist after edit (err=%v sym=%v)", err, sym)
	}
}
