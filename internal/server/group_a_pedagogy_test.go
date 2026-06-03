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

// #712 Group A: every arg-validation / input-rejection error path that
// used bare errResult now returns the rich JSON envelope with
// _meta.next_steps. These tests pin that contract per tool so a future
// refactor can't quietly regress one back to a bare text error.

// decodeRichErr asserts the result is an IsError carrying a JSON body
// with an `error` string and `_meta.next_steps`. Returns the steps.
func decodeRichErr(t *testing.T, raw string) []any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("error body is not JSON (still bare errResult?): %v\nraw: %s", err, raw)
	}
	if _, ok := body["error"].(string); !ok {
		t.Fatalf("rich error body missing `error` string; got: %s", raw)
	}
	meta, _ := body["_meta"].(map[string]any)
	if meta == nil {
		t.Fatalf("rich error body missing `_meta`; got: %s", raw)
	}
	steps, _ := meta["next_steps"].([]any)
	if len(steps) == 0 {
		t.Fatalf("rich error body missing `_meta.next_steps`; got: %s", raw)
	}
	return steps
}

func TestGroupA_Search_EmptyQueryRichError(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	res, err := srv.handleSearch(context.Background(), makeReq(map[string]any{"query": "   "}))
	if err != nil {
		t.Fatalf("handleSearch: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError; got %s", textOf(t, res))
	}
	steps := decodeRichErr(t, textOf(t, res))
	first, _ := steps[0].(map[string]any)
	if first["tool"] != "search" {
		t.Errorf("first next_step tool = %v, want search", first["tool"])
	}
}

func TestGroupA_Query_EmptyPinchqlRichError(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	res, err := srv.handleQuery(context.Background(), makeReq(map[string]any{}))
	if err != nil {
		t.Fatalf("handleQuery: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError; got %s", textOf(t, res))
	}
	steps := decodeRichErr(t, textOf(t, res))
	// One of the steps should point at `schema` — that's the
	// orient-yourself move when you don't know the node/edge kinds.
	foundSchema := false
	for _, s := range steps {
		m, _ := s.(map[string]any)
		if m["tool"] == "schema" {
			foundSchema = true
		}
	}
	if !foundSchema {
		t.Errorf("empty-pinchql next_steps should include `schema`; got %v", steps)
	}
}

func TestGroupA_Trace_NoSeedRichError(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	res, err := srv.handleTrace(context.Background(), makeReq(map[string]any{}))
	if err != nil {
		t.Fatalf("handleTrace: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError; got %s", textOf(t, res))
	}
	decodeRichErr(t, textOf(t, res))
}

func TestGroupA_Symbol_NoIDRichError(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	res, err := srv.handleSymbol(context.Background(), makeReq(map[string]any{}))
	if err != nil {
		t.Fatalf("handleSymbol: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError; got %s", textOf(t, res))
	}
	steps := decodeRichErr(t, textOf(t, res))
	first, _ := steps[0].(map[string]any)
	if first["tool"] != "search" {
		t.Errorf("first next_step tool = %v, want search", first["tool"])
	}
}

func TestGroupA_Neighborhood_NoIDRichError(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	res, err := srv.handleNeighborhood(context.Background(), makeReq(map[string]any{}))
	if err != nil {
		t.Fatalf("handleNeighborhood: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError; got %s", textOf(t, res))
	}
	decodeRichErr(t, textOf(t, res))
}

func TestGroupA_Guide_EmptyTaskRichError(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	res, err := srv.handleGuide(context.Background(), makeReq(map[string]any{}))
	if err != nil {
		t.Fatalf("handleGuide: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError; got %s", textOf(t, res))
	}
	steps := decodeRichErr(t, textOf(t, res))
	first, _ := steps[0].(map[string]any)
	if first["tool"] != "guide" {
		t.Errorf("first next_step tool = %v, want guide (example task)", first["tool"])
	}
}

func TestGroupA_Fetch_NoURLRichError(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	res, err := srv.handleFetch(context.Background(), makeReq(map[string]any{}))
	if err != nil {
		t.Fatalf("handleFetch: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError; got %s", textOf(t, res))
	}
	decodeRichErr(t, textOf(t, res))
}

func TestGroupA_Fetch_BadSchemeRichError(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	res, err := srv.handleFetch(context.Background(), makeReq(map[string]any{"url": "file:///etc/passwd"}))
	if err != nil {
		t.Fatalf("handleFetch: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError; got %s", textOf(t, res))
	}
	raw := textOf(t, res)
	decodeRichErr(t, raw)
	// The error message must still name the rejected URL.
	if !strings.Contains(raw, "file:///etc/passwd") {
		t.Errorf("error should name the rejected URL; got: %s", raw)
	}
}

