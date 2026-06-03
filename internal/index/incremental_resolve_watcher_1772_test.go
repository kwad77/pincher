// SPDX-License-Identifier: MIT

package index

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// #1772 watcher-faithful repro harness — incremental resolve silently
// dropping CALLS edges.
//
// The existing 1772 tests drive `idx.Index()` directly. A real watcher
// tick does more: it computes the changed set and clears the file_hash
// of every referrer (#427 invalidateReferencers) BEFORE the Index call,
// so referrers re-extract too. This harness reproduces that exact
// sequence — changedFiles -> invalidateReferencers -> Index — which is
// the path the issue's "branch switches / binary swaps" repro actually
// exercised.
//
// Each test:
//  1. Full-indexes a small multi-package fixture.
//  2. Snapshots every CALLS edge WITH its `source` column.
//  3. Performs an incremental tick that edits files WITHOUT changing
//     any call-graph semantics (a trailing comment), so the CALLS edge
//     set MUST be byte-identical before and after.
//  4. Asserts the set is identical; a failure prints the dropped edges
//     with their `source`, so per_file (intra-file, extraction-time)
//     vs resolve_pass (cross-file) loss is immediately visible.
//
// If any of these fail, the failure IS the minimal #1772 reproduction.

const repro1772GoMod = "module example.com/repro1772\n\ngo 1.22\n"

// alpha: a 3-edge intra-file call chain. Nothing in the harness edits
// these functions; they are the canary for graph degradation.
const repro1772Alpha = `package alpha

func AlphaEntry() int { return alphaMid() }
func alphaMid() int   { return alphaInner() }
func alphaInner() int { return alphaLeaf() }
func alphaLeaf() int  { return 1 }
`

// beta: BetaEntry->betaMid is intra-file; betaMid->alpha.AlphaEntry
// crosses the package boundary, so beta is a referrer of alpha.
const repro1772Beta = `package beta

import "example.com/repro1772/alpha"

func BetaEntry() int { return betaMid() }
func betaMid() int   { return alpha.AlphaEntry() }
`

// gamma: a fully unrelated island — no edges into or out of
// alpha/beta. Editing anything else must never disturb these edges.
const repro1772Gamma = `package gamma

func GammaEntry() int { return gammaMid() }
func gammaMid() int   { return gammaLeaf() }
func gammaLeaf() int  { return 2 }
`

// trigger: makes no calls, so editing this file changes zero CALLS
// edges anywhere — the cleanest possible incremental tick.
const repro1772Trigger = `package trigger

func Trigger() string { return "ok" }
`

// expectedRepro1772CALLS — alpha 3 + beta 1 intra + beta 1 cross +
// gamma 2 = 7.
const expectedRepro1772CALLS = 7

type callsEdge1772 struct{ from, to, source string }

// snapshotCALLS1772 returns every CALLS edge in the project keyed on
// from\x00to, carrying the `source` column so a diff can attribute a
// loss to per_file vs resolve_pass.
func snapshotCALLS1772(t *testing.T, store *db.Store, projectID string) map[string]callsEdge1772 {
	t.Helper()
	rows, err := store.RO().Query(
		`SELECT from_id, to_id, COALESCE(source,'') FROM edges
		   WHERE project_id = ? AND kind = 'CALLS'`, projectID)
	if err != nil {
		t.Fatalf("snapshot CALLS query: %v", err)
	}
	defer rows.Close()
	out := map[string]callsEdge1772{}
	for rows.Next() {
		var e callsEdge1772
		if err := rows.Scan(&e.from, &e.to, &e.source); err != nil {
			t.Fatalf("snapshot scan: %v", err)
		}
		out[e.from+"\x00"+e.to] = e
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("snapshot rows.Err: %v", err)
	}
	return out
}

// assertCALLSIdentical1772 fails with a per-edge, source-tagged diff
// when the incremental tick changed the CALLS edge set. Because every
// test edits files without changing call-graph semantics, ANY
// difference is a bug.
func assertCALLSIdentical1772(t *testing.T, label string, before, after map[string]callsEdge1772) {
	t.Helper()
	var dropped, added []string
	for k, e := range before {
		if _, ok := after[k]; !ok {
			dropped = append(dropped, fmt.Sprintf("  - %s -> %s (source=%q)", e.from, e.to, e.source))
		}
	}
	for k, e := range after {
		if _, ok := before[k]; !ok {
			added = append(added, fmt.Sprintf("  + %s -> %s (source=%q)", e.from, e.to, e.source))
		}
	}
	sort.Strings(dropped)
	sort.Strings(added)
	if len(dropped) > 0 {
		t.Errorf("[%s] %d CALLS edge(s) DROPPED by the incremental tick — graph degradation (#1772):\n%s",
			label, len(dropped), strings.Join(dropped, "\n"))
	}
	if len(added) > 0 {
		t.Errorf("[%s] %d unexpected CALLS edge(s) ADDED by the incremental tick:\n%s",
			label, len(added), strings.Join(added, "\n"))
	}
}

