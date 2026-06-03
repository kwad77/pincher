// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #1103: when a pinchQL query passes min_confidence but its RETURN clause
// doesn't project extraction_confidence, the filter silently no-ops —
// every row passes through unfiltered. Pre-fix the caller had no way to
// learn the filter was inert; same silent-confidently-wrong family as
// #1094 (dead_code all-unknown kinds) / #1096 (trace all-unknown kinds).
// Now: a warning fires naming the no-op and pointing at the fix.

func TestHandleQuery_MinConfidenceNoOp_WarnsWhenReturnLacksConfidence(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "q-mc-noop"
	store.UpsertProject(db.Project{ID: pid, Path: "/tmp/" + pid, Name: pid, IndexedAt: time.Now()})
	srv.sessionID = pid
	srv.sessionRoot = "/tmp/" + pid
	mustUpsertSymbols(t, store, []db.Symbol{
		{ID: pid + "::pkg.Foo#Function", ProjectID: pid, FilePath: "x.go",
			Name: "Foo", QualifiedName: "pkg.Foo", Kind: "Function", Language: "Go",
			ExtractionConfidence: 0.5},
	})

	// RETURN n.name only — no extraction_confidence projected.
	// min_confidence=0.9 is meaningless against this row shape.
	res, err := srv.handleQuery(context.Background(), makeReq(map[string]any{
		"pinchql":        `MATCH (n:Function) RETURN n.name`,
		"min_confidence": 0.9,
	}))
	if err != nil {
		t.Fatalf("handleQuery: %v", err)
	}
	body := decode(t, res)
	ws := metaWarnings(t, body)
	if !warningsContain(ws, "no-op") {
		t.Errorf("expected a no-op warning naming the missed filter; got warnings: %v", ws)
	}
	if !warningsContain(ws, "extraction_confidence") {
		t.Errorf("warning should name the column to add to RETURN; got: %v", ws)
	}
	// The row still passes through — confirm the filter behavior didn't
	// change (the warning is the fix; passing rows through is the
	// documented contract).
	rows, _ := body["rows"].([]any)
	if len(rows) != 1 {
		t.Errorf("expected pass-through (1 row); got %d: %v", len(rows), body)
	}
}

// Control: a RETURN that DOES project extraction_confidence must NOT
// trip the no-op warning, even when min_confidence is set.
func TestHandleQuery_MinConfidenceProjected_NoWarning(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "q-mc-ok"
	store.UpsertProject(db.Project{ID: pid, Path: "/tmp/" + pid, Name: pid, IndexedAt: time.Now()})
	srv.sessionID = pid
	srv.sessionRoot = "/tmp/" + pid
	mustUpsertSymbols(t, store, []db.Symbol{
		{ID: pid + "::pkg.Foo#Function", ProjectID: pid, FilePath: "x.go",
			Name: "Foo", QualifiedName: "pkg.Foo", Kind: "Function", Language: "Go",
			ExtractionConfidence: 1.0},
	})

	res, err := srv.handleQuery(context.Background(), makeReq(map[string]any{
		"pinchql":        `MATCH (n:Function) RETURN n.name, n.extraction_confidence`,
		"min_confidence": 0.5,
	}))
	if err != nil {
		t.Fatalf("handleQuery: %v", err)
	}
	body := decode(t, res)
	for _, w := range metaWarnings(t, body) {
		if strings.Contains(w, "no-op") {
			t.Errorf("query that projects extraction_confidence must not trip no-op warning; got: %q", w)
		}
	}
}

// Empty-result queries already get the empty-result advisory — adding
// the no-op warning on top is redundant noise. Confirm the warning is
// suppressed when the underlying query returned zero rows.
func TestHandleQuery_MinConfidenceNoOp_SuppressedOnEmptyResult(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "q-mc-empty"
	store.UpsertProject(db.Project{ID: pid, Path: "/tmp/" + pid, Name: pid, IndexedAt: time.Now()})
	srv.sessionID = pid
	srv.sessionRoot = "/tmp/" + pid

	res, err := srv.handleQuery(context.Background(), makeReq(map[string]any{
		"pinchql":        `MATCH (n:Function) WHERE n.name = "definitely_not_real_xyz" RETURN n.name`,
		"min_confidence": 0.9,
	}))
	if err != nil {
		t.Fatalf("handleQuery: %v", err)
	}
	body := decode(t, res)
	for _, w := range metaWarnings(t, body) {
		if strings.Contains(w, "no-op") {
			t.Errorf("empty-result query must not also trip no-op warning (redundant noise); got: %q", w)
		}
	}
}
