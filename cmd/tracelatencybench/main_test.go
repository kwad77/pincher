// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// End-to-end smoke: open a fresh test DB, seed a tiny project with
// edges, run the tool, assert the output mentions both CTE and
// Closure paths plus a ratio.
//
// Tiny corpus, n=5 — we're not measuring real perf here, just gating
// that the tool compiles, parses args, talks to the DB, and renders.
func TestRun_E2E_TinyProject(t *testing.T) {
	dir := t.TempDir()
	store, err := db.Open(dir)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	pid := "p"
	if err := store.UpsertProject(db.Project{
		ID:        pid,
		Path:      dir,
		Name:      "p",
		IndexedAt: time.Now(),
	}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	// Seed 3 functions A → B → C so a trace from A has reach.
	mk := func(name string) db.Symbol {
		return db.Symbol{
			ID:            "p::" + name + "#Function",
			ProjectID:     pid,
			FilePath:      "main.go",
			Language:      "Go",
			Kind:          "Function",
			Name:          name,
			QualifiedName: name,
		}
	}
	syms := []db.Symbol{}
	for _, name := range []string{"A", "B", "C"} {
		syms = append(syms, mk(name))
	}
	if err := store.BulkUpsertSymbols(syms); err != nil {
		t.Fatalf("BulkUpsertSymbols: %v", err)
	}
	edges := []db.Edge{}
	for _, e := range [][2]string{{"A", "B"}, {"B", "C"}} {
		edges = append(edges, db.Edge{
			ProjectID:  pid,
			FromID:     "p::" + e[0] + "#Function",
			ToID:       "p::" + e[1] + "#Function",
			Kind:       "CALLS",
			Confidence: 1.0,
		})
	}
	if err := store.BulkUpsertEdges(edges); err != nil {
		t.Fatalf("BulkUpsertEdges: %v", err)
	}
	// Pre-build closure so the run loop can time both paths against
	// the same data. (run() also calls BuildClosure but doing it here
	// keeps the test independent of that subroutine.)
	if err := store.BuildClosure(context.Background(), pid, 3); err != nil {
		t.Fatalf("BuildClosure: %v", err)
	}
	store.Close()

	// run() opens its own store via -db. Use the same dir.
	var stderr bytes.Buffer
	code := run([]string{"-db", dir, "-project", pid, "-n", "3", "-depth", "3"}, &stderr)
	if code != 0 {
		t.Fatalf("run exited %d; stderr: %s", code, stderr.String())
	}
	// stdout went to os.Stdout — we can't capture it without harness
	// changes. Asserting code 0 + no stderr is the contract for the
	// happy path; stderr captures real failure modes.
	if stderr.Len() != 0 {
		t.Errorf("expected no stderr; got: %s", stderr.String())
	}
}

// run reports the gating-acceptance hint in its summary. Doc-mode
// behaviour pinned so future changes don't quietly drop the gate.
func TestRun_E2E_ProjectMissing(t *testing.T) {
	dir := t.TempDir()
	// No project upserted.
	var stderr bytes.Buffer
	code := run([]string{"-db", dir, "-project", "nope"}, &stderr)
	if code == 0 {
		t.Errorf("expected non-zero exit on missing project; got 0")
	}
	if !strings.Contains(stderr.String(), "nope") && !strings.Contains(stderr.String(), "no indexed projects") {
		t.Errorf("stderr should name the missing project or report no projects; got: %s", stderr.String())
	}
}