func TestGroupA_Adr_UnknownActionRichError(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p"
	store.UpsertProject(db.Project{ID: "p", Path: "/tmp/p", Name: "p", IndexedAt: time.Now()})
	res, err := srv.handleADR(context.Background(), makeReq(map[string]any{"action": "bogus"}))
	if err != nil {
		t.Fatalf("handleADR: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError; got %s", textOf(t, res))
	}
	raw := textOf(t, res)
	decodeRichErr(t, raw)
	// Must enumerate the valid actions so the caller doesn't guess.
	for _, want := range []string{"get", "set", "list", "delete"} {
		if !strings.Contains(raw, want) {
			t.Errorf("unknown-action error should list valid action %q; got: %s", want, raw)
		}
	}
}

func TestGroupA_Adr_SetMissingKeyRichError(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p"
	store.UpsertProject(db.Project{ID: "p", Path: "/tmp/p", Name: "p", IndexedAt: time.Now()})
	res, err := srv.handleADR(context.Background(), makeReq(map[string]any{
		"action": "set", "value": "x",
	}))
	if err != nil {
		t.Fatalf("handleADR: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError; got %s", textOf(t, res))
	}
	decodeRichErr(t, textOf(t, res))
}

func TestGroupA_Index_NoPathRichError(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	srv.sessionRoot = "" // force the "no session root" branch
	res, err := srv.handleIndex(context.Background(), makeReq(map[string]any{}))
	if err != nil {
		t.Fatalf("handleIndex: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError; got %s", textOf(t, res))
	}
	decodeRichErr(t, textOf(t, res))
}

// #712 follow-up: handleSymbols empty-ids and over-cap rejections were
// missed by the original Group A sweep — they used bare errResult. Pin
// the rich envelope.
func TestSymbols_EmptyIdsRichError(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	res, err := srv.handleSymbols(context.Background(), makeReq(map[string]any{"ids": []string{}}))
	if err != nil {
		t.Fatalf("handleSymbols: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError; got %s", textOf(t, res))
	}
	steps := decodeRichErr(t, textOf(t, res))
	first, _ := steps[0].(map[string]any)
	if first["tool"] != "search" {
		t.Errorf("first next_step tool = %v, want search", first["tool"])
	}
}

func TestSymbols_TooManyIdsRichError(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	ids := make([]string, maxBatchSymbols+1)
	for i := range ids {
		ids[i] = "x"
	}
	res, err := srv.handleSymbols(context.Background(), makeReq(map[string]any{"ids": ids}))
	if err != nil {
		t.Fatalf("handleSymbols: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError; got %s", textOf(t, res))
	}
	decodeRichErr(t, textOf(t, res))
}

// #712: passing both `pinchql` and the legacy `cypher` alias silently
// dropped `cypher`. It now runs `pinchql` and warns in _meta.warnings.
func TestQuery_BothPinchqlAndCypher_Warns(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	srv.sessionID = "qbp"
	srv.sessionRoot = "/tmp/qbp"
	store.UpsertProject(db.Project{ID: "qbp", Path: "/tmp/qbp", Name: "qbp", IndexedAt: time.Now()})

	res, err := srv.handleQuery(context.Background(), makeReq(map[string]any{
		"pinchql": "MATCH (n:Function) RETURN n.name LIMIT 1",
		"cypher":  "MATCH (n:Class) RETURN n.name LIMIT 1",
	}))
	if err != nil {
		t.Fatalf("handleQuery: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success; got error %s", textOf(t, res))
	}
	body := decode(t, res)
	meta, _ := body["_meta"].(map[string]any)
	warns, _ := meta["warnings"].([]any)
	if len(warns) == 0 {
		t.Fatalf("expected a warning about both params; got _meta %v", meta)
	}
	w, _ := warns[0].(string)
	if !strings.Contains(w, "cypher") || !strings.Contains(w, "pinchql") {
		t.Errorf("warning should name both params; got %q", w)
	}
}

// #712: adr action=get on a key that doesn't exist used a bare
// errResult. It now returns the rich envelope pointing at adr list so
// the caller can spot a typo or wrong-project scope.
func TestAdr_GetMissingKeyRichError(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	srv.sessionID = "adrp"
	srv.sessionRoot = "/tmp/adrp"
	store.UpsertProject(db.Project{ID: "adrp", Path: "/tmp/adrp", Name: "adrp", IndexedAt: time.Now()})

	res, err := srv.handleADR(context.Background(), makeReq(map[string]any{
		"action": "get", "key": "NO_SUCH_KEY",
	}))
	if err != nil {
		t.Fatalf("handleADR: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError; got %s", textOf(t, res))
	}
	steps := decodeRichErr(t, textOf(t, res))
	first, _ := steps[0].(map[string]any)
	if first["tool"] != "adr" {
		t.Errorf("first next_step tool = %v, want adr", first["tool"])
	}
}
