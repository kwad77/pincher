// SPDX-License-Identifier: MIT

package index

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #1772 real-corpus probe. The synthetic harnesses (watcher + graph-
// change layers) all keep the graph whole, so #1772's mechanism needs
// real complex code — closures, interfaces, generics, embedded types,
// build-tag siblings — that trivial fixtures lack.
//
// This probe copies pincher's own internal/ tree into a tempdir,
// indexes it, then runs incremental watcher ticks with semantically-
// inert edits (comment appends). The CALLS edge set must stay
// byte-identical. If it does not, this is the live #1772 reproduction
// on real code.
//
// Gated behind PINCHER_1772_PROBE=1 — it indexes ~hundreds of files and
// is a diagnostic probe, not a fast unit test.

func copyGoTree1772(t *testing.T, srcRoot, dstRoot string) int {
	t.Helper()
	n := 0
	err := filepath.WalkDir(srcRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, relErr := filepath.Rel(srcRoot, path)
		if relErr != nil {
			return relErr
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		dst := filepath.Join(dstRoot, rel)
		if mkErr := os.MkdirAll(filepath.Dir(dst), 0o755); mkErr != nil {
			return mkErr
		}
		if wErr := os.WriteFile(dst, data, 0o644); wErr != nil {
			return wErr
		}
		n++
		return nil
	})
	if err != nil {
		t.Fatalf("copy %s: %v", srcRoot, err)
	}
	return n
}

// TestIncrementalResolve_CALLS_RealCorpus_1772 indexes a real copy of
// pincher's internal/ source and asserts incremental watcher ticks
// (inert edits) never drop a CALLS edge.
func TestIncrementalResolve_CALLS_RealCorpus_1772(t *testing.T) {
	if os.Getenv("PINCHER_1772_PROBE") != "1" {
		t.Skip("set PINCHER_1772_PROBE=1 to run the real-corpus #1772 probe")
	}
	idx, store := newTestIndexer(t)
	dir := t.TempDir()

	// The test runs from internal/index/; repo root is two levels up.
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	internalSrc := filepath.Join(repoRoot, "internal")
	if _, statErr := os.Stat(internalSrc); statErr != nil {
		t.Fatalf("internal/ not found at %s: %v", internalSrc, statErr)
	}

	writeFile(t, dir, "go.mod", "module github.com/kwad77/pincher\n\ngo 1.22\n")
	copied := copyGoTree1772(t, internalSrc, filepath.Join(dir, "internal"))
	t.Logf("copied %d .go files from internal/", copied)

	res, err := idx.Index(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("full index: %v", err)
	}
	projectID := res.ProjectID

	before := snapshotCALLS1772(t, store, projectID)
	t.Logf("baseline CALLS edges: %d", len(before))
	if len(before) < 1000 {
		t.Fatalf("baseline: only %d CALLS edges from real internal/ — extractor broken, probe invalid", len(before))
	}

	// Touch a spread of real files across the tree, one watcher tick at
	// a time. Each touch is an inert comment append.
	var goFiles []string
	_ = filepath.WalkDir(filepath.Join(dir, "internal"), func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".go") {
			goFiles = append(goFiles, path)
		}
		return nil
	})
	if len(goFiles) == 0 {
		t.Fatal("no .go files found in the copied tree")
	}

	for tick := 0; tick < 8; tick++ {
		// Touch ~10 files spread through the tree this tick.
		for j := 0; j < 10; j++ {
			f := goFiles[(tick*10+j)%len(goFiles)]
			data, rErr := os.ReadFile(f)
			if rErr != nil {
				t.Fatalf("read %s: %v", f, rErr)
			}
			if wErr := os.WriteFile(f, append(data, []byte(fmt.Sprintf("\n// #1772 probe touch %d.%d\n", tick, j))...), 0o644); wErr != nil {
				t.Fatalf("touch %s: %v", f, wErr)
			}
		}
		watcherTick1772(t, idx, store, projectID, dir)
		after := snapshotCALLS1772(t, store, projectID)
		assertCALLSIdentical1772(t, fmt.Sprintf("real-corpus tick %d", tick), before, after)
		t.Logf("tick %d: %d CALLS edges (baseline %d)", tick, len(after), len(before))
	}

	// Final cross-check: a force reindex must agree with the running
	// incremental graph. A divergence here is the #1772 signature.
	if _, err := idx.Index(context.Background(), dir, true); err != nil {
		t.Fatalf("final force reindex: %v", err)
	}
	forced := snapshotCALLS1772(t, store, projectID)
	t.Logf("post-force CALLS edges: %d", len(forced))
	assertCALLSIdentical1772(t, "force vs incremental graph", before, forced)
}
