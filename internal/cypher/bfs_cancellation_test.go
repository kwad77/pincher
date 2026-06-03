// SPDX-License-Identifier: MIT

package cypher

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #1599 v0.84: runBFS honors ctx cancellation between start nodes.
//
// Pre-fix the per-start-node loop ran every iteration even after the
// caller cancelled — bfsViaCTE's QueryContext errors immediately on a
// cancelled ctx, but the for loop continued through every remaining
// startNode firing N error-returning SQL calls. With the entry-point
// ctx.Err() check the loop bails on the first iteration after cancel.
//
// Pattern mirrors v0.82's #1579 composite cancellation contract + v0.83
// #1595 inner-loop ctx checks.

func seedSymbolsForBFSCancel(t *testing.T) (*db.Store, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := db.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	const projectID = "p1"
	if err := store.UpsertProject(db.Project{ID: projectID, Path: "/p1", Name: "p1"}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	// Seed enough start nodes that the loop would visibly iterate
	// without the bail.
	var syms []db.Symbol
	for i := 0; i < 50; i++ {
		name := "fn" + strings.Repeat("x", 1) + string(rune('0'+i%10))
		syms = append(syms, db.Symbol{
			ID:                   projectID + "::a." + name + "#Function",
			ProjectID:            projectID,
			FilePath:             "a.go",
			Name:                 name,
			QualifiedName:        "a." + name,
			Kind:                 "Function",
			Language:             "Go",
			ExtractionConfidence: 1.0,
		})
	}
	if err := store.BulkUpsertSymbols(syms); err != nil {
		t.Fatalf("BulkUpsertSymbols: %v", err)
	}
	return store, projectID
}

func TestRunBFS_HonorsCtxCancellation(t *testing.T) {
	t.Parallel()
	store, pid := seedSymbolsForBFSCancel(t)
	ex := &Executor{DB: store.DB(), ProjectID: pid}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel BEFORE the call so the entry-point check fires

	start := time.Now()
	_, err := ex.Execute(ctx,
		`MATCH (a:Function)-[:CALLS*1..3]->(b:Function) RETURN b.name LIMIT 20`)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error on pre-cancelled ctx; got nil")
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("runBFS took %v on cancelled ctx with 50 start nodes — bail-on-cancel gap (expected <500ms)", elapsed)
	}
}
