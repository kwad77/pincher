// SPDX-License-Identifier: MIT

package db

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// seedProject1772 inserts the projects row that edges.project_id's
// foreign key requires before any edge can be written.
func seedProject1772(t *testing.T, store *Store, projectID string) {
	t.Helper()
	if err := store.UpsertProject(Project{
		ID:        projectID,
		Path:      "/tmp/" + projectID,
		Name:      projectID,
		IndexedAt: time.Now(),
	}); err != nil {
		t.Fatalf("UpsertProject(%s): %v", projectID, err)
	}
}

// #1772: the resolve passes replaced their edge set with a
// DeleteEdgesByKindAndSource commit followed by a separate
// BulkUpsertEdges commit. Between the two, the committed DB held zero
// resolve_pass edges of that kind — a concurrent MCP read landing in
// the window saw cross-file CALLS edges vanish (the
// graph-degrades-over-a-session symptom).
//
// ReplaceEdgesByKindSource does the delete and the insert in ONE
// transaction. This test runs a reader in a tight loop while a writer
// replaces a 200-edge set 50 times, and asserts the reader never
// observes a count below the full set — i.e. never catches the gap.
func TestReplaceEdgesByKindSource_AtomicForConcurrentReaders_1772(t *testing.T) {
	t.Parallel()
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	const (
		projectID = "proj-1772"
		nEdges    = 200
		nWrites   = 50
	)
	seedProject1772(t, store, projectID)
	edges := make([]Edge, nEdges)
	for i := range edges {
		edges[i] = Edge{
			ProjectID:  projectID,
			FromID:     fmt.Sprintf("from-%d", i),
			ToID:       fmt.Sprintf("to-%d", i),
			Kind:       "CALLS",
			Confidence: 1.0,
			Source:     "resolve_pass",
		}
	}
	if err := store.ReplaceEdgesByKindSource(projectID, "CALLS", "resolve_pass", nil, edges); err != nil {
		t.Fatalf("seed ReplaceEdgesByKindSource: %v", err)
	}

	stop := make(chan struct{})
	minSeen := int64(nEdges)
	var readErr atomic.Value
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			var n int64
			if e := store.ro.QueryRow(
				`SELECT COUNT(*) FROM edges WHERE project_id=? AND kind='CALLS' AND source='resolve_pass'`,
				projectID).Scan(&n); e != nil {
				readErr.Store(e)
				return
			}
			for {
				cur := atomic.LoadInt64(&minSeen)
				if n >= cur || atomic.CompareAndSwapInt64(&minSeen, cur, n) {
					break
				}
			}
		}
	}()

	for i := 0; i < nWrites; i++ {
		if err := store.ReplaceEdgesByKindSource(projectID, "CALLS", "resolve_pass", nil, edges); err != nil {
			close(stop)
			wg.Wait()
			t.Fatalf("ReplaceEdgesByKindSource write %d: %v", i, err)
		}
	}
	close(stop)
	wg.Wait()

	if e := readErr.Load(); e != nil {
		t.Fatalf("concurrent reader errored: %v", e)
	}
	if got := atomic.LoadInt64(&minSeen); got != nEdges {
		t.Errorf("a concurrent reader observed %d resolve_pass CALLS edges (want always %d) — "+
			"the replace is not atomic; readers catch the delete-before-reinsert gap (#1772)", got, nEdges)
	}
}

// Functional check: ReplaceEdgesByKindSource genuinely replaces (not
// merges) the set, and leaves edges of other (kind, source) untouched.
func TestReplaceEdgesByKindSource_ReplacesAndScopesByKindSource_1772(t *testing.T) {
	t.Parallel()
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()
	const projectID = "proj-replace"
	seedProject1772(t, store, projectID)

	// A per_file CALLS edge and an IMPORTS edge that must survive every
	// resolve_pass CALLS replace.
	untouched := []Edge{
		{ProjectID: projectID, FromID: "a", ToID: "b", Kind: "CALLS", Confidence: 1.0, Source: "per_file"},
		{ProjectID: projectID, FromID: "a", ToID: "pkg", Kind: "IMPORTS", Confidence: 1.0, Source: "resolve_pass"},
	}
	if err := store.BulkUpsertEdges(untouched); err != nil {
		t.Fatalf("seed untouched edges: %v", err)
	}

	first := []Edge{
		{ProjectID: projectID, FromID: "x", ToID: "y", Kind: "CALLS", Confidence: 1.0, Source: "resolve_pass"},
		{ProjectID: projectID, FromID: "x", ToID: "z", Kind: "CALLS", Confidence: 1.0, Source: "resolve_pass"},
	}
	if err := store.ReplaceEdgesByKindSource(projectID, "CALLS", "resolve_pass", nil, first); err != nil {
		t.Fatalf("first replace: %v", err)
	}

	count := func(kind, source string) int {
		t.Helper()
		var n int
		if e := store.ro.QueryRow(
			`SELECT COUNT(*) FROM edges WHERE project_id=? AND kind=? AND source=?`,
			projectID, kind, source).Scan(&n); e != nil {
			t.Fatalf("count(%s,%s): %v", kind, source, e)
		}
		return n
	}

	if got := count("CALLS", "resolve_pass"); got != 2 {
		t.Errorf("after first replace: resolve_pass CALLS = %d, want 2", got)
	}

	// Second replace with a different, smaller set fully supersedes it.
	if err := store.ReplaceEdgesByKindSource(projectID, "CALLS", "resolve_pass", nil,
		[]Edge{{ProjectID: projectID, FromID: "x", ToID: "y", Kind: "CALLS", Confidence: 1.0, Source: "resolve_pass"}},
	); err != nil {
		t.Fatalf("second replace: %v", err)
	}
	if got := count("CALLS", "resolve_pass"); got != 1 {
		t.Errorf("after second replace: resolve_pass CALLS = %d, want 1 (replace, not merge)", got)
	}

	// An empty replace clears the set entirely.
	if err := store.ReplaceEdgesByKindSource(projectID, "CALLS", "resolve_pass", nil, nil); err != nil {
		t.Fatalf("empty replace: %v", err)
	}
	if got := count("CALLS", "resolve_pass"); got != 0 {
		t.Errorf("after empty replace: resolve_pass CALLS = %d, want 0", got)
	}

	// The per_file CALLS edge and the IMPORTS edge were never touched.
	if got := count("CALLS", "per_file"); got != 1 {
		t.Errorf("per_file CALLS edge was disturbed: %d, want 1", got)
	}
	if got := count("IMPORTS", "resolve_pass"); got != 1 {
		t.Errorf("IMPORTS resolve_pass edge was disturbed: %d, want 1", got)
	}
}
