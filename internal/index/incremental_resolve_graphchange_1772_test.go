// SPDX-License-Identifier: MIT

package index

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// #1772 graph-CHANGING repro layer. The watcher harness in
// incremental_resolve_watcher_1772_test.go uses semantically-inert
// edits (comment appends) and proves the scope logic is sound for
// those. This layer does what a real branch switch does: it adds and
// removes actual calls, across a multi-hop referrer chain
// (top -> mid -> leaf), and asserts each function's callee set matches
// the new content exactly.
//
// invalidateReferencers' own doc comment flags a "one hop" limitation:
// a referencer that itself has incoming edges drops those edges when
// DeleteSymbolsForFile cascades. This layer exercises exactly that —
// edit leaf.go and check that top.go (two hops up, never directly
// edited) keeps every CALLS edge.

// calleesOf1772 returns the set of callee names for CALLS edges out of
// every symbol named callerName in the project.
func calleesOf1772(t *testing.T, store *db.Store, projectID, callerName string) map[string]bool {
	t.Helper()
	rows, err := store.RO().Query(
		`SELECT ts.name FROM edges e
		   JOIN symbols fs ON fs.project_id = e.project_id AND fs.id = e.from_id
		   JOIN symbols ts ON ts.project_id = e.project_id AND ts.id = e.to_id
		  WHERE e.project_id = ? AND e.kind = 'CALLS' AND fs.name = ?`,
		projectID, callerName)
	if err != nil {
		t.Fatalf("calleesOf(%s): %v", callerName, err)
	}
	defer rows.Close()
	got := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("calleesOf(%s) scan: %v", callerName, err)
		}
		got[n] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("calleesOf(%s) rows.Err: %v", callerName, err)
	}
	return got
}

