// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #453: FTS5's default operator between bare tokens is implicit AND.
// Multi-token unquoted queries that miss because no single symbol
// matches all terms used to silently return 0. The handler now retries
// once with " OR " joining the per-token-sanitised tokens.
//
// Repro shape from the dogfood report: query "Watch poll" returned 0
// because no symbol matched both terms, even though `Watch` alone
// surfaces 5 hits. The AND→OR fallback recovers.

func TestHandleSearch_MultiTokenAndFallsBackToOr(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := t.TempDir()
	store.UpsertProject(db.Project{ID: pid, Path: pid, Name: "and-or-fallback", IndexedAt: time.Now()})
	srv.sessionID = pid
	srv.sessionRoot = pid

	// Seed: two symbols, one matching each token individually. No symbol
	// matches BOTH "watch" and "poll" tokens — the AND default returns 0.
	// The OR fallback must surface at least the Watch hit.
	mustUpsertSymbols(t, store, []db.Symbol{
		{
			ID: "p::index.*Indexer.Watch#Method", ProjectID: pid,
			FilePath: "internal/index/indexer.go", Name: "Watch",
			QualifiedName: "index.*Indexer.Watch", Kind: "Method", Language: "Go",
			StartByte: 0, EndByte: 100, StartLine: 1, EndLine: 5,
			ExtractionConfidence: 1.0,
		},
		{
			ID: "p::server.handlePoll#Function", ProjectID: pid,
			FilePath: "internal/server/poll.go", Name: "handlePoll",
			QualifiedName: "server.handlePoll", Kind: "Function", Language: "Go",
			StartByte: 0, EndByte: 100, StartLine: 1, EndLine: 5,
			ExtractionConfidence: 1.0,
		},
	})

	result, err := srv.handleSearch(context.Background(), makeReq(map[string]any{
		"query":   "Watch poll",
		"project": pid,
	}))
	if err != nil {
		t.Fatalf("handleSearch: %v", err)
	}
	body := decode(t, result)

	count, _ := body["count"].(float64)
	if count == 0 {
		t.Errorf("AND→OR fallback should surface ≥1 result; got count=0 body=%v", body)
	}

	meta, _ := body["_meta"].(map[string]any)
	if meta == nil {
		t.Fatal("_meta missing from response")
	}
	if got, _ := meta["and_fallback_to_or"].(bool); !got {
		t.Errorf("expected _meta.and_fallback_to_or=true after recovery; got meta=%v", meta)
	}
	effective, _ := meta["effective_query"].(string)
	if !strings.Contains(effective, " OR ") {
		t.Errorf("expected _meta.effective_query to contain ' OR '; got %q", effective)
	}
}

// When the user types AND/OR/NOT operators explicitly, the sanitizer
// phrase-wraps them today (#452, separate issue). The AND→OR fallback
// must NOT fire in that case — the user already made an explicit choice
// and a hidden retry would confuse them further.
func TestHandleSearch_AndOrFallback_SkippedWhenUserUsesOperator(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := t.TempDir()
	store.UpsertProject(db.Project{ID: pid, Path: pid, Name: "explicit-op", IndexedAt: time.Now()})
	srv.sessionID = pid
	srv.sessionRoot = pid

	mustUpsertSymbols(t, store, []db.Symbol{
		{
			ID: "p::pkg.Foo#Function", ProjectID: pid,
			FilePath: "pkg/foo.go", Name: "Foo",
			QualifiedName: "pkg.Foo", Kind: "Function", Language: "Go",
			StartByte: 0, EndByte: 100, StartLine: 1, EndLine: 5,
			ExtractionConfidence: 1.0,
		},
	})

	// Query with explicit OR operator. The sanitizer wraps this as a
	// phrase today, so 0 results is expected. The fallback must not
	// re-rewrite — let the explicit query path stand.
	result, err := srv.handleSearch(context.Background(), makeReq(map[string]any{
		"query":   "Foo OR Bar",
		"project": pid,
	}))
	if err != nil {
		t.Fatalf("handleSearch: %v", err)
	}
	body := decode(t, result)
	meta, _ := body["_meta"].(map[string]any)
	if meta == nil {
		t.Fatal("_meta missing")
	}
	if got, _ := meta["and_fallback_to_or"].(bool); got {
		t.Errorf("and_fallback_to_or should NOT fire when user supplied an explicit operator; got meta=%v", meta)
	}
}

// When the user explicitly quotes a phrase, the fallback must not fire
// — they asked for an exact phrase, not an OR over the tokens.
func TestHandleSearch_AndOrFallback_SkippedWhenUserQuoted(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := t.TempDir()
	store.UpsertProject(db.Project{ID: pid, Path: pid, Name: "quoted-query", IndexedAt: time.Now()})
	srv.sessionID = pid
	srv.sessionRoot = pid

	mustUpsertSymbols(t, store, []db.Symbol{
		{
			ID: "p::pkg.Watch#Method", ProjectID: pid,
			FilePath: "pkg/watch.go", Name: "Watch",
			QualifiedName: "pkg.Watch", Kind: "Method", Language: "Go",
			StartByte: 0, EndByte: 100, StartLine: 1, EndLine: 5,
			ExtractionConfidence: 1.0,
		},
	})

	result, err := srv.handleSearch(context.Background(), makeReq(map[string]any{
		"query":   `"Watch poll"`,
		"project": pid,
	}))
	if err != nil {
		t.Fatalf("handleSearch: %v", err)
	}
	body := decode(t, result)
	meta, _ := body["_meta"].(map[string]any)
	if meta == nil {
		t.Fatal("_meta missing")
	}
	if got, _ := meta["and_fallback_to_or"].(bool); got {
		t.Errorf("and_fallback_to_or should NOT fire for explicit phrase query; got meta=%v", meta)
	}
}
