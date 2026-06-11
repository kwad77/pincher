// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// #655: diff-encoded context for repeat reads (PINCHER_DIFF_CONTEXT=1).
// A repeat context(id=X) call short-circuits on the backing file's
// content hash: unchanged → {unchanged:true}; changed → the symbol's
// source as a line diff against what we last served. These tests pin:
//   - flag off: every call returns full source (no regression)
//   - flag on, first call: full source (cache miss)
//   - flag on, repeat unchanged: {unchanged:true} + since_hash, no source
//   - flag on, repeat after edit: symbol.diff (not source) + since_hash
//   - tokens_saved is positive on the short-circuit paths

// seedDiffSymbol writes src to a temp file and registers a project +
// one symbol that spans the WHOLE file (StartByte=0, EndByte=len(src)).
// Whole-file span keeps the "changed" test from needing offset
// arithmetic — a rewrite just re-points EndByte at the new length.
func seedDiffSymbol(t *testing.T, srv *Server, src string) (db.Symbol, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f.go"), []byte(src), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := srv.store.UpsertProject(db.Project{ID: "diffpr", Path: dir, Name: "diffpr"}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}
	sym := db.Symbol{
		ID:                   "diffpr::foo.Foo#Function",
		ProjectID:            "diffpr",
		FilePath:             "f.go",
		Name:                 "Foo",
		QualifiedName:        "foo.Foo",
		Kind:                 "Function",
		Language:             "Go",
		StartByte:            0,
		EndByte:              len(src),
		StartLine:            1,
		EndLine:              strings.Count(src, "\n") + 1,
		ExtractionConfidence: 1.0,
	}
	if err := srv.store.BulkUpsertSymbols([]db.Symbol{sym}); err != nil {
		t.Fatalf("upsert symbol: %v", err)
	}
	return sym, dir
}

// rewriteDiffSymbol overwrites the symbol's backing file with newSrc and
// re-points EndByte at the new length, simulating an edit between two
// context() calls.
func rewriteDiffSymbol(t *testing.T, srv *Server, sym db.Symbol, dir, newSrc string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "f.go"), []byte(newSrc), 0o600); err != nil {
		t.Fatalf("rewrite source: %v", err)
	}
	sym.EndByte = len(newSrc)
	sym.EndLine = strings.Count(newSrc, "\n") + 1
	if err := srv.store.BulkUpsertSymbols([]db.Symbol{sym}); err != nil {
		t.Fatalf("re-upsert symbol: %v", err)
	}
}

const diffSrcV1 = `package foo

func Foo() {
	a := 1
	b := 2
	println(a + b)
}
`

func contextSymbolMap(t *testing.T, result map[string]any) map[string]any {
	t.Helper()
	sm, ok := result["symbol"].(map[string]any)
	if !ok {
		t.Fatalf("response missing symbol map: %v", result)
	}
	return sm
}

func TestHandleContext_DiffOff_AlwaysFullSource(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	srv.diffContext = false
	sym, _ := seedDiffSymbol(t, srv, diffSrcV1)

	for i := 0; i < 2; i++ {
		res, err := srv.handleContext(context.Background(), makeReq(map[string]any{"id": sym.ID}))
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		body := decode(t, res)
		if body["unchanged"] != nil {
			t.Errorf("call %d: diff feature off but response carries unchanged=%v", i, body["unchanged"])
		}
		sm := contextSymbolMap(t, body)
		if src, _ := sm["source"].(string); !strings.Contains(src, "a + b") {
			t.Errorf("call %d: expected full source, got %q", i, src)
		}
		if sm["diff"] != nil {
			t.Errorf("call %d: diff feature off but symbol carries a diff", i)
		}
	}
}

func TestHandleContext_DiffOn_FirstCallFullSource(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	srv.diffContext = true
	sym, _ := seedDiffSymbol(t, srv, diffSrcV1)

	res, err := srv.handleContext(context.Background(), makeReq(map[string]any{"id": sym.ID}))
	if err != nil {
		t.Fatalf("handleContext: %v", err)
	}
	body := decode(t, res)
	if body["unchanged"] != nil {
		t.Errorf("first call must serve full source, not unchanged=%v", body["unchanged"])
	}
	sm := contextSymbolMap(t, body)
	if src, _ := sm["source"].(string); !strings.Contains(src, "a + b") {
		t.Errorf("first call: expected full source, got %q", src)
	}
}

func TestHandleContext_DiffOn_RepeatUnchanged(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	srv.diffContext = true
	sym, _ := seedDiffSymbol(t, srv, diffSrcV1)
	req := makeReq(map[string]any{"id": sym.ID})

	if _, err := srv.handleContext(context.Background(), req); err != nil {
		t.Fatalf("first call: %v", err)
	}
	res, err := srv.handleContext(context.Background(), req)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	body := decode(t, res)

	if body["unchanged"] != true {
		t.Errorf("repeat call on untouched file: unchanged=%v, want true", body["unchanged"])
	}
	if _, ok := body["since_hash"].(string); !ok {
		t.Errorf("unchanged response must carry since_hash; got %v", body["since_hash"])
	}
	sm := contextSymbolMap(t, body)
	if sm["source"] != nil {
		t.Errorf("unchanged response must omit source; got %v", sm["source"])
	}
	if saved := metaTokensSaved(t, body); saved <= 0 {
		t.Errorf("unchanged response tokens_saved=%d, want > 0 (the source we didn't resend)", saved)
	}
}

