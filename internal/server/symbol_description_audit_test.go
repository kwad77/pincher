// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// v0.65 description-honesty audit (continuation): the symbol tool's
// description named the ID format and recommended context/symbols
// alternatives, but didn't mention a load-bearing capability — the
// handler auto-resolves stale IDs via symbol_moves on file renames
// (#704 substrate). Agents who'd cached an ID across a file rename
// might have re-issued search by short name, when they could just
// retry the symbol call as-is.
//
// Table-from-the-start (#1152):
//   - Positive: description mentions symbol_moves / rename
//     resilience.
//   - Negative: description does not claim IDs only work pre-rename.
//   - Control: ResolveStaleID is still wired into handleSymbol —
//     handler-vs-description parity.
//   - Cross-check: a real handleSymbol call with a stale ID after
//     a recorded rename succeeds (end-to-end the feature works).

func TestSymbolDescription_MentionsRenameResilience(t *testing.T) {
	srv, _, _ := newTestServer(t)
	tool := srv.tools["symbol"]
	if tool == nil {
		t.Fatal("symbol tool not registered")
	}
	desc := tool.Description
	for _, want := range []string{"rename", "symbol_moves"} {
		if !strings.Contains(desc, want) {
			t.Errorf("symbol description missing %q\nGOT:\n%s", want, desc)
		}
	}
}

// Cross-check: handleSymbol still resolves stale IDs via
// symbol_moves end-to-end. Pre-fix a future refactor could drop
// the ResolveStaleID fallback without breaking the description
// claim until a real agent hit a rename.
func TestHandleSymbol_StaleIDResolvesViaSymbolMoves(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "p-symbol-rename"
	store.UpsertProject(db.Project{
		ID: pid, Path: t.TempDir(), Name: pid, IndexedAt: time.Now(),
	})
	srv.sessionID = pid

	// Seed a symbol at its new location (post-rename).
	newID := pid + "::pkg.Foo#Function"
	mustUpsertSymbols(t, store, []db.Symbol{
		{ID: newID, ProjectID: pid, FilePath: "new/loc.go",
			Name: "Foo", QualifiedName: "pkg.Foo", Kind: "Function", Language: "Go",
			ExtractionConfidence: 1.0},
	})

	// Record the move directly via the symbol_moves table. The
	// schema is documented in db.go v2→v3 migration; bypassing
	// the package-private writer keeps this test independent of
	// how indexer chooses to record moves.
	oldID := pid + "::pkg.Foo#Function (old path placeholder)"
	if _, err := store.DB().Exec(
		`INSERT INTO symbol_moves(old_id, new_id, project_id, moved_at) VALUES(?, ?, ?, ?)`,
		oldID, newID, pid, time.Now().Unix(),
	); err != nil {
		t.Fatalf("seed symbol_moves: %v", err)
	}

	// Issue a symbol call with the old ID — handler should
	// auto-redirect via ResolveStaleID and return the symbol at
	// the new location.
	result, err := srv.handleSymbol(context.Background(), makeReq(map[string]any{
		"id": oldID,
	}))
	if err != nil {
		t.Fatalf("handleSymbol: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success after symbol_moves redirect; got error %s", textOf(t, result))
	}
	body := decode(t, result)
	if name, _ := body["name"].(string); name != "Foo" {
		t.Errorf("resolved symbol name = %q, want Foo (rename redirect failed)", name)
	}
}
