// SPDX-License-Identifier: MIT

package index

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// #1772: exercise the full watcher incremental sequence on a 3-file
// call chain U -> R -> X. When X changes, the watcher's
// invalidateReferencers clears the file_hash of X's direct referencer R
// so R re-extracts; R's DeleteSymbolsForFile cascade then deletes the
// edge U -> R (an incoming edge from a file that is NOT re-extracted).
// The #1678 referrer snapshot + #457 pending_edges are supposed to
// re-bind U -> R in the scoped resolve. This test runs the exact
// watcher steps (changedFiles -> invalidateReferencers -> Index) and
// asserts the upstream edge survives.
func TestWatcherSequence_ThreeFileChain_PreservesUpstreamEdge_1772(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("go.mod", "module example.com/chain\n\ngo 1.25\n")
	// Chain: UseR -> RHelper -> XHelper, one function per file.
	write("u.go", "package chain\n\nfunc UseR() int { return RHelper() }\n")
	write("r.go", "package chain\n\nfunc RHelper() int { return XHelper() }\n")
	write("x.go", "package chain\n\nfunc XHelper() int { return 1 }\n")

	dataDir := t.TempDir()
	store, err := db.Open(dataDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()
	idx := New(store)

	summary, err := idx.Index(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("initial Index: %v", err)
	}
	projectID := summary.ProjectID

	// edgeExists reports whether a CALLS edge fromName -> toName is in
	// the graph.
	edgeExists := func(fromName, toName string) bool {
		t.Helper()
		var n int
		if err := store.DB().QueryRow(
			`SELECT COUNT(*) FROM edges e
			   JOIN symbols fs ON fs.project_id=e.project_id AND fs.id=e.from_id
			   JOIN symbols ts ON ts.project_id=e.project_id AND ts.id=e.to_id
			  WHERE e.project_id=? AND e.kind='CALLS' AND fs.name=? AND ts.name=?`,
			projectID, fromName, toName).Scan(&n); err != nil {
			t.Fatalf("edge query: %v", err)
		}
		return n > 0
	}

	if !edgeExists("UseR", "RHelper") || !edgeExists("RHelper", "XHelper") {
		t.Fatalf("initial index: expected UseR->RHelper AND RHelper->XHelper")
	}

	// Edit x.go — change XHelper's body so the content hash differs.
	write("x.go", "package chain\n\nfunc XHelper() int { return 999 }\n")

	// Replicate the watcher's exact incremental sequence.
	p := db.Project{ID: projectID, Name: "chain", Path: dir}
	changed := idx.changedFiles(p)
	idx.invalidateReferencers(p, changed)
	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("incremental Index: %v", err)
	}

	// The edge into the re-extracted referrer must survive.
	if !edgeExists("UseR", "RHelper") {
		t.Errorf("#1772: UseR->RHelper lost after the watcher re-extracted its callee's referrer — " +
			"R's DeleteSymbolsForFile cascade dropped the upstream edge and the scoped resolve did not re-bind it")
	}
	if !edgeExists("RHelper", "XHelper") {
		t.Errorf("#1772: RHelper->XHelper lost after incremental re-index of x.go")
	}
}
