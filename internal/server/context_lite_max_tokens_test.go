// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// hook-redirect-v2 (as integrated with loop-substrate PR-5):
// `context lite=true max_tokens=N` truncates source to the budget via
// the shared line-boundary truncateSourceToTokens, announced with a
// truncated:true flag + a structured budget_truncated warning. The
// PreToolUse hook derives N from the intercepted Read call's own limit
// param, so the redirect can never cost more window than the Read it
// replaced. These tests pin:
//   - over-budget source is truncated, announced, and flagged
//   - within-budget source passes through untouched, no flag
//   - max_tokens without lite applies the general per-call budget
//     (loop-substrate semantics — full shape preserved, primary
//     source cut). NOTE: the original hook-redirect-v2 branch had
//     max_tokens ignored when lite=false; that claim did not survive
//     integration with feat/meta-max-tokens, which budgets the full
//     path too.

// liteFullSource fetches the canonical un-budgeted lite source for the
// seed symbol, so budget tests can assert prefix/equality against what
// the server actually serves for its byte span rather than a
// hand-maintained constant.
func liteFullSource(t *testing.T, srv *Server, id string) string {
	t.Helper()
	req := makeReq(map[string]any{"id": id, "lite": true})
	req.Params.Name = "context"
	result, err := srv.handleContext(context.Background(), req)
	if err != nil || result.IsError {
		t.Fatalf("handleContext (full lite fetch): err=%v isErr=%v", err, result.IsError)
	}
	m := decode(t, result)
	src, _ := m["source"].(string)
	if src == "" {
		t.Fatal("canonical lite source fetch returned empty source")
	}
	return src
}

func TestHandleContext_LiteMaxTokens_TruncatesOverBudget(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	sym, _ := newLiteTestSymbol(t, srv)
	full := liteFullSource(t, srv, sym.ID)

	// The seed symbol's source is ~55 bytes ≈ 14 tokens; a 5-token
	// budget (20 bytes) must force truncation.
	req := makeReq(map[string]any{"id": sym.ID, "lite": true, "max_tokens": 5})
	req.Params.Name = "context"
	result, err := srv.handleContext(context.Background(), req)
	if err != nil || result.IsError {
		t.Fatalf("handleContext: err=%v isErr=%v", err, result.IsError)
	}
	m := decode(t, result)
	src, _ := m["source"].(string)
	if src == "" {
		t.Fatal("source missing from budgeted lite response")
	}
	if truncated, _ := m["truncated"].(bool); !truncated {
		t.Errorf("truncated flag missing or false; got %v", m["truncated"])
	}
	// The cut must respect the token budget and land on a line
	// boundary (prefix of the original source).
	if got := db.ApproxTokens(src); got > 5 {
		t.Errorf("truncated lite source exceeds budget: %d tokens > 5", got)
	}
	if !strings.HasPrefix(full, src) {
		t.Errorf("line-boundary cut must be a prefix of the original source; got %q", src)
	}
	// Truncation must be announced via the structured warning channel.
	codes := budgetWarningCodes(t, m)
	found := false
	for _, c := range codes {
		if c == "budget_truncated" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected warnings_v2 code budget_truncated, got %v", codes)
	}
}

func TestHandleContext_LiteMaxTokens_WithinBudgetUntouched(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	sym, _ := newLiteTestSymbol(t, srv)
	full := liteFullSource(t, srv, sym.ID)

	req := makeReq(map[string]any{"id": sym.ID, "lite": true, "max_tokens": 10000})
	req.Params.Name = "context"
	result, err := srv.handleContext(context.Background(), req)
	if err != nil || result.IsError {
		t.Fatalf("handleContext: err=%v isErr=%v", err, result.IsError)
	}
	m := decode(t, result)
	src, _ := m["source"].(string)
	if src != full {
		t.Errorf("within-budget source must pass through untouched; got %q want %q", src, full)
	}
	if _, present := m["truncated"]; present {
		t.Errorf("truncated flag must be absent when nothing was cut; got %v", m["truncated"])
	}
}

func TestHandleContext_MaxTokensWithoutLite_AppliesGeneralBudget(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	sym, _ := newLiteTestSymbol(t, srv)
	full := liteFullSource(t, srv, sym.ID)

	req := makeReq(map[string]any{"id": sym.ID, "max_tokens": 5})
	req.Params.Name = "context"
	result, err := srv.handleContext(context.Background(), req)
	if err != nil || result.IsError {
		t.Fatalf("handleContext: err=%v isErr=%v", err, result.IsError)
	}
	m := decode(t, result)
	// Full shape preserved: the budget degrades section contents
	// (metadata-only entries, truncated primary source) but never
	// drops the top-level keys.
	for _, expected := range []string{"symbol", "imports", "callees"} {
		if _, present := m[expected]; !present {
			t.Errorf("non-lite mode should include %q; got keys %v", expected, mapKeys(m))
		}
	}
	// Integrated semantics: the budget DOES apply on the non-lite
	// path — the primary source is cut at a line boundary.
	symMap, _ := m["symbol"].(map[string]any)
	src, _ := symMap["source"].(string)
	if len(src) >= len(full) {
		t.Errorf("expected primary source truncation under max_tokens=5: got %d bytes, full is %d", len(src), len(full))
	}
	if !strings.HasPrefix(full, src) {
		t.Errorf("line-boundary cut must be a prefix of the original source; got %q", src)
	}
}
