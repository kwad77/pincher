// SPDX-License-Identifier: MIT

package index

import (
	"context"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// .pincherignore — user-controlled ignore file with gitignore semantics,
// applied at the walker level via gocodewalker's CustomIgnore. Motivation:
// a real project's data/*.json model artifacts (2.5 MB each, under the
// 4 MB per-file cap) became 27,672 junk Setting symbols — 96% of the
// project — with no user-facing way to exclude them.

// symbolPathSet indexes ListSymbolFilePaths into a membership set.
func symbolPathSet(t *testing.T, store *db.Store, projectID string) map[string]bool {
	t.Helper()
	paths, err := store.ListSymbolFilePaths(projectID)
	if err != nil {
		t.Fatalf("ListSymbolFilePaths: %v", err)
	}
	set := map[string]bool{}
	for _, p := range paths {
		set[p] = true
	}
	return set
}

// TestIndex_PincherIgnore_Patterns verifies the core gitignore-syntax
// surface in one fixture: comments and blank lines are inert, `dir/`
// ignores a directory subtree, `*` globs match by basename anywhere,
// a leading `/` anchors to the project root (a same-named nested dir
// survives), and `!` negation re-includes a previously-excluded file.
func TestIndex_PincherIgnore_Patterns(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()

	writeFile(t, dir, ".pincherignore", `# model artifacts — 27k junk Setting symbols without this

data/
*.skipme.go
/gen
*.json
!keep.json
`)

	// Should index.
	writeFile(t, dir, "main.go", "package demo\nfunc Main() {}\n")
	writeFile(t, dir, "nested/gen/keep.go", "package gen\nfunc Keep() {}\n") // anchored /gen only matches root
	writeFile(t, dir, "keep.json", `{"keep": {"me": true}}`)                 // negation re-includes

	// Should be ignored.
	writeFile(t, dir, "data/model.json", `{"weights": {"layer1": 0.5, "layer2": 0.25}}`)
	writeFile(t, dir, "data/sub/model2.json", `{"weights": {"layer1": 0.5}}`)
	writeFile(t, dir, "util.skipme.go", "package demo\nfunc Skipped() {}\n")
	writeFile(t, dir, "gen/gen.go", "package gen\nfunc Generated() {}\n")

	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("Index: %v", err)
	}

	pid := db.ProjectIDFromPath(dir)
	got := symbolPathSet(t, store, pid)

	for _, want := range []string{"main.go", "nested/gen/keep.go", "keep.json"} {
		if !got[want] {
			t.Errorf("expected symbols from %s; symbol file paths: %v", want, got)
		}
	}
	for _, banned := range []string{"data/model.json", "data/sub/model2.json", "util.skipme.go", "gen/gen.go"} {
		if got[banned] {
			t.Errorf(".pincherignore should have excluded %s; symbol file paths: %v", banned, got)
		}
	}
}

// TestIndex_PincherIgnore_GCOnNewlyIgnored proves the GC contract:
// because walker-filtered files never enter the seen-files bookkeeping,
// the #326 tail-pass GC deletes previously-indexed symbols (and file
// hashes) of files that a NEWLY-added .pincherignore rule excludes —
// on an ordinary (non-force) re-index. This is the "add the rule,
// re-index, done" remediation the doctor settings_flood advisory
// promises.
func TestIndex_PincherIgnore_GCOnNewlyIgnored(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()

	writeFile(t, dir, "main.go", "package demo\nfunc Main() {}\n")
	writeFile(t, dir, "data/model.json", `{"weights": {"layer1": 0.5, "layer2": 0.25}}`)

	// Pass 1: no ignore file — the artifact indexes.
	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("Index pass 1: %v", err)
	}
	pid := db.ProjectIDFromPath(dir)
	got := symbolPathSet(t, store, pid)
	if !got["data/model.json"] {
		t.Fatalf("precondition failed: data/model.json should index without .pincherignore; symbol file paths: %v", got)
	}

	// Pass 2: user adds the ignore rule, then an ordinary re-index.
	writeFile(t, dir, ".pincherignore", "data/\n")
	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("Index pass 2: %v", err)
	}

	got = symbolPathSet(t, store, pid)
	if got["data/model.json"] {
		t.Errorf("symbols of newly-ignored data/model.json must be GC'd on re-index; symbol file paths: %v", got)
	}
	if !got["main.go"] {
		t.Errorf("main.go symbols must survive the GC pass; symbol file paths: %v", got)
	}
	if h := store.GetFileHash(pid, "data/model.json"); h != "" {
		t.Errorf("file hash of newly-ignored file must be GC'd; got %q", h)
	}
}

// TestIndex_PincherIgnore_MissingFileNoop pins the no-op default: with
// no .pincherignore present, nothing is filtered by the feature — the
// data dir that the other tests exclude indexes normally.
func TestIndex_PincherIgnore_MissingFileNoop(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()

	writeFile(t, dir, "main.go", "package demo\nfunc Main() {}\n")
	writeFile(t, dir, "data/model.json", `{"weights": {"layer1": 0.5}}`)

	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("Index: %v", err)
	}

	pid := db.ProjectIDFromPath(dir)
	got := symbolPathSet(t, store, pid)
	for _, want := range []string{"main.go", "data/model.json"} {
		if !got[want] {
			t.Errorf("without .pincherignore, %s should index; symbol file paths: %v", want, got)
		}
	}
}
