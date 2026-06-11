// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"
)

// hook-redirect-v2: `context lite=true max_tokens=N` truncates source
// to the budget (4 bytes/token approximation) with an explicit marker
// + truncated:true flag. The PreToolUse hook derives N from the
// intercepted Read call's own limit param, so the redirect can never
// cost more window than the Read it replaced. These tests pin:
//   - over-budget source is truncated, announced, and flagged
//   - within-budget source passes through untouched, no flag
//   - max_tokens without lite is ignored (full shape preserved)

func TestHandleContext_LiteMaxTokens_TruncatesOverBudget(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	sym, _ := newLiteTestSymbol(t, srv)

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
	if !strings.Contains(src, "truncated at max_tokens=5") {
		t.Errorf("truncation must be announced in the source body; got %q", src)
	}
	if truncated, _ := m["truncated"].(bool); !truncated {
		t.Errorf("truncated flag missing or false; got %v", m["truncated"])
	}
	// Payload before the marker must respect the byte budget (5 × 4).
	payload := src[:strings.Index(src, "\n…[truncated")]
	if len(payload) > 5*4 {
		t.Errorf("payload exceeds budget: %d bytes > 20", len(payload))
	}
}

func TestHandleContext_LiteMaxTokens_WithinBudgetUntouched(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	sym, _ := newLiteTestSymbol(t, srv)

	req := makeReq(map[string]any{"id": sym.ID, "lite": true, "max_tokens": 10000})
	req.Params.Name = "context"
	result, err := srv.handleContext(context.Background(), req)
	if err != nil || result.IsError {
		t.Fatalf("handleContext: err=%v isErr=%v", err, result.IsError)
	}
	m := decode(t, result)
	src, _ := m["source"].(string)
	if strings.Contains(src, "truncated at max_tokens") {
		t.Errorf("within-budget source must not be truncated; got %q", src)
	}
	if _, present := m["truncated"]; present {
		t.Errorf("truncated flag must be absent when nothing was cut; got %v", m["truncated"])
	}
}

func TestHandleContext_MaxTokensWithoutLite_Ignored(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	sym, _ := newLiteTestSymbol(t, srv)

	req := makeReq(map[string]any{"id": sym.ID, "max_tokens": 5})
	req.Params.Name = "context"
	result, err := srv.handleContext(context.Background(), req)
	if err != nil || result.IsError {
		t.Fatalf("handleContext: err=%v isErr=%v", err, result.IsError)
	}
	m := decode(t, result)
	// Full shape preserved; the budget only applies to the lite path.
	for _, expected := range []string{"symbol", "imports", "callees"} {
		if _, present := m[expected]; !present {
			t.Errorf("non-lite mode should include %q; got keys %v", expected, mapKeys(m))
		}
	}
	symMap, _ := m["symbol"].(map[string]any)
	if src, _ := symMap["source"].(string); strings.Contains(src, "truncated at max_tokens") {
		t.Errorf("max_tokens must not truncate the non-lite path; got %q", src)
	}
}
