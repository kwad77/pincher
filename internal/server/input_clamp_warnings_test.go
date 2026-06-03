// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #712 Group B: input-clamp warnings. Negative / out-of-range page
// params were previously coerced silently — the caller got a different
// page size than it asked for with no signal. These tests pin that the
// clamp now surfaces in _meta.warnings.

func metaWarnings(t *testing.T, body map[string]any) []string {
	t.Helper()
	meta, _ := body["_meta"].(map[string]any)
	if meta == nil {
		return nil
	}
	raw, _ := meta["warnings"].([]any)
	out := make([]string, 0, len(raw))
	for _, w := range raw {
		if s, ok := w.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func warningsContain(ws []string, substr string) bool {
	for _, w := range ws {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}

func TestSearch_NegativeLimitWarns(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p"
	store.UpsertProject(db.Project{ID: "p", Path: "/tmp/p", Name: "p", IndexedAt: time.Now(), EdgeCount: 1})

	res, err := srv.handleSearch(context.Background(), makeReq(map[string]any{
		"query": "anything",
		"limit": -5,
	}))
	if err != nil {
		t.Fatalf("handleSearch: %v", err)
	}
	body := decode(t, res)
	ws := metaWarnings(t, body)
	if !warningsContain(ws, "limit=-5 clamped") {
		t.Errorf("expected a limit-clamp warning for limit=-5; got warnings: %v", ws)
	}
}

func TestSearch_OversizeLimitWarns(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p"
	store.UpsertProject(db.Project{ID: "p", Path: "/tmp/p", Name: "p", IndexedAt: time.Now(), EdgeCount: 1})

	res, err := srv.handleSearch(context.Background(), makeReq(map[string]any{
		"query": "anything",
		"limit": 9999999,
	}))
	if err != nil {
		t.Fatalf("handleSearch: %v", err)
	}
	body := decode(t, res)
	ws := metaWarnings(t, body)
	if !warningsContain(ws, "clamped to 500") {
		t.Errorf("expected a limit-clamp warning capping at 500; got warnings: %v", ws)
	}
	// And the effective limit in the response body reflects the clamp.
	if lim, _ := body["limit"].(float64); int(lim) != 500 {
		t.Errorf("response limit = %v, want 500", body["limit"])
	}
}

func TestSearch_ValidLimitNoWarning(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p"
	store.UpsertProject(db.Project{ID: "p", Path: "/tmp/p", Name: "p", IndexedAt: time.Now(), EdgeCount: 1})

	res, err := srv.handleSearch(context.Background(), makeReq(map[string]any{
		"query": "anything",
		"limit": 25,
	}))
	if err != nil {
		t.Fatalf("handleSearch: %v", err)
	}
	body := decode(t, res)
	for _, w := range metaWarnings(t, body) {
		if strings.Contains(w, "limit") {
			t.Errorf("valid limit=25 should not produce a limit warning; got: %q", w)
		}
	}
}

func TestList_NegativeActiveWithinDaysWarns(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	store.UpsertProject(db.Project{ID: "p", Path: t.TempDir(), Name: "p", IndexedAt: time.Now(), EdgeCount: 1})

	res, err := srv.handleList(context.Background(), makeReq(map[string]any{
		"active_within_days": -7,
	}))
	if err != nil {
		t.Fatalf("handleList: %v", err)
	}
	body := decode(t, res)
	ws := metaWarnings(t, body)
	if !warningsContain(ws, "active_within_days=-7 ignored") {
		t.Errorf("expected an active_within_days warning for -7; got warnings: %v", ws)
	}
}

func TestList_NegativeLimitWarns(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	store.UpsertProject(db.Project{ID: "p", Path: t.TempDir(), Name: "p", IndexedAt: time.Now(), EdgeCount: 1})

	res, err := srv.handleList(context.Background(), makeReq(map[string]any{
		"limit": -5,
	}))
	if err != nil {
		t.Fatalf("handleList: %v", err)
	}
	body := decode(t, res)
	ws := metaWarnings(t, body)
	if !warningsContain(ws, "limit=-5 treated as unbounded") {
		t.Errorf("expected a negative-limit warning; got warnings: %v", ws)
	}
}

func TestTrace_UnknownEdgeKindWarns(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	store.UpsertProject(db.Project{ID: "p", Path: "/tmp/p", Name: "p", EdgeCount: 1})
	srv.sessionID = "p"
	store.BulkUpsertSymbols([]db.Symbol{
		{ID: "x.go::pkg.Seed#Function", ProjectID: "p", Name: "Seed", QualifiedName: "pkg.Seed", Kind: "Function", FilePath: "x.go"},
	})

	res, err := srv.handleTrace(context.Background(), makeReq(map[string]any{
		"name":  "Seed",
		"kinds": "INVALID_EDGE_KIND",
	}))
	if err != nil {
		t.Fatalf("handleTrace: %v", err)
	}
	// #1096: all-unknown edge kinds now fail loudly with a rich
	// envelope (was: warn-but-proceed, which silently widened to
	// all-edge-kinds). The error string still names the bad value;
	// the warning shape is preserved in the rich envelope.
	if !res.IsError {
		t.Fatalf("expected IsError on all-unknown kinds; got: %s", textOf(t, res))
	}
	body := decode(t, res)
	errStr, _ := body["error"].(string)
	if !strings.Contains(errStr, "INVALID_EDGE_KIND") {
		t.Errorf("expected error to name the bad kind; got %q", errStr)
	}
}

func TestTrace_DepthOverMaxClamps(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	store.UpsertProject(db.Project{ID: "p", Path: "/tmp/p", Name: "p", EdgeCount: 1})
	srv.sessionID = "p"
	store.BulkUpsertSymbols([]db.Symbol{
		{ID: "x.go::pkg.Seed#Function", ProjectID: "p", Name: "Seed", QualifiedName: "pkg.Seed", Kind: "Function", FilePath: "x.go"},
	})

	res, err := srv.handleTrace(context.Background(), makeReq(map[string]any{
		"name":  "Seed",
		"depth": 99,
	}))
	if err != nil {
		t.Fatalf("handleTrace: %v", err)
	}
	body := decode(t, res)
	ws := metaWarnings(t, body)
	if !warningsContain(ws, "depth=99 clamped to 5") {
		t.Errorf("expected a depth-clamp warning for depth=99; got warnings: %v", ws)
	}
}
