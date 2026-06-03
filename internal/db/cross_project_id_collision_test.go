// SPDX-License-Identifier: MIT

package db

import (
	"path/filepath"
	"testing"
)

// #1231 root cause + fix verification.
//
// Pre-v28 the symbols table had `id TEXT PRIMARY KEY` without project_id
// included. MakeSymbolID returns "{file_path}::{qualified_name}#{kind}" —
// no project scope. So two projects with the same relative file path
// containing the same symbol collided on PK, and INSERT OR REPLACE
// silently flipped the row's project_id to the latest writer.
//
// Live shape: pincher-repo's internal/server/server.go showed 8 of 75
// Methods because sniffer (an older mirror of pincher) also indexed
// the same relative path. The 67 pre-existing rows got clobbered;
// only the 8 newly-added methods survived because their IDs were
// unique to pincher-repo's snapshot.
//
// Schema v28 makes the symbols PRIMARY KEY composite (project_id, id).
// Same id in two projects is now two distinct rows. INSERT OR REPLACE
// scoped to the composite PK only replaces within its own project.

// Positive: two projects, same relative file, same symbol, both must
// survive in their own project scope. The exact regression #1231
// described — flips RED→GREEN with the v28 migration.
func TestSymbol_CrossProjectIDCollision_BothMustSurvive(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	if err := store.UpsertProject(Project{ID: "proj-a", Name: "proj-a", Path: "/abs/path/a"}); err != nil {
		t.Fatalf("UpsertProject a: %v", err)
	}
	if err := store.UpsertProject(Project{ID: "proj-b", Name: "proj-b", Path: "/abs/path/b"}); err != nil {
		t.Fatalf("UpsertProject b: %v", err)
	}

	// Same relative file, same qualified name + kind in both projects.
	// Both projects' indexers produce the SAME id via MakeSymbolID.
	id := MakeSymbolID("internal/server/server.go", "server.handleSearch", "Method")

	symA := Symbol{
		ID:                   id,
		ProjectID:            "proj-a",
		FilePath:             "internal/server/server.go",
		Name:                 "handleSearch",
		QualifiedName:        "server.handleSearch",
		Kind:                 "Method",
		Language:             "Go",
		StartByte:            100, EndByte: 200,
		StartLine: 10, EndLine: 20,
		ExtractionConfidence: 1.0,
	}
	symB := symA
	symB.ProjectID = "proj-b"
	symB.StartByte = 300
	symB.EndByte = 400

	if err := store.BulkUpsertSymbols([]Symbol{symA}); err != nil {
		t.Fatalf("BulkUpsertSymbols a: %v", err)
	}
	if err := store.BulkUpsertSymbols([]Symbol{symB}); err != nil {
		t.Fatalf("BulkUpsertSymbols b: %v", err)
	}

	countA, err := countSymbolsForProject(store, "proj-a")
	if err != nil {
		t.Fatalf("count proj-a: %v", err)
	}
	countB, err := countSymbolsForProject(store, "proj-b")
	if err != nil {
		t.Fatalf("count proj-b: %v", err)
	}

	if countA != 1 {
		t.Errorf("proj-a lost its symbol after proj-b indexed the same id (THIS WAS THE #1231 BUG) — got %d, want 1", countA)
	}
	if countB != 1 {
		t.Errorf("proj-b symbol count = %d; want 1", countB)
	}

	// Cross-check the byte ranges so we know the right project's row
	// survived in each. Pre-v28, proj-a's row would have been overwritten
	// with proj-b's payload (project_id flipped) — but composite PK
	// keeps them as distinct rows.
	gotA, _ := store.GetSymbolScoped("proj-a", id)
	gotB, _ := store.GetSymbolScoped("proj-b", id)
	if gotA == nil || gotA.StartByte != 100 {
		t.Errorf("proj-a row contents wrong: got %+v; want StartByte=100", gotA)
	}
	if gotB == nil || gotB.StartByte != 300 {
		t.Errorf("proj-b row contents wrong: got %+v; want StartByte=300", gotB)
	}
}

// Negative: distinct symbols in distinct projects should never trigger
// any collision regardless of fix shape. Pinned as a baseline.
func TestSymbol_CrossProjectNoCollision_BothSurvive(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	if err := store.UpsertProject(Project{ID: "proj-a", Name: "proj-a", Path: "/abs/a"}); err != nil {
		t.Fatalf("UpsertProject a: %v", err)
	}
	if err := store.UpsertProject(Project{ID: "proj-b", Name: "proj-b", Path: "/abs/b"}); err != nil {
		t.Fatalf("UpsertProject b: %v", err)
	}

	symA := Symbol{
		ID:        MakeSymbolID("a.go", "pkgA.fn", "Function"),
		ProjectID: "proj-a", FilePath: "a.go", Name: "fn", QualifiedName: "pkgA.fn",
		Kind: "Function", Language: "Go", ExtractionConfidence: 1.0,
	}
	symB := Symbol{
		ID:        MakeSymbolID("b.go", "pkgB.fn", "Function"),
		ProjectID: "proj-b", FilePath: "b.go", Name: "fn", QualifiedName: "pkgB.fn",
		Kind: "Function", Language: "Go", ExtractionConfidence: 1.0,
	}
	if err := store.BulkUpsertSymbols([]Symbol{symA, symB}); err != nil {
		t.Fatalf("BulkUpsertSymbols: %v", err)
	}

	countA, _ := countSymbolsForProject(store, "proj-a")
	countB, _ := countSymbolsForProject(store, "proj-b")
	if countA != 1 || countB != 1 {
		t.Errorf("distinct-id baseline failed: proj-a=%d proj-b=%d; want 1,1", countA, countB)
	}
}

// Control: re-upserting the same symbol in the same project is
// idempotent. INSERT OR REPLACE inside one project's composite-PK
// scope is correct — same id + same project_id collides and replaces
// the row in-place.
func TestSymbol_SameProjectReUpsert_Idempotent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	if err := store.UpsertProject(Project{ID: "proj", Name: "proj", Path: "/abs"}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}

	sym := Symbol{
		ID:        MakeSymbolID("x.go", "fn", "Function"),
		ProjectID: "proj", FilePath: "x.go", Name: "fn", QualifiedName: "fn",
		Kind: "Function", Language: "Go", ExtractionConfidence: 1.0,
	}

	for i := 0; i < 3; i++ {
		if err := store.BulkUpsertSymbols([]Symbol{sym}); err != nil {
			t.Fatalf("upsert pass %d: %v", i, err)
		}
	}

	count, _ := countSymbolsForProject(store, "proj")
	if count != 1 {
		t.Errorf("3x re-upsert of same id produced count=%d; want 1 (idempotent)", count)
	}
}

func countSymbolsForProject(store *Store, projectID string) (int, error) {
	var n int
	err := store.db.QueryRow(`SELECT COUNT(*) FROM symbols WHERE project_id=?`, projectID).Scan(&n)
	return n, err
}
