// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #925: when the indexer is mid-pass (binary-swap restart, first
// index after server start, watcher-triggered reindex), tool results
// were silently incomplete with no `_meta` flag, no diagnosis. Now
// jsonResultWithMeta injects `index_in_progress` + a warning when
// idx.GetProgress reports the session project as active.

// Idle-state baseline: no index_in_progress field, no
// "indexer is mid-pass" warning. Pin the non-regression so the flag
// doesn't accidentally start firing on every call.
func TestJsonResultWithMeta_IdleIndexer_NoInProgressFlag(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p925-idle"
	store.UpsertProject(db.Project{ID: "p925-idle", Path: "/tmp/p925-idle", Name: "p925-idle", IndexedAt: time.Now()})
	store.BulkUpsertSymbols([]db.Symbol{
		{ID: "s1", ProjectID: "p925-idle", FilePath: "a.go", Name: "Foo",
			QualifiedName: "pkg.Foo", Kind: "Function", Language: "Go",
			StartByte: 0, EndByte: 100, StartLine: 1, EndLine: 5,
			ExtractionConfidence: 1.0},
	})

	result, err := srv.handleSearch(context.Background(), makeReq(map[string]any{
		"query": "Foo",
	}))
	if err != nil {
		t.Fatalf("handleSearch: %v", err)
	}
	body := decode(t, result)
	meta, _ := body["_meta"].(map[string]any)
	if meta == nil {
		t.Fatal("_meta missing")
	}
	if _, present := meta["index_in_progress"]; present {
		t.Errorf("idle indexer must not emit index_in_progress; got meta=%v", meta)
	}
	warnings, _ := meta["warnings"].([]any)
	for _, w := range warnings {
		s, _ := w.(string)
		if strings.Contains(s, "indexer is mid-pass") {
			t.Errorf("idle indexer must not warn about mid-pass; got %q", s)
		}
	}
}
