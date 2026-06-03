// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #1094: pre-fix, dead_code with kinds=BogusKind dropped the unknown
// value from the filter (treating an all-bogus list as "no filter")
// and returned dead symbols from EVERY kind — contradicting the
// caller's intent. The warning named the bad kind in
// _meta.warnings, but the response contradicted the warning. Now: an
// all-unknown-kinds filter rides through to SQL (which returns 0) and
// the empty-result branch emits a kind-filter-specific diagnosis.

func TestHandleDeadCode_AllKindsUnknown_DiagnosisNamesFilter(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "p-dc-kind"
	store.UpsertProject(db.Project{
		ID: pid, Path: t.TempDir(), Name: pid, IndexedAt: time.Now(),
		FileCount: 5, SymCount: 5, EdgeCount: 1,
	})
	srv.sessionID = pid

	syms := []db.Symbol{}
	for i := 0; i < 5; i++ {
		syms = append(syms, db.Symbol{
			ID:                   pid + "::pkg.F" + string(rune('A'+i)) + "#Function",
			ProjectID:            pid,
			FilePath:             "a.go",
			Name:                 "F",
			QualifiedName:        "pkg.F",
			Kind:                 "Function",
			Language:             "Go",
			ExtractionConfidence: 1.0,
		})
	}
	mustUpsertSymbols(t, store, syms)

	res, err := srv.handleDeadCode(context.Background(), makeReq(map[string]any{
		"kinds": "BogusKind",
	}))
	if err != nil {
		t.Fatalf("handleDeadCode: %v", err)
	}
	body := decode(t, res)

	// Pre-fix the symbols list would have contained Function entries
	// (the filter was silently dropped). Post-fix: 0 results.
	dead, _ := body["dead_symbols"].([]any)
	if len(dead) != 0 {
		t.Errorf("expected 0 results when every kind is unknown; got %d (filter was silently dropped pre-fix)", len(dead))
	}

	meta, _ := body["_meta"].(map[string]any)
	if meta == nil {
		t.Fatalf("expected _meta envelope; got %v", body)
	}
	diagnosis, _ := meta["diagnosis"].(string)
	if !strings.Contains(diagnosis, "BogusKind") {
		t.Errorf("diagnosis must name the bad kind value; got %q", diagnosis)
	}
	if strings.Contains(diagnosis, "min_confidence") {
		t.Errorf("diagnosis must NOT suggest min_confidence (filter is the real cause); got %q", diagnosis)
	}
	if !strings.Contains(diagnosis, "Function") {
		t.Errorf("diagnosis must list Function as an available kind; got %q", diagnosis)
	}
}

// Control: a MIXED kinds list (one valid, one bogus) keeps the
// existing per-kind warning behavior — the valid kind survives, the
// bogus one is dropped with a warning. The bad-kind diagnosis branch
// must NOT fire here.
func TestHandleDeadCode_MixedKinds_DropsBadKeepsGood(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "p-dc-mixed"
	store.UpsertProject(db.Project{
		ID: pid, Path: t.TempDir(), Name: pid, IndexedAt: time.Now(),
		FileCount: 1, SymCount: 1, EdgeCount: 1,
	})
	srv.sessionID = pid
	mustUpsertSymbols(t, store, []db.Symbol{
		{
			ID:                   pid + "::pkg.F#Function",
			ProjectID:            pid,
			FilePath:             "a.go",
			Name:                 "F",
			QualifiedName:        "pkg.F",
			Kind:                 "Function",
			Language:             "Go",
			ExtractionConfidence: 1.0,
		},
	})

	res, err := srv.handleDeadCode(context.Background(), makeReq(map[string]any{
		"kinds": "Function,BogusKind",
	}))
	if err != nil {
		t.Fatalf("handleDeadCode: %v", err)
	}
	body := decode(t, res)
	meta, _ := body["_meta"].(map[string]any)
	if meta == nil {
		return
	}
	diagnosis, _ := meta["diagnosis"].(string)
	// Mixed kinds must NOT trip the all-unknown diagnosis — Function
	// is still in the filter and rides through to SQL.
	if strings.Contains(diagnosis, "every value in kinds") {
		t.Errorf("mixed kinds (one valid) must not trip all-unknown diagnosis; got %q", diagnosis)
	}
}
