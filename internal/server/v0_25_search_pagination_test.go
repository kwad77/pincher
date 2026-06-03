// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/kwad77/pincher/internal/db"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// #532: search accepts limit + offset and returns
// {results, total, has_more, offset, limit, count, ...}.
func TestSearch_Pagination(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p1"
	if err := store.UpsertProject(db.Project{ID: "p1", Path: "/tmp/p1", Name: "p1"}); err != nil {
		t.Fatalf("project: %v", err)
	}

	// Seed 30 symbols all matching "TestPaginate" so BM25 returns all 30.
	syms := make([]db.Symbol, 0, 30)
	for i := 0; i < 30; i++ {
		syms = append(syms, db.Symbol{
			ID:                   fmt.Sprintf("s::TestPaginate%02d#Function", i),
			ProjectID:            "p1",
			FilePath:             fmt.Sprintf("file_%02d.go", i),
			Name:                 fmt.Sprintf("TestPaginate%02d", i),
			QualifiedName:        fmt.Sprintf("pkg.TestPaginate%02d", i),
			Kind:                 "Function",
			Language:             "Go",
			ExtractionConfidence: 1,
		})
	}
	if err := store.BulkUpsertSymbols(syms); err != nil {
		t.Fatalf("seed: %v", err)
	}

	call := func(t *testing.T, args map[string]any) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(args)
		req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{
			Name: "search", Arguments: raw,
		}}
		result, err := srv.handleSearch(context.Background(), req)
		if err != nil {
			t.Fatalf("handleSearch: %v", err)
		}
		if result.IsError {
			t.Fatalf("IsError: %s", textOf(t, result))
		}
		var resp map[string]any
		if err := json.Unmarshal([]byte(textOf(t, result)), &resp); err != nil {
			t.Fatalf("decode: %v\nbody: %s", err, textOf(t, result))
		}
		return resp
	}

	t.Run("first-page-limit-10", func(t *testing.T) {
		// `TestPaginate*` prefix wildcard — FTS5 tokenizer treats each
		// `TestPaginate##` name as one token, so a bare `TestPaginate`
		// query wouldn't match. The `*` makes it a prefix scan.
		resp := call(t, map[string]any{"query": "TestPaginate*", "limit": 10})
		assertSearchEnvelope(t, resp, 10, 0, 10, 30, true)
	})

	t.Run("second-page-offset-10", func(t *testing.T) {
		resp := call(t, map[string]any{"query": "TestPaginate*", "limit": 10, "offset": 10})
		assertSearchEnvelope(t, resp, 10, 10, 10, 30, true)
	})

	t.Run("last-page-offset-25", func(t *testing.T) {
		resp := call(t, map[string]any{"query": "TestPaginate*", "limit": 10, "offset": 25})
		// 5 results remaining; has_more depends on whether the underlying
		// fetch saturated the cap. With 30 seeded rows + (10+25)*1=35
		// fetchLimit, we ask for 35 and get 30, so has_more should be
		// false.
		assertSearchEnvelope(t, resp, 5, 25, 10, 30, false)
	})

	t.Run("offset-past-end", func(t *testing.T) {
		resp := call(t, map[string]any{"query": "TestPaginate", "limit": 10, "offset": 9999})
		// With offset > 5000 the server clamps to 5000 — still past 30 rows.
		count, _ := resp["count"].(float64)
		if int(count) != 0 {
			t.Errorf("count = %v, want 0 for past-end offset", count)
		}
		results, _ := resp["results"].([]any)
		if len(results) != 0 {
			t.Errorf("results len = %d, want 0", len(results))
		}
	})

	t.Run("limit-cap-500", func(t *testing.T) {
		// limit=10000 must clamp to 500.
		resp := call(t, map[string]any{"query": "TestPaginate*", "limit": 10000})
		gotLimit, _ := resp["limit"].(float64)
		if int(gotLimit) != 500 {
			t.Errorf("limit echo = %v, want 500 (cap)", gotLimit)
		}
	})

	t.Run("results-never-null", func(t *testing.T) {
		// #334-class invariant: empty result page must be []. The raw
		// JSON body is the source of truth — once decoded the type is
		// already []any{}.
		raw, _ := json.Marshal(map[string]any{"query": "no-such-symbol-anywhere", "project": "p1"})
		req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{
			Name: "search", Arguments: raw,
		}}
		result, err := srv.handleSearch(context.Background(), req)
		if err != nil {
			t.Fatalf("handleSearch: %v", err)
		}
		body := textOf(t, result)
		if strings.Contains(body, `"results":null`) {
			t.Errorf("results serialized as null on empty match\nbody: %s", body)
		}
	})
}

func assertSearchEnvelope(t *testing.T, resp map[string]any, wantCount, wantOffset, wantLimit, wantTotal int, wantHasMore bool) {
	t.Helper()
	results, ok := resp["results"].([]any)
	if !ok {
		t.Fatalf("results not array; got %T", resp["results"])
	}
	if len(results) != wantCount {
		t.Errorf("len(results) = %d, want %d", len(results), wantCount)
	}
	if got, _ := resp["count"].(float64); int(got) != wantCount {
		t.Errorf("count = %v, want %d", got, wantCount)
	}
	if got, _ := resp["offset"].(float64); int(got) != wantOffset {
		t.Errorf("offset echo = %v, want %d", got, wantOffset)
	}
	if got, _ := resp["limit"].(float64); int(got) != wantLimit {
		t.Errorf("limit echo = %v, want %d", got, wantLimit)
	}
	if got, _ := resp["total"].(float64); int(got) != wantTotal {
		t.Errorf("total = %v, want %d", got, wantTotal)
	}
	if got, _ := resp["has_more"].(bool); got != wantHasMore {
		t.Errorf("has_more = %v, want %v", got, wantHasMore)
	}
}

