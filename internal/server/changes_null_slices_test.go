// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #330: changes endpoint must return [] (not null) for impacted and
// changed_symbols when the diff has none. A nil slice marshals to JSON
// null, which forces every consumer to null-check before iterating.
// Same JSON-shape class as #328 on health.extraction_coverage.

// Code change with no callers → impacted must be non-nil empty array,
// serialised as [] in JSON (not null).
func TestHandleChanges_ImpactedIsEmptyArrayNotNull(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	repoDir := setupChangesGitRepo(t)
	store.UpsertProject(db.Project{ID: repoDir, Path: repoDir, Name: "impacted-empty", IndexedAt: time.Now()})
	srv.sessionID = repoDir
	srv.sessionRoot = repoDir

	// One changed Function with no inbound CALLS edges → no impacted.
	mustUpsertSymbols(t, store, []db.Symbol{
		{ID: "p::main.Foo#Function", ProjectID: repoDir, FilePath: "main.go", Name: "Foo",
			QualifiedName: "main.Foo", Kind: "Function", Language: "Go",
			StartByte: 13, EndByte: 30, StartLine: 2, EndLine: 2, ExtractionConfidence: 1.0},
	})

	result, err := srv.handleChanges(context.Background(), makeReq(map[string]any{"scope": "all"}))
	if err != nil {
		t.Fatalf("handleChanges: %v", err)
	}

	// Decode into a map and verify the *raw* JSON is "[]", not "null".
	// decode() returns a map[string]any where a nil slice becomes nil
	// (interface{}); check the raw bytes too so a future regression
	// from `nil → null` marshal still trips the test.
	body := decode(t, result)
	if v, present := body["impacted"]; !present {
		t.Fatal("impacted key missing from response")
	} else if v == nil {
		t.Errorf("impacted is null; want [] (non-nil empty array)")
	} else if arr, ok := v.([]any); !ok {
		t.Errorf("impacted has wrong type %T; want []any", v)
	} else if len(arr) != 0 {
		t.Errorf("impacted length = %d, want 0", len(arr))
	}

	// Verify raw JSON contains "impacted":[] not "impacted":null.
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	if strings.Contains(string(raw), `"impacted":null`) {
		t.Errorf("response JSON contains \"impacted\":null; want \"impacted\":[]\nfull: %s", raw)
	}
}

// changed_symbols must also default to [] (not null). Independent of
// impacted — the changed symbol list is built in a separate loop and
// has its own nil-slice failure mode.
func TestHandleChanges_ChangedSymbolsIsEmptyArrayNotNull(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	// A repo where no symbols match the diff (no symbols seeded at all)
	// — changed_symbols stays empty after the diff-to-symbol mapping pass.
	repoDir := setupChangesGitRepo(t)
	store.UpsertProject(db.Project{ID: repoDir, Path: repoDir, Name: "changed-empty", IndexedAt: time.Now()})
	srv.sessionID = repoDir
	srv.sessionRoot = repoDir

	result, err := srv.handleChanges(context.Background(), makeReq(map[string]any{"scope": "all"}))
	if err != nil {
		t.Fatalf("handleChanges: %v", err)
	}
	body := decode(t, result)
	if v, present := body["changed_symbols"]; !present {
		t.Fatal("changed_symbols key missing from response")
	} else if v == nil {
		t.Errorf("changed_symbols is null; want [] (non-nil empty array)")
	}
}
