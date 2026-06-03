// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
	"github.com/kwad77/pincher/internal/index"
)

// #1066: symbols batch lumped found and not-found into a single `count`
// field. The per-id `error:"not found"` stub was buried in the symbols
// array — agents reading top-level fields concluded all IDs resolved.
// Now: explicit not_found_ids + count_found + count_not_found and a
// warning naming the misses with a `search` recovery hint.

func TestHandleSymbols_PartialMiss_SurfacesNotFoundIDsAndCounts(t *testing.T) {
	t.Parallel()
	srv, store, root := newTestServer(t)
	srv.sessionRoot = root
	srv.sessionID = "snfsum"
	store.UpsertProject(db.Project{
		ID: "snfsum", Path: root, Name: "snfsum", IndexedAt: time.Now(),
	})
	writeGoFile(t, root, "x.go", `package x
func Real() {}
`)
	idx := index.New(store)
	result, err := idx.Index(context.Background(), root, false)
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	indexedProjectID := result.ProjectID

	realID := "x.go::x.Real#Function"
	bogusID := "x.go::x.MissingFunc#Function"

	res, err := srv.handleSymbols(context.Background(), makeReq(map[string]any{
		"ids":     []any{realID, bogusID},
		"project": indexedProjectID,
	}))
	if err != nil {
		t.Fatalf("handleSymbols: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success (partial hit not an error); got %s", textOf(t, res))
	}
	body := decode(t, res)

	// not_found_ids must list exactly the bogus ID.
	notFoundRaw, ok := body["not_found_ids"].([]any)
	if !ok {
		t.Fatalf("expected not_found_ids array on partial-miss; got %v", body["not_found_ids"])
	}
	if len(notFoundRaw) != 1 {
		t.Fatalf("expected 1 entry in not_found_ids; got %d (%v)", len(notFoundRaw), notFoundRaw)
	}
	if s, _ := notFoundRaw[0].(string); s != bogusID {
		t.Errorf("expected not_found_ids[0]=%q; got %q", bogusID, s)
	}

	// count_found / count_not_found split must add up to ids length.
	cf, _ := body["count_found"].(float64)
	cnf, _ := body["count_not_found"].(float64)
	if int(cf) != 1 || int(cnf) != 1 {
		t.Errorf("expected count_found=1 count_not_found=1; got %v/%v", cf, cnf)
	}

	// _meta.warnings must name the bogus ID and reference `search` as recovery.
	meta, _ := body["_meta"].(map[string]any)
	warnings, _ := meta["warnings"].([]any)
	found := false
	for _, w := range warnings {
		ws, _ := w.(string)
		if strings.Contains(ws, bogusID) && strings.Contains(ws, "search") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected warning naming %q with `search` recovery hint; got warnings=%v", bogusID, warnings)
	}
}

// Control: a fully-resolving batch must NOT surface not_found_ids,
// count_found, or count_not_found fields. Keeps the response shape
// minimal when nothing went wrong.
func TestHandleSymbols_AllFound_NoNotFoundFields(t *testing.T) {
	t.Parallel()
	srv, store, root := newTestServer(t)
	srv.sessionRoot = root
	srv.sessionID = "snfsumok"
	store.UpsertProject(db.Project{
		ID: "snfsumok", Path: root, Name: "snfsumok", IndexedAt: time.Now(),
	})
	writeGoFile(t, root, "x.go", `package x
func A() {}
func B() {}
`)
	idx := index.New(store)
	result, err := idx.Index(context.Background(), root, false)
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	indexedProjectID := result.ProjectID

	res, err := srv.handleSymbols(context.Background(), makeReq(map[string]any{
		"ids":     []any{"x.go::x.A#Function", "x.go::x.B#Function"},
		"project": indexedProjectID,
	}))
	if err != nil {
		t.Fatalf("handleSymbols: %v", err)
	}
	body := decode(t, res)
	if _, ok := body["not_found_ids"]; ok {
		t.Error("not_found_ids must be omitted when every id resolved")
	}
	if _, ok := body["count_not_found"]; ok {
		t.Error("count_not_found must be omitted when every id resolved")
	}
}