// setupRepro1772 writes the fixture, full-indexes it once, and returns
// the indexer, store, repo dir, and project id.
func setupRepro1772(t *testing.T) (*Indexer, *db.Store, string, string) {
	t.Helper()
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", repro1772GoMod)
	writeFile(t, dir, "alpha/alpha.go", repro1772Alpha)
	writeFile(t, dir, "beta/beta.go", repro1772Beta)
	writeFile(t, dir, "gamma/gamma.go", repro1772Gamma)
	writeFile(t, dir, "trigger/trigger.go", repro1772Trigger)

	res, err := idx.Index(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("initial full Index: %v", err)
	}
	return idx, store, dir, res.ProjectID
}

// watcherTick1772 reproduces one watcher reindex tick exactly: compute
// the changed set, invalidate referrers' hashes (#427), then run the
// incremental Index. This is what Watch() does per project per tick.
func watcherTick1772(t *testing.T, idx *Indexer, store *db.Store, projectID, dir string) {
	t.Helper()
	p, err := store.GetProject(projectID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if p == nil {
		t.Fatalf("project %q not found", projectID)
	}
	changed := idx.changedFiles(*p)
	if len(changed) == 0 {
		t.Fatalf("watcherTick: changedFiles returned empty — the edit did not register; harness bug, not #1772")
	}
	idx.invalidateReferencers(*p, changed)
	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("incremental Index: %v", err)
	}
}

// touch1772 rewrites a fixture file with a trailing comment appended —
// the content hash changes (so the file re-extracts) but the call
// graph is semantically unchanged.
func touch1772(t *testing.T, dir, relpath, base string) {
	t.Helper()
	writeFile(t, dir, relpath, base+"\n// harness touch — semantically inert\n")
}

func baselineGuard1772(t *testing.T, before map[string]callsEdge1772) {
	t.Helper()
	if len(before) < expectedRepro1772CALLS {
		t.Fatalf("baseline: full index produced %d CALLS edges, expected %d — "+
			"fixture or extractor broken, the #1772 repro would be vacuous",
			len(before), expectedRepro1772CALLS)
	}
}

// TestIncrementalResolve_CALLS_SurvivesNoChangeReindex_1772 — a reindex
// with zero file edits must leave the CALLS graph byte-identical. Every
// file hash-skips; nothing should be deleted or re-resolved.
func TestIncrementalResolve_CALLS_SurvivesNoChangeReindex_1772(t *testing.T) {
	idx, store, dir, projectID := setupRepro1772(t)
	before := snapshotCALLS1772(t, store, projectID)
	baselineGuard1772(t, before)

	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("no-change reindex: %v", err)
	}
	after := snapshotCALLS1772(t, store, projectID)
	assertCALLSIdentical1772(t, "no-change reindex", before, after)
}

// TestIncrementalResolve_CALLS_SurvivesUnrelatedFileEdit_1772 — editing
// trigger.go (which makes no calls and has no referrers) must disturb
// nothing. extractedFiles={trigger.go}, referrerFiles={}; the scoped
// incremental resolve runs over trigger.go alone. alpha/beta/gamma all
// hash-skip — their CALLS edges, especially the intra-file per_file
// ones, must survive untouched.
func TestIncrementalResolve_CALLS_SurvivesUnrelatedFileEdit_1772(t *testing.T) {
	idx, store, dir, projectID := setupRepro1772(t)
	before := snapshotCALLS1772(t, store, projectID)
	baselineGuard1772(t, before)

	touch1772(t, dir, "trigger/trigger.go", repro1772Trigger)
	watcherTick1772(t, idx, store, projectID, dir)

	after := snapshotCALLS1772(t, store, projectID)
	assertCALLSIdentical1772(t, "unrelated-file edit", before, after)
}

// TestIncrementalResolve_CALLS_SurvivesReferrerFileUnchanged_1772 —
// editing alpha.go (a comment-only change) makes beta a referrer:
// beta has a cross-file edge into alpha. invalidateReferencers clears
// beta's hash so it re-extracts too. beta's intra-file per_file edge
// (BetaEntry->betaMid) and its cross-file resolve_pass edge
// (betaMid->alpha.AlphaEntry) must both survive. gamma is wholly
// outside the scope.
func TestIncrementalResolve_CALLS_SurvivesReferrerFileUnchanged_1772(t *testing.T) {
	idx, store, dir, projectID := setupRepro1772(t)
	before := snapshotCALLS1772(t, store, projectID)
	baselineGuard1772(t, before)

	touch1772(t, dir, "alpha/alpha.go", repro1772Alpha)
	watcherTick1772(t, idx, store, projectID, dir)

	after := snapshotCALLS1772(t, store, projectID)
	assertCALLSIdentical1772(t, "referrer-file pulled into scope", before, after)
}

