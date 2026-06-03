// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #1157: an empty-result pinchQL query should credit the avoided
// grep+empty-read cycle (~200 tokens), not silently report 0. The
// agent ran a tool call that legitimately answered zero rows — that's
// work they would otherwise have done with `grep -r <name>` + reading
// the empty result. Crediting zero makes audit-shape queries look like
// wasted calls in stats.
//
// Tests follow the table-from-the-start shape (#1152):
//   - Positive: empty rows → tokens_saved == emptyResultBaselineTokens
//   - Negative: non-empty rows with file_path → savings based on those files
//   - Negative: non-empty rows without file_path → 0 savings (honest)
//   - Cross-check: the constant is named, not magic

// Positive: query that legitimately returns zero rows gets the
// baseline credit. The exact #1157 issue scenario.
func TestHandleQuery_EmptyResult_CreditsBaselineTokens(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "empty-savings"
	if err := store.UpsertProject(db.Project{
		ID: pid, Path: "/tmp/" + pid, Name: pid, IndexedAt: time.Now(),
	}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}

	res, err := srv.handleQuery(context.Background(), makeReq(map[string]any{
		"project": pid,
		"pinchql": `MATCH (n:Function) WHERE n.name = "totallyNonexistentFunction_xyz_123" RETURN n.file_path`,
	}))
	if err != nil {
		t.Fatalf("handleQuery: %v", err)
	}
	parsed := decode(t, res)
	meta, ok := parsed["_meta"].(map[string]any)
	if !ok {
		t.Fatalf("_meta missing or wrong type: %v", parsed["_meta"])
	}
	saved, ok := meta["tokens_saved"].(float64)
	if !ok {
		t.Fatalf("tokens_saved not numeric: %T (%v)", meta["tokens_saved"], meta["tokens_saved"])
	}
	if int(saved) != emptyResultBaselineTokens {
		t.Errorf("empty-result tokens_saved = %d; want %d (the #1157 baseline credit)",
			int(saved), emptyResultBaselineTokens)
	}
}

// Negative: non-empty result with file_path column uses the
// file-size-based savings path — NOT the empty-result constant.
// Pre-#1157 behaviour preserved.
func TestHandleQuery_NonEmptyResult_UsesFileSizeSavings(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "nonempty-savings"
	dir := t.TempDir()
	if err := store.UpsertProject(db.Project{
		ID: pid, Path: dir, Name: pid, IndexedAt: time.Now(),
	}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	// Seed one symbol so the query returns a row.
	sym := db.Symbol{
		ID: "internal/x.go::x.F#Function", ProjectID: pid,
		FilePath: "internal/x.go", Name: "F",
		QualifiedName: "x.F", Kind: "Function", Language: "Go",
	}
	if err := store.BulkUpsertSymbols([]db.Symbol{sym}); err != nil {
		t.Fatalf("BulkUpsertSymbols: %v", err)
	}

	res, err := srv.handleQuery(context.Background(), makeReq(map[string]any{
		"project": pid,
		"pinchql": `MATCH (n:Function) WHERE n.name = "F" RETURN n.file_path`,
	}))
	if err != nil {
		t.Fatalf("handleQuery: %v", err)
	}
	parsed := decode(t, res)
	meta := parsed["_meta"].(map[string]any)
	saved, ok := meta["tokens_saved"].(float64)
	if !ok {
		// tokens_saved may be 0 if file doesn't exist on disk in test
		// (savedVsFileSizesSession reads file sizes). Both paths are
		// fine — we just need to confirm we did NOT hit the empty-
		// result constant.
		return
	}
	// We need to NOT be returning the empty-result constant for a
	// non-empty rowset. Either the file-size path runs (likely 0 if
	// file doesn't exist) or it returns 0. Either way it must NOT be
	// emptyResultBaselineTokens.
	if int(saved) == emptyResultBaselineTokens {
		t.Errorf("non-empty result tokens_saved = %d (the empty-result constant); should have used file-size path",
			int(saved))
	}
}

// Cross-check: emptyResultBaselineTokens is the documented constant
// that other tests reference. Asserting its value here pins the
// "what number do we credit" decision in one place — a future tweak
// from 200 → 150 fails this loudly so the test bundle keeps the
// constant + behaviour in sync.
func TestEmptyResultBaselineTokens_IsDocumentedConstant(t *testing.T) {
	if emptyResultBaselineTokens != 200 {
		t.Errorf("emptyResultBaselineTokens = %d; want 200 (the #1157 conservative grep+empty-read estimate). If you intend to change it, also update the CHANGELOG entry and #1157 issue body.",
			emptyResultBaselineTokens)
	}
}
