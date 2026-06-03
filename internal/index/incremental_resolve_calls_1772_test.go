// SPDX-License-Identifier: MIT

package index

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// #1772: a referrer file pulled into the incremental resolve scope —
// because it CALLS a symbol in the edited file — must keep ALL its
// CALLS edges, including same-file ones unrelated to the edit.
//
// The incremental tick scoped-deletes the referrer's resolve_pass
// CALLS edges (to recreate its edge into the re-extracted callee) and
// re-resolves from the referrer's persisted pending_edges. If that
// re-resolve produces fewer edges than the file had, the referrer
// silently loses CALLS edges until a force-reindex.
func TestIndex_IncrementalResolve_PreservesReferrerCallsEdges_1772(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("go.mod", "module example.com/p\n\ngo 1.25\n")
	// a.go defines Helper. b.go's B calls Helper (cross-file — makes b
	// a referrer of a) AND Sibling (same-file within b).
	write("a.go", "package p\n\nfunc Helper() int { return 1 }\n")
	write("b.go", "package p\n\nfunc B() int { return Helper() + Sibling() }\n\nfunc Sibling() int { return 2 }\n")

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

	// CALLS edges out of b.go's B function, keyed by callee name.
	bCallees := func(t *testing.T) map[string]bool {
		t.Helper()
		rows, err := store.DB().Query(
			`SELECT ts.name FROM edges e
			   JOIN symbols fs ON fs.project_id=e.project_id AND fs.id=e.from_id
			   JOIN symbols ts ON ts.project_id=e.project_id AND ts.id=e.to_id
			  WHERE e.project_id=? AND e.kind='CALLS' AND fs.name='B'`, projectID)
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		defer rows.Close()
		got := map[string]bool{}
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err != nil {
				t.Fatalf("scan: %v", err)
			}
			got[n] = true
		}
		return got
	}

	pre := bCallees(t)
	if !pre["Helper"] || !pre["Sibling"] {
		t.Fatalf("initial index: B should call Helper AND Sibling; got %v", pre)
	}

	// Edit a.go only. a.go re-extracts; b.go becomes a referrer (B
	// calls Helper in a.go) and is pulled into the scoped resolve.
	write("a.go", "package p\n\nfunc Helper() int { return 100 }\n")
	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("incremental Index: %v", err)
	}

	post := bCallees(t)
	if !post["Helper"] {
		t.Errorf("#1772: B→Helper lost after incremental re-index — the referrer's edge into the edited file was not restored")
	}
	if !post["Sibling"] {
		t.Errorf("#1772: B→Sibling (same-file edge) lost after incremental re-index — " +
			"the referrer-scope DELETE wiped b.go's resolve_pass CALLS and the re-resolve did not restore them all")
	}
}

// countResolvePassCalls returns the project's resolve_pass CALLS edge count.
func countResolvePassCalls(t *testing.T, store *db.Store, projectID string) int {
	t.Helper()
	var n int
	if err := store.DB().QueryRow(
		`SELECT COUNT(*) FROM edges WHERE project_id=? AND kind='CALLS' AND source='resolve_pass'`,
		projectID,
	).Scan(&n); err != nil {
		t.Fatalf("count CALLS: %v", err)
	}
	return n
}

// TestResolveOnly_RestoresDroppedCallsEdges_1772 is the #1772 self-heal:
// ResolveOnly re-resolves the full persisted pending_edges pool, with no
// re-extraction, restoring CALLS edges that an incremental tick dropped.
// The test simulates the degradation directly — deletes resolve_pass
// CALLS edges from the DB — so it verifies the *heal* regardless of the
// exact incremental-resolve drop mechanism.
func TestResolveOnly_RestoresDroppedCallsEdges_1772(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("go.mod", "module example.com/h\n\ngo 1.25\n")
	// a.go defines three helpers; b.go's B calls all three — cross-file
	// calls are resolved by the project-wide resolve pass, so each is a
	// resolve_pass CALLS edge the self-heal must restore.
	write("a.go", "package h\n\nfunc HelperOne() int { return 1 }\nfunc HelperTwo() int { return 2 }\nfunc HelperThree() int { return 3 }\n")
	write("b.go", "package h\n\nfunc B() int { return HelperOne() + HelperTwo() + HelperThree() }\n")

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

	pre := countResolvePassCalls(t, store, projectID)
	if pre < 3 {
		t.Fatalf("baseline: expected >=3 resolve_pass CALLS edges (B→HelperOne/Two/Three); got %d", pre)
	}

	// Simulate the #1772 degradation: drop every resolve_pass CALLS edge.
	if _, err := store.DB().Exec(
		`DELETE FROM edges WHERE project_id=? AND kind='CALLS' AND source='resolve_pass'`,
		projectID); err != nil {
		t.Fatalf("simulate edge drop: %v", err)
	}
	if mid := countResolvePassCalls(t, store, projectID); mid != 0 {
		t.Fatalf("after simulated drop: expected 0 CALLS edges; got %d", mid)
	}

	// The self-heal: ResolveOnly re-resolves the full pending_edges pool.
	if _, err := idx.ResolveOnly(context.Background(), dir); err != nil {
		t.Fatalf("ResolveOnly: %v", err)
	}

	post := countResolvePassCalls(t, store, projectID)
	if post != pre {
		t.Errorf("#1772 self-heal failed: ResolveOnly restored %d resolve_pass CALLS edges, want %d (the full pre-degradation count)", post, pre)
	}
}

// TestResolveOnly_CleanGraphIdempotent_1772 — running ResolveOnly on an
// already-correct graph must not change the edge count (no duplicates,
// no drops). The self-heal runs on every active→idle settle, often over
// a graph that is already whole; it must be a safe no-op there.
func TestResolveOnly_CleanGraphIdempotent_1772(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("go.mod", "module example.com/i\n\ngo 1.25\n")
	write("a.go", "package i\n\nfunc Helper() int { return 1 }\n")
	write("b.go", "package i\n\nfunc B() int { return Helper() + Sibling() }\n\nfunc Sibling() int { return 2 }\n")

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
	before := countResolvePassCalls(t, store, projectID)

	for i := 0; i < 3; i++ {
		if _, err := idx.ResolveOnly(context.Background(), dir); err != nil {
			t.Fatalf("ResolveOnly pass %d: %v", i, err)
		}
	}
	if after := countResolvePassCalls(t, store, projectID); after != before {
		t.Errorf("ResolveOnly is not idempotent on a clean graph: %d edges before, %d after 3 passes", before, after)
	}
}