func TestHandleContext_DiffOn_RepeatAfterEditReturnsDiff(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	srv.diffContext = true
	sym, dir := seedDiffSymbol(t, srv, diffSrcV1)
	req := makeReq(map[string]any{"id": sym.ID})

	if _, err := srv.handleContext(context.Background(), req); err != nil {
		t.Fatalf("first call: %v", err)
	}

	// Edit one line: b := 2 → b := 99.
	const diffSrcV2 = `package foo

func Foo() {
	a := 1
	b := 99
	println(a + b)
}
`
	rewriteDiffSymbol(t, srv, sym, dir, diffSrcV2)

	res, err := srv.handleContext(context.Background(), req)
	if err != nil {
		t.Fatalf("post-edit call: %v", err)
	}
	body := decode(t, res)
	if body["unchanged"] != nil {
		t.Errorf("post-edit call must not report unchanged; got %v", body["unchanged"])
	}
	sm := contextSymbolMap(t, body)
	if sm["source"] != nil {
		t.Errorf("post-edit call must ship a diff, not full source; got source=%v", sm["source"])
	}
	diff, ok := sm["diff"].(string)
	if !ok {
		t.Fatalf("post-edit call must carry symbol.diff; got %v", sm["diff"])
	}
	// The diff must show the removed and added lines and keep an
	// unchanged context line.
	if !strings.Contains(diff, "- \tb := 2") {
		t.Errorf("diff missing removed line:\n%s", diff)
	}
	if !strings.Contains(diff, "+ \tb := 99") {
		t.Errorf("diff missing added line:\n%s", diff)
	}
	if !strings.Contains(diff, "  \ta := 1") {
		t.Errorf("diff missing unchanged context line:\n%s", diff)
	}
	if _, ok := sm["since_hash"].(string); !ok {
		t.Errorf("post-edit response must carry symbol.since_hash; got %v", sm["since_hash"])
	}
}

// lineDiff is the pure diff primitive — exercise it directly so a
// regression in the LCS walk is caught without the handler scaffolding.
func TestLineDiff(t *testing.T) {
	old := "one\ntwo\nthree\n"
	neu := "one\nTWO\nthree\nfour\n"
	got := lineDiff(old, neu)
	for _, want := range []string{"  one", "- two", "+ TWO", "  three", "+ four"} {
		if !strings.Contains(got, want) {
			t.Errorf("lineDiff missing %q in:\n%s", want, got)
		}
	}
	// Identical input → every line unchanged, no +/- markers.
	same := lineDiff(old, old)
	if strings.Contains(same, "\n- ") || strings.Contains(same, "\n+ ") ||
		strings.HasPrefix(same, "- ") || strings.HasPrefix(same, "+ ") {
		t.Errorf("lineDiff of identical input should have no +/- lines:\n%s", same)
	}
}

// v1.4.0 release-review hardening regressions (adversarial findings #1/#2):
// the diff cache must only operate on full-fidelity serves to an
// identifiable consumer, and diff=false must always recover the full body.

// TestHandleContext_DiffFalse_BypassesCache: diff=false on a repeat call
// returns the full source (no {unchanged:true} short-circuit) and does not
// write the cache.
func TestHandleContext_DiffFalse_BypassesCache(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	srv.diffContext = true
	sym, _ := seedDiffSymbol(t, srv, diffSrcV1)

	// Warm the cache with a normal full-fidelity call.
	if _, err := srv.handleContext(context.Background(), makeReq(map[string]any{"id": sym.ID})); err != nil {
		t.Fatalf("warm call: %v", err)
	}
	// Repeat WITHOUT diff=false would short-circuit; with diff=false the
	// full body must ship — this is the documented recovery path for a
	// consumer that never saw the prior serve.
	res, err := srv.handleContext(context.Background(), makeReq(map[string]any{"id": sym.ID, "diff": false}))
	if err != nil {
		t.Fatalf("diff=false call: %v", err)
	}
	body := decode(t, res)
	sm := contextSymbolMap(t, body)
	if _, unchanged := body["unchanged"]; unchanged {
		t.Fatalf("diff=false must never short-circuit to unchanged: %v", body)
	}
	src, _ := sm["source"].(string)
	if !strings.Contains(src, "println(a + b)") {
		t.Fatalf("diff=false must ship the full source, got: %v", sm)
	}
}