// TestIncrementalResolve_CALLS_SurvivesMultiFileEdit_1772 — the
// branch-switch shape from the issue: several files change in one tick.
// alpha, gamma and trigger are all touched (comment-only); beta is not.
// The full CALLS graph is semantically unchanged, so the edge set must
// be byte-identical after the tick.
func TestIncrementalResolve_CALLS_SurvivesMultiFileEdit_1772(t *testing.T) {
	idx, store, dir, projectID := setupRepro1772(t)
	before := snapshotCALLS1772(t, store, projectID)
	baselineGuard1772(t, before)

	touch1772(t, dir, "alpha/alpha.go", repro1772Alpha)
	touch1772(t, dir, "gamma/gamma.go", repro1772Gamma)
	touch1772(t, dir, "trigger/trigger.go", repro1772Trigger)
	watcherTick1772(t, idx, store, projectID, dir)

	after := snapshotCALLS1772(t, store, projectID)
	assertCALLSIdentical1772(t, "multi-file edit (branch-switch shape)", before, after)
}

// TestIncrementalResolve_CALLS_SurvivesRepeatedTicks_1772 — the issue's
// core claim is cumulative: "the graph degrades over a working
// session." One tick may lose nothing; ten in a row may. Run ten
// watcher ticks, each a semantically-inert edit, and assert the graph
// never degrades.
func TestIncrementalResolve_CALLS_SurvivesRepeatedTicks_1772(t *testing.T) {
	idx, store, dir, projectID := setupRepro1772(t)
	before := snapshotCALLS1772(t, store, projectID)
	baselineGuard1772(t, before)

	files := []struct{ rel, base string }{
		{"alpha/alpha.go", repro1772Alpha},
		{"beta/beta.go", repro1772Beta},
		{"gamma/gamma.go", repro1772Gamma},
		{"trigger/trigger.go", repro1772Trigger},
	}
	for i := 0; i < 10; i++ {
		f := files[i%len(files)]
		// Vary the inert edit so the hash changes every tick.
		writeFile(t, dir, f.rel, f.base+fmt.Sprintf("\n// harness touch %d\n", i))
		watcherTick1772(t, idx, store, projectID, dir)
		after := snapshotCALLS1772(t, store, projectID)
		assertCALLSIdentical1772(t, fmt.Sprintf("tick %d (edited %s)", i, f.rel), before, after)
	}
}

// bigGoFile generates a Go file shaped like the real #1772 repro
// targets (cypher/engine.go, ast/extractor.go): one exported Entry
// that fans out to many same-file helpers — nHelpers intra-file CALLS
// edges from a single function — plus an optional cross-package call
// into the next package, which makes this package a referrer of it.
func bigGoFile(pkgName, modulePath, importPkgDir, importPkgName string, nHelpers int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "package %s\n", pkgName)
	if importPkgName != "" {
		fmt.Fprintf(&b, "\nimport %q\n", modulePath+"/"+importPkgDir)
	}
	b.WriteString("\nfunc Entry() int {\n\ttotal := 0\n")
	for i := 0; i < nHelpers; i++ {
		fmt.Fprintf(&b, "\ttotal += helper%d()\n", i)
	}
	if importPkgName != "" {
		fmt.Fprintf(&b, "\ttotal += %s.Entry()\n", importPkgName)
	}
	b.WriteString("\treturn total\n}\n\n")
	for i := 0; i < nHelpers; i++ {
		fmt.Fprintf(&b, "func helper%d() int { return %d }\n", i, i)
	}
	return b.String()
}