// assertCallees1772 asserts the exact callee set of callerName.
func assertCallees1772(t *testing.T, label string, store *db.Store, projectID, callerName string, want ...string) {
	t.Helper()
	got := calleesOf1772(t, store, projectID, callerName)
	wantSet := map[string]bool{}
	for _, w := range want {
		wantSet[w] = true
	}
	var missing, extra []string
	for w := range wantSet {
		if !got[w] {
			missing = append(missing, w)
		}
	}
	for g := range got {
		if !wantSet[g] {
			extra = append(extra, g)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Errorf("[%s] %s is MISSING CALLS edges to {%s} — graph degradation (#1772)",
			label, callerName, strings.Join(missing, ", "))
	}
	if len(extra) > 0 {
		t.Errorf("[%s] %s has UNEXPECTED CALLS edges to {%s}",
			label, callerName, strings.Join(extra, ", "))
	}
}

// repro1772GraphChain writes the three-package chain top -> mid -> leaf.
// leafExtra / midExtra / topExtra append an extra inert function so the
// file's content (hash) changes without changing any call.
func repro1772GraphChain(t *testing.T, dir string, leafBody, midBody, topBody string) {
	t.Helper()
	writeFile(t, dir, "go.mod", "module example.com/gc1772\n\ngo 1.22\n")
	writeFile(t, dir, "leaf/leaf.go", leafBody)
	writeFile(t, dir, "mid/mid.go", midBody)
	writeFile(t, dir, "top/top.go", topBody)
}

const gc1772LeafBase = `package leaf

func Leaf() int { return 1 }
`
const gc1772MidBase = `package mid

import "example.com/gc1772/leaf"

func Mid() int    { return midLocal() + leaf.Leaf() }
func midLocal() int { return 2 }
`
const gc1772TopBase = `package top

import "example.com/gc1772/mid"

func Top() int    { return topLocal() + mid.Mid() }
func topLocal() int { return 3 }
`

// TestIncrementalResolve_CALLS_MultiHopReferrer_LeafEdited_1772 — edit
// leaf.go (genuinely: add a function). leaf re-extracts; mid is a
// direct referrer (Mid -> leaf.Leaf); top is a TWO-hop referrer
// (Top -> mid.Mid). Neither mid.go nor top.go is edited — their callee
// sets must be exactly preserved across every tick.
func TestIncrementalResolve_CALLS_MultiHopReferrer_LeafEdited_1772(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	repro1772GraphChain(t, dir, gc1772LeafBase, gc1772MidBase, gc1772TopBase)

	res, err := idx.Index(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("full index: %v", err)
	}
	projectID := res.ProjectID

	assertCallees1772(t, "baseline", store, projectID, "Mid", "midLocal", "Leaf")
	assertCallees1772(t, "baseline", store, projectID, "Top", "topLocal", "Mid")

	// Five ticks, each genuinely changing leaf.go's content (adding then
	// removing a real function). mid/top are never edited.
	leafVariants := []string{
		gc1772LeafBase + "\nfunc LeafExtra1() int { return 11 }\n",
		gc1772LeafBase,
		gc1772LeafBase + "\nfunc LeafExtra2() int { return 22 }\nfunc LeafExtra3() int { return 33 }\n",
		gc1772LeafBase + "\nfunc LeafExtra2() int { return 22 }\n",
		gc1772LeafBase,
	}
	for i, body := range leafVariants {
		writeFile(t, dir, "leaf/leaf.go", body)
		watcherTick1772(t, idx, store, projectID, dir)
		label := "leaf tick"
		assertCallees1772(t, label, store, projectID, "Mid", "midLocal", "Leaf")
		assertCallees1772(t, label, store, projectID, "Top", "topLocal", "Mid")
		_ = i
	}
}

// TestIncrementalResolve_CALLS_CallerEditChangesGraph_1772 — edit the
// MIDDLE of the chain so its OWN call set genuinely changes, then
// assert both the new edges land AND the unedited top.go keeps its
// edge into mid. This is the resolve pass handling a real
// pending_edges change for a file that is simultaneously a referrer's
// callee.
func TestIncrementalResolve_CALLS_CallerEditChangesGraph_1772(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	repro1772GraphChain(t, dir, gc1772LeafBase, gc1772MidBase, gc1772TopBase)

	res, err := idx.Index(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("full index: %v", err)
	}
	projectID := res.ProjectID
	assertCallees1772(t, "baseline", store, projectID, "Mid", "midLocal", "Leaf")
	assertCallees1772(t, "baseline", store, projectID, "Top", "topLocal", "Mid")

	// Tick 1: Mid drops its leaf.Leaf() call and adds a second local.
	writeFile(t, dir, "mid/mid.go", `package mid

import "example.com/gc1772/leaf"

func Mid() int    { return midLocal() + midLocal2() }
func midLocal() int  { return 2 }
func midLocal2() int { return 5 }

var _ = leaf.Leaf
`)
	watcherTick1772(t, idx, store, projectID, dir)
	assertCallees1772(t, "mid drops Leaf, adds midLocal2", store, projectID,
		"Mid", "midLocal", "midLocal2")
	// top.go unedited — its edge into Mid must survive the mid re-extract.
	assertCallees1772(t, "mid drops Leaf, adds midLocal2", store, projectID,
		"Top", "topLocal", "Mid")

	// Tick 2: Mid restores leaf.Leaf() and keeps both locals.
	writeFile(t, dir, "mid/mid.go", `package mid

import "example.com/gc1772/leaf"

func Mid() int    { return midLocal() + midLocal2() + leaf.Leaf() }
func midLocal() int  { return 2 }
func midLocal2() int { return 5 }
`)
	watcherTick1772(t, idx, store, projectID, dir)
	assertCallees1772(t, "mid restores Leaf", store, projectID,
		"Mid", "midLocal", "midLocal2", "Leaf")
	assertCallees1772(t, "mid restores Leaf", store, projectID,
		"Top", "topLocal", "Mid")

	// Tick 3: edit top.go itself — Top drops mid.Mid(), keeps topLocal.
	writeFile(t, dir, "top/top.go", `package top

import "example.com/gc1772/mid"

func Top() int    { return topLocal() }
func topLocal() int { return 3 }

var _ = mid.Mid
`)
	watcherTick1772(t, idx, store, projectID, dir)
	assertCallees1772(t, "top drops Mid", store, projectID, "Top", "topLocal")
	// mid.go unedited this tick — its edges must be intact.
	assertCallees1772(t, "top drops Mid", store, projectID,
		"Mid", "midLocal", "midLocal2", "Leaf")
}
