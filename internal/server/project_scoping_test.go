// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// TestHandleSymbol_ProjectScopedLookup pins #2: when the caller passes
// `project=A` and the requested symbol ID happens to also exist in
// project B (which CAN happen pre-#1 because the symbol_id format
// `{file_path}::{qualified_name}#{kind}` is global), the handler MUST
// return project A's row, not project B's. Pre-fix, GetSymbol(id) was
// unscoped and the SQLite PK collision was resolved by INSERT OR
// REPLACE — whichever project indexed last would shadow the other.
func TestHandleSymbol_ProjectScopedLookup(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)

	// Two projects with a colliding symbol ID. In real life this happens
	// when two Go projects both have `cmd/main.go` with `func main()`.
	// We synthesise the collision directly.
	mustUpsertProject(t, store, "proj-A", "/tmp/A", "projA")
	mustUpsertProject(t, store, "proj-B", "/tmp/B", "projB")

	// Project A's symbol with the colliding ID.
	mustUpsertSymbols(t, store, []db.Symbol{{
		ID: "cmd/main.go::main.main#Function", ProjectID: "proj-A",
		FilePath: "cmd/main.go", Name: "main", QualifiedName: "main.main",
		Kind: "Function", Language: "Go", Signature: "// FROM project A",
	}})
	// Project B's symbol with the SAME ID. After this insert, vanilla
	// GetSymbol(id) will return project B's row (last-write-wins).
	mustUpsertSymbols(t, store, []db.Symbol{{
		ID: "cmd/main.go::main.main#Function", ProjectID: "proj-B",
		FilePath: "cmd/main.go", Name: "main", QualifiedName: "main.main",
		Kind: "Function", Language: "Go", Signature: "// FROM project B",
	}})

	// Sanity: with `project="projA"`, the handler MUST return project A's
	// signature, even though project B was the most recent writer. The
	// scoped lookup catches the collision and returns nothing for project
	// A in this synthetic case (since the row was overwritten).
	res, err := srv.handleSymbol(context.Background(), makeReq(map[string]any{
		"id":      "cmd/main.go::main.main#Function",
		"project": "projA",
	}))
	if err != nil {
		t.Fatalf("handleSymbol: %v", err)
	}
	// One of two correct outcomes for the projA scoped lookup:
	//   1. Returns proj-A's row — possible if the implementation stores
	//      both rows under different (project_id, id) tuples; the row
	//      hasn't been clobbered.
	//   2. Returns IsError=true with "not found" — correct: the proj-A
	//      row was clobbered by proj-B's INSERT OR REPLACE, so a
	//      project-scoped SELECT on the survivor row excludes it.
	// The unacceptable outcome (pre-fix) was: returns proj-B's row to a
	// caller asking for proj-A.
	if res.IsError {
		// "not found" path is acceptable.
		t.Logf("projA scoped lookup returned not-found (proj-B clobbered the PK row); structurally correct.")
	} else {
		m := decode(t, res)
		sig, _ := m["signature"].(string)
		if sig != "// FROM project A" {
			t.Errorf("scoped lookup returned wrong project's row: signature=%q (want \"// FROM project A\" or not-found)", sig)
		}
	}

	// Now ask for `project="projB"` — must return project B's row.
	res, err = srv.handleSymbol(context.Background(), makeReq(map[string]any{
		"id":      "cmd/main.go::main.main#Function",
		"project": "projB",
	}))
	if err != nil {
		t.Fatalf("handleSymbol projB: %v", err)
	}
	if res.IsError {
		t.Fatalf("project B lookup failed (unexpected): %v", res)
	}
	m := decode(t, res)
	if sig, _ := m["signature"].(string); sig != "// FROM project B" {
		t.Errorf("project B scoped lookup returned wrong signature: %q (want \"// FROM project B\")", sig)
	}

	// Without `project`, the unscoped path is used (back-compat). One row
	// returns; we don't pin which because last-write-wins is implementation
	// defined. The point is: when project IS passed, the cross-project
	// leak is structurally closed.
	res, err = srv.handleSymbol(context.Background(), makeReq(map[string]any{
		"id": "cmd/main.go::main.main#Function",
	}))
	if err != nil {
		t.Fatalf("handleSymbol no-project: %v", err)
	}
	if res.IsError {
		t.Errorf("unscoped lookup must still work for legacy callers; got IsError=true")
	}
}

func mustUpsertProject(t *testing.T, store *db.Store, id, path, name string) {
	t.Helper()
	if err := store.UpsertProject(db.Project{ID: id, Path: path, Name: name, IndexedAt: time.Now()}); err != nil {
		t.Fatalf("upsert project %s: %v", id, err)
	}
}

func mustUpsertSymbols(t *testing.T, store *db.Store, syms []db.Symbol) {
	t.Helper()
	if err := store.BulkUpsertSymbols(syms); err != nil {
		t.Fatalf("bulk upsert: %v", err)
	}
}