// TestIncrementalResolve_CALLS_SurvivesAtScale_1772 — the small-fixture
// tests above pass; the real #1772 repros were large files in a
// 700-file repo. This fixture has 70 packages, each a large fan-out
// file (Entry calls 20 same-file helpers), wired into a cross-package
// referrer chain. It exercises BOTH incremental-resolve code paths:
//   - single-package edits → scope = {pkg, referrer} ≤ 64 → scoped path
//   - a 66-package edit     → scope > 64 → project-wide fallback path
// Every edit is semantically inert, so the CALLS graph must stay
// byte-identical across every tick.
func TestIncrementalResolve_CALLS_SurvivesAtScale_1772(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	const (
		nPkgs      = 70
		nHelpers   = 20
		modulePath = "example.com/scaled1772"
	)
	writeFile(t, dir, "go.mod", "module "+modulePath+"\n\ngo 1.22\n")

	pkgFiles := make([]string, nPkgs)
	pkgBodies := make([]string, nPkgs)
	for k := 0; k < nPkgs; k++ {
		pkgName := fmt.Sprintf("p%02d", k)
		pkgFiles[k] = pkgName + "/" + pkgName + ".go"
		var importDir, importName string
		if k+1 < nPkgs {
			importDir = fmt.Sprintf("p%02d", k+1)
			importName = importDir
		}
		pkgBodies[k] = bigGoFile(pkgName, modulePath, importDir, importName, nHelpers)
		writeFile(t, dir, pkgFiles[k], pkgBodies[k])
	}

	res, err := idx.Index(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("full index: %v", err)
	}
	projectID := res.ProjectID

	before := snapshotCALLS1772(t, store, projectID)
	// nPkgs*nHelpers intra-file + (nPkgs-1) cross-package.
	minExpected := nPkgs * nHelpers
	if len(before) < minExpected {
		t.Fatalf("baseline: %d CALLS edges, expected >= %d — fixture/extractor broken",
			len(before), minExpected)
	}

	// Scoped-path ticks: edit one package at a time, scattered through
	// the chain (head, middle, tail). Each pulls in exactly one referrer.
	for tick, k := range []int{3, 40, 69, 17, 55, 0} {
		writeFile(t, dir, pkgFiles[k], pkgBodies[k]+fmt.Sprintf("\n// scale touch %d\n", tick))
		watcherTick1772(t, idx, store, projectID, dir)
		after := snapshotCALLS1772(t, store, projectID)
		assertCALLSIdentical1772(t,
			fmt.Sprintf("scoped tick %d (edited %s)", tick, pkgFiles[k]), before, after)
	}

	// Threshold-crossing tick: edit 66 packages at once so the resolve
	// scope (66 + referrers) exceeds incrementalScopeThreshold=64 and
	// the indexer falls back to the project-wide resolve.
	for k := 0; k < 66; k++ {
		writeFile(t, dir, pkgFiles[k], pkgBodies[k]+"\n// threshold-crossing touch\n")
	}
	watcherTick1772(t, idx, store, projectID, dir)
	after := snapshotCALLS1772(t, store, projectID)
	assertCALLSIdentical1772(t, "threshold-crossing tick (66-file edit)", before, after)
}

// methodHeavyGoFile generates the #986 shape: a struct whose Run method
// calls many same-file sibling methods — intra-file method CALLS edges.
// #986 ("binary-swap loses Methods in large Go files") is in #1772's
// lineage; if a re-extraction of a method-heavy file emits fewer edges
// than the first pass, that is an extractor-determinism bug and the
// CALLS graph degrades exactly as #1772 describes.
func methodHeavyGoFile(pkgName string, nMethods int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "package %s\n\ntype Worker struct{}\n\n", pkgName)
	b.WriteString("func (w Worker) Run() int {\n\ttotal := 0\n")
	for i := 0; i < nMethods; i++ {
		fmt.Fprintf(&b, "\ttotal += w.step%d()\n", i)
	}
	b.WriteString("\treturn total\n}\n\n")
	for i := 0; i < nMethods; i++ {
		fmt.Fprintf(&b, "func (w Worker) step%d() int { return %d }\n", i, i)
	}
	return b.String()
}

// TestIncrementalResolve_CALLS_MethodHeavyFile_StableAcrossForce_1772 —
// extractor-determinism guard. A method-heavy file (Run fans out to 30
// sibling methods) is force-reindexed three times; the CALLS edge set
// must be byte-identical every pass. A force pass re-extracts every
// file, so this isolates the extractor: if a large Go file emits a
// different edge set on re-extraction, the graph degrades (#986/#1772).
func TestIncrementalResolve_CALLS_MethodHeavyFile_StableAcrossForce_1772(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/methods1772\n\ngo 1.22\n")
	writeFile(t, dir, "worker/worker.go", methodHeavyGoFile("worker", 30))

	res, err := idx.Index(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("full index: %v", err)
	}
	projectID := res.ProjectID

	before := snapshotCALLS1772(t, store, projectID)
	if len(before) < 30 {
		t.Fatalf("baseline: %d CALLS edges, expected >= 30 (Run -> step0..step29) — "+
			"extractor dropped method CALLS on the first pass", len(before))
	}

	for pass := 1; pass <= 3; pass++ {
		if _, err := idx.Index(context.Background(), dir, true); err != nil {
			t.Fatalf("force reindex pass %d: %v", pass, err)
		}
		after := snapshotCALLS1772(t, store, projectID)
		assertCALLSIdentical1772(t, fmt.Sprintf("force reindex pass %d", pass), before, after)
	}
}
