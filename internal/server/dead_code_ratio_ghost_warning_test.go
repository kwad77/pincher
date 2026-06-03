// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #1071: companion to #1010/#1067/#1068. Strict #1044 dead_code ghost
// diagnosis fires only on edgeCount == 0. A ratio-class ghost project
// (substantial syms, vanishingly few edges) gets dead_code candidates
// that are disproportionately FPs from the un-resolved bulk. Warning
// fires alongside the result list so the response stays usable for
// the small subset that did resolve.

func TestHandleDeadCode_LowRatio_AttachesGhostWarning(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "p-dc-ratio"
	store.UpsertProject(db.Project{
		ID: pid, Path: t.TempDir(), Name: pid, IndexedAt: time.Now(),
		FileCount: 50, SymCount: 1001, EdgeCount: 1,
	})
	srv.sessionID = pid

	syms := []db.Symbol{}
	for i := 0; i < 2; i++ {
		syms = append(syms, db.Symbol{
			ID:                   pid + "::pkg.Dead" + string(rune('A'+(i%26))) + string(rune('A'+(i/26))) + "#Function",
			ProjectID:            pid,
			FilePath:             "a.go",
			Name:                 "Dead",
			QualifiedName:        "pkg.Dead",
			Kind:                 "Function",
			Language:             "Go",
			ExtractionConfidence: 1.0,
		})
	}
	mustUpsertSymbols(t, store, syms)
	if err := store.BulkUpsertEdges([]db.Edge{
		{ProjectID: pid, FromID: syms[0].ID, ToID: syms[1].ID, Kind: "CALLS", Confidence: 1.0},
	}); err != nil {
		t.Fatalf("BulkUpsertEdges: %v", err)
	}

	res, err := srv.handleDeadCode(context.Background(), makeReq(map[string]any{
		"min_confidence": 0.0,
	}))
	if err != nil {
		t.Fatalf("handleDeadCode: %v", err)
	}
	body := decode(t, res)
	meta, _ := body["_meta"].(map[string]any)
	if meta == nil {
		t.Fatalf("expected _meta envelope; got %v", body)
	}
	warnings, _ := meta["warnings"].([]any)
	found := false
	for _, w := range warnings {
		ws, _ := w.(string)
		if strings.Contains(ws, "ratio") && strings.Contains(ws, "ghost-extraction") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected ratio-ghost warning; got warnings=%v", warnings)
	}
}