// TestHandleContext_MaxTokens_DoesNotPoisonDiffCache: a budgeted
// (truncated) serve must not record "the caller has the body" — the
// follow-up unbudgeted call must return the full source, not
// {unchanged:true}.
func TestHandleContext_MaxTokens_DoesNotPoisonDiffCache(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	srv.diffContext = true
	// Big enough body that a small budget truncates it.
	big := "package foo\n\nfunc Foo() {\n"
	for i := 0; i < 200; i++ {
		big += "\tprintln(\"line line line line line line line\")\n"
	}
	big += "}\n"
	sym, _ := seedDiffSymbol(t, srv, big)

	// Budgeted first fetch: truncated body, must NOT populate the cache.
	if _, err := srv.handleContext(context.Background(), makeReq(map[string]any{"id": sym.ID, "max_tokens": 50})); err != nil {
		t.Fatalf("budgeted call: %v", err)
	}
	// The natural follow-up "now give me the whole thing" call.
	res, err := srv.handleContext(context.Background(), makeReq(map[string]any{"id": sym.ID}))
	if err != nil {
		t.Fatalf("full call: %v", err)
	}
	body := decode(t, res)
	if _, unchanged := body["unchanged"]; unchanged {
		t.Fatalf("budgeted serve poisoned the diff cache — full body unobtainable: %v", body)
	}
	sm := contextSymbolMap(t, body)
	if src, _ := sm["source"].(string); !strings.HasSuffix(strings.TrimSpace(src), "}") {
		t.Fatalf("follow-up call must ship the full untruncated source")
	}
}

// TestHandleContext_FieldsProjection_DoesNotPoisonDiffCache: a
// fields=callees call never ships the body, so it must not mark the
// symbol as served.
func TestHandleContext_FieldsProjection_DoesNotPoisonDiffCache(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	srv.diffContext = true
	sym, _ := seedDiffSymbol(t, srv, diffSrcV1)

	if _, err := srv.handleContext(context.Background(), makeReq(map[string]any{"id": sym.ID, "fields": "callees"})); err != nil {
		t.Fatalf("projected call: %v", err)
	}
	res, err := srv.handleContext(context.Background(), makeReq(map[string]any{"id": sym.ID}))
	if err != nil {
		t.Fatalf("full call: %v", err)
	}
	body := decode(t, res)
	if _, unchanged := body["unchanged"]; unchanged {
		t.Fatalf("bodiless projected serve poisoned the diff cache: %v", body)
	}
	sm := contextSymbolMap(t, body)
	if src, _ := sm["source"].(string); !strings.Contains(src, "println(a + b)") {
		t.Fatalf("full body must ship after a projected serve, got: %v", sm)
	}
}

// TestHandleContext_NoDiffCtx_BypassesCache: the HTTP REST dispatch marks
// its ctx (no per-connection identity); marked calls must neither read
// nor write the cache.
func TestHandleContext_NoDiffCtx_BypassesCache(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	srv.diffContext = true
	sym, _ := seedDiffSymbol(t, srv, diffSrcV1)

	restCtx := withNoDiffContext(context.Background())
	// Two REST-shaped calls: the second must still ship the full body.
	for i := 0; i < 2; i++ {
		res, err := srv.handleContext(restCtx, makeReq(map[string]any{"id": sym.ID}))
		if err != nil {
			t.Fatalf("rest call %d: %v", i, err)
		}
		body := decode(t, res)
		if _, unchanged := body["unchanged"]; unchanged {
			t.Fatalf("REST call %d short-circuited to unchanged — cross-client poisoning", i)
		}
	}
	// And a REST call must not have warmed the cache for MCP consumers
	// either: a following non-REST call is a cache miss (full source).
	res, err := srv.handleContext(context.Background(), makeReq(map[string]any{"id": sym.ID}))
	if err != nil {
		t.Fatalf("mcp call: %v", err)
	}
	body := decode(t, res)
	if _, unchanged := body["unchanged"]; unchanged {
		t.Fatalf("REST serve leaked into the MCP connection's cache keyspace")
	}
}

// TestHandleContext_Unchanged_CarriesRecoveryHint: the {unchanged:true}
// short-circuit must be self-describing — it names diff=false as the
// recovery path for consumers that don't hold the prior serve.
func TestHandleContext_Unchanged_CarriesRecoveryHint(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	srv.diffContext = true
	sym, _ := seedDiffSymbol(t, srv, diffSrcV1)

	if _, err := srv.handleContext(context.Background(), makeReq(map[string]any{"id": sym.ID})); err != nil {
		t.Fatalf("warm call: %v", err)
	}
	res, err := srv.handleContext(context.Background(), makeReq(map[string]any{"id": sym.ID}))
	if err != nil {
		t.Fatalf("repeat call: %v", err)
	}
	raw := textOf(t, res)
	body := decode(t, res)
	if u, _ := body["unchanged"].(bool); !u {
		t.Fatalf("expected unchanged short-circuit, got: %v", body)
	}
	if !strings.Contains(raw, "diff=false") {
		t.Fatalf("unchanged response must carry the diff=false recovery hint, got: %s", raw)
	}
}
