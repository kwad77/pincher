// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// marshalToString renders v as JSON for substring assertions in _meta.
func marshalToString(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// mode=skeleton — render symbols from already-indexed metadata, NO file read.
// These tests pin the headline properties:
//   - a function skeleton returns signature + docstring with NO body, at a
//     fraction of the full-source tokens,
//   - a container skeleton lists its members' signatures (via Parent), no
//     bodies,
//   - the render reads ONLY the DB: it still works after the source file is
//     edited or deleted (the byte-offset staleness path is sidestepped),
//   - backward compat: omitting mode (or mode=full) returns today's
//     source-byte behaviour unchanged,
//   - max_tokens truncates a huge container's child list with a "+N more"
//     note, never mid-line.

// dbSkelFuncSrc is a small Go function used as the on-disk source for the
// full-mode baseline (and to prove skeleton mode does NOT read it).
const dbSkelFuncSrc = `// Charge debits the account and records the transaction.
// Returns the new balance, or an error when funds are insufficient.
func Charge(acct *Account, amount int64) (int64, error) {
	if amount <= 0 {
		return acct.Balance, errBadAmount
	}
	if acct.Balance < amount {
		return acct.Balance, errInsufficient
	}
	acct.Balance -= amount
	record(acct.ID, amount)
	return acct.Balance, nil
}
`

// seedDBSkelFunc writes a function to a temp project and registers its symbol
// with the indexed Signature/ReturnType/Docstring fields populated (as the
// Go AST extractor would). Returns the symbol and the project dir.
func seedDBSkelFunc(t *testing.T, srv *Server) (db.Symbol, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bank.go"), []byte(dbSkelFuncSrc), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if err := srv.store.UpsertProject(db.Project{ID: "dbskel", Path: dir, Name: "dbskel"}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}
	sym := db.Symbol{
		ID:                   "dbskel::bank.Charge#Function",
		ProjectID:            "dbskel",
		FilePath:             "bank.go",
		Name:                 "Charge",
		QualifiedName:        "bank.Charge",
		Kind:                 "Function",
		Language:             "Go",
		StartByte:            0,
		EndByte:              len(dbSkelFuncSrc),
		StartLine:            1,
		EndLine:              strings.Count(dbSkelFuncSrc, "\n") + 1,
		Signature:            "func Charge(acct *Account, amount int64) (int64, error)",
		ReturnType:           "int64, error",
		Docstring:            "Charge debits the account and records the transaction.\nReturns the new balance, or an error when funds are insufficient.",
		ExtractionConfidence: 1.0,
	}
	if err := srv.store.BulkUpsertSymbols([]db.Symbol{sym}); err != nil {
		t.Fatalf("upsert symbols: %v", err)
	}
	return sym, dir
}

// symbolSourceWithMode calls handleSymbol with the given mode and returns the
// source string + the full decoded body.
func symbolSourceWithMode(t *testing.T, srv *Server, id, mode string) (string, map[string]any) {
	t.Helper()
	args := map[string]any{"id": id, "project": id[:strings.Index(id, ":")]}
	// Use the symbol's project explicitly — derive from the seeded fixtures.
	if strings.HasPrefix(id, "dbskel::") {
		args["project"] = "dbskel"
	} else if strings.HasPrefix(id, "cont::") {
		args["project"] = "cont"
	}
	if mode != "" {
		args["mode"] = mode
	}
	res, err := srv.handleSymbol(context.Background(), makeReq(args))
	if err != nil {
		t.Fatalf("handleSymbol(mode=%q): %v", mode, err)
	}
	body := decode(t, res)
	src, _ := body["source"].(string)
	return src, body
}

// A function skeleton: signature + docstring, NO body, far fewer tokens.
func TestSymbol_ModeSkeleton_FunctionSignatureAndDocNoBody(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	sym, _ := seedDBSkelFunc(t, srv)

	full, _ := symbolSourceWithMode(t, srv, sym.ID, "full")
	if full != dbSkelFuncSrc {
		t.Fatalf("mode=full must return verbatim source; got %d bytes want %d", len(full), len(dbSkelFuncSrc))
	}

	skel, body := symbolSourceWithMode(t, srv, sym.ID, "skeleton")
	if skel == "" {
		t.Fatal("skeleton source is empty")
	}

	// Signature present.
	if !strings.Contains(skel, "func Charge(acct *Account, amount int64) (int64, error)") {
		t.Errorf("skeleton missing signature:\n%s", skel)
	}
	// Docstring present as a leading comment.
	if !strings.Contains(skel, "// Charge debits the account") {
		t.Errorf("skeleton missing docstring comment:\n%s", skel)
	}
	// Elided body placeholder present.
	if !strings.Contains(skel, "{ ... }") {
		t.Errorf("skeleton missing body-elision placeholder:\n%s", skel)
	}
	// NO body: the actual statements must NOT appear.
	for _, bodyTok := range []string{"errInsufficient", "acct.Balance -= amount", "record(acct.ID"} {
		if strings.Contains(skel, bodyTok) {
			t.Errorf("skeleton leaked body statement %q:\n%s", bodyTok, skel)
		}
	}

	fullTok := db.ApproxTokens(full)
	skelTok := db.ApproxTokens(skel)
	t.Logf("function compression: full=%d tokens, skeleton=%d tokens, ratio=%.3f",
		fullTok, skelTok, float64(skelTok)/float64(fullTok))
	if skelTok >= fullTok {
		t.Errorf("skeleton not smaller than full: %d >= %d tokens", skelTok, fullTok)
	}

	// _meta.mode=skeleton + savings.
	meta, _ := body["_meta"].(map[string]any)
	if meta == nil || meta["mode"] != modeSkeletonValue {
		t.Errorf("_meta.mode=skeleton missing; _meta=%v", body["_meta"])
	}
	if _, ok := meta["tokens_saved_vs_full"]; !ok {
		t.Errorf("_meta.tokens_saved_vs_full missing; _meta=%v", meta)
	}
}

// Backward compat: omitting mode (and mode=full) returns verbatim source and
// stamps NO mode marker.
func TestSymbol_ModeSkeleton_BackwardCompat(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	sym, _ := seedDBSkelFunc(t, srv)

	for _, mode := range []string{"", "full"} {
		src, body := symbolSourceWithMode(t, srv, sym.ID, mode)
		if src != dbSkelFuncSrc {
			t.Errorf("mode=%q: source not verbatim; got %d want %d bytes", mode, len(src), len(dbSkelFuncSrc))
		}
		if meta, _ := body["_meta"].(map[string]any); meta != nil {
			if _, present := meta["mode"]; present {
				t.Errorf("mode=%q must not stamp _meta.mode; _meta=%v", mode, meta)
			}
		}
	}
}

// Unknown mode degrades to full with a warning (soft contract).
func TestSymbol_ModeSkeleton_UnknownDegradesToFull(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	sym, _ := seedDBSkelFunc(t, srv)

	src, body := symbolSourceWithMode(t, srv, sym.ID, "bogus")
	if src != dbSkelFuncSrc {
		t.Errorf("unknown mode must return full source; got %d bytes", len(src))
	}
	// Warning surfaced somewhere in _meta.
	raw, _ := body["_meta"].(map[string]any)
	if raw == nil {
		t.Fatalf("expected _meta with a warning; body=%v", body)
	}
	found := strings.Contains(strings.ToLower(marshalToString(t, raw)), "unknown mode")
	if !found {
		t.Errorf("expected an 'unknown mode' warning in _meta; _meta=%v", raw)
	}
}

// HEADLINE PROPERTY: skeleton mode does NOT read the file. The render still
// works after the source file is deleted — proof it reads only the DB and is
// immune to the byte-offset staleness path.
func TestSymbol_ModeSkeleton_WorksAfterFileDeleted(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	sym, dir := seedDBSkelFunc(t, srv)

	// Sanity: full mode reads the file fine first.
	full, _ := symbolSourceWithMode(t, srv, sym.ID, "full")
	if full != dbSkelFuncSrc {
		t.Fatalf("precondition: full read should work; got %d bytes", len(full))
	}

	// Delete the backing file. Full mode would now fail to read it.
	if err := os.Remove(filepath.Join(dir, "bank.go")); err != nil {
		t.Fatalf("remove fixture: %v", err)
	}

	skel, body := symbolSourceWithMode(t, srv, sym.ID, "skeleton")
	if skel == "" {
		t.Fatal("skeleton empty after file deletion — it must render from the DB alone")
	}
	if !strings.Contains(skel, "func Charge(acct *Account, amount int64) (int64, error)") {
		t.Errorf("skeleton after deletion lost the signature:\n%s", skel)
	}
	if !strings.Contains(skel, "// Charge debits the account") {
		t.Errorf("skeleton after deletion lost the docstring:\n%s", skel)
	}
	if meta, _ := body["_meta"].(map[string]any); meta == nil || meta["mode"] != modeSkeletonValue {
		t.Errorf("_meta.mode=skeleton missing after deletion; _meta=%v", body["_meta"])
	}
	// #2047: the skeleton still renders from the DB after deletion (the
	// headline delete-survival property), but it must now ALSO carry an
	// honest staleness signal — a cheap os.Stat (not a file read) proves the
	// backing file is gone. The render is preserved; the silent-lie is not.
	meta, _ := body["_meta"].(map[string]any)
	if meta == nil {
		t.Fatalf("_meta missing after deletion; body=%v", body)
	}
	if meta["disk_verified"] != false {
		t.Errorf("expected _meta.disk_verified=false honesty marker; _meta=%v", meta)
	}
	if meta["rendered_from"] != "index" {
		t.Errorf("expected _meta.rendered_from=\"index\" honesty marker; _meta=%v", meta)
	}
	if !slices.Contains(warningsV2Codes(body), "skeleton_index_stale") {
		t.Errorf("expected warnings_v2 code=skeleton_index_stale after file deletion; _meta=%v", meta)
	}

	// Full mode, by contrast, returns empty source for the now-missing file —
	// confirming skeleton's independence from the read path is real.
	fullAfter, _ := symbolSourceWithMode(t, srv, sym.ID, "full")
	if fullAfter == dbSkelFuncSrc {
		t.Errorf("full mode unexpectedly still returned source after file deletion")
	}
}

// #2047: mode=skeleton must surface index-staleness honestly across the
// three drift shapes. A skeleton renders the signature from indexed db.Symbol
// fields with NO file read, so without a signal a drifted source is served
// silently as the OLD shape. The fix is a CHEAP os.Stat (not a file read):
//
//	clean        → honesty marker present, NO stale warning,
//	shrink/delete → stat proves drift → skeleton_index_stale warning,
//	grow/same-size → size-stat can't catch it, but the disk_verified=false
//	                 honesty marker still prevents the silent-current lie.
func TestSymbol_ModeSkeleton_StalenessGate(t *testing.T) {
	t.Parallel()

	// Case 1: clean — file matches the index. Honesty marker present, but
	// NO stale warning (the stat passes; the symbol fits inside the file).
	t.Run("clean_marker_no_warning", func(t *testing.T) {
		t.Parallel()
		srv, _, _ := newTestServer(t)
		sym, _ := seedDBSkelFunc(t, srv)

		_, body := symbolSourceWithMode(t, srv, sym.ID, "skeleton")
		meta, _ := body["_meta"].(map[string]any)
		if meta == nil {
			t.Fatalf("_meta missing; body=%v", body)
		}
		if meta["disk_verified"] != false {
			t.Errorf("expected _meta.disk_verified=false honesty marker on a clean skeleton; _meta=%v", meta)
		}
		if meta["rendered_from"] != "index" {
			t.Errorf("expected _meta.rendered_from=\"index\" on a clean skeleton; _meta=%v", meta)
		}
		if slices.Contains(warningsV2Codes(body), "skeleton_index_stale") {
			t.Errorf("clean skeleton must NOT carry a stale warning; _meta=%v", meta)
		}
	})

	// Case 2: shrink — rewrite the file far shorter so the symbol's indexed
	// EndByte exceeds the current file size. The stat (size) catches this.
	t.Run("shrink_emits_stale_warning", func(t *testing.T) {
		t.Parallel()
		srv, _, _ := newTestServer(t)
		sym, dir := seedDBSkelFunc(t, srv)

		if err := os.WriteFile(filepath.Join(dir, "bank.go"), []byte("// gone\n"), 0o600); err != nil {
			t.Fatalf("shrink fixture: %v", err)
		}

		skel, body := symbolSourceWithMode(t, srv, sym.ID, "skeleton")
		if skel == "" {
			t.Fatal("skeleton must still render from the DB after a shrink")
		}
		meta, _ := body["_meta"].(map[string]any)
		if meta == nil || meta["disk_verified"] != false {
			t.Errorf("expected _meta.disk_verified=false after shrink; _meta=%v", meta)
		}
		if !slices.Contains(warningsV2Codes(body), "skeleton_index_stale") {
			t.Errorf("expected warnings_v2 code=skeleton_index_stale after shrink; _meta=%v", meta)
		}
	})

	// Case 3: grow / same-size edit — rewrite the signature on disk to the
	// SAME byte length (or longer) so the symbol still fits. A size-stat
	// alone cannot detect this (the complete fix needs the indexed mtime,
	// tracked as the #2043 follow-up). The contract for THIS PR: the
	// disk_verified=false honesty marker is still present, so the drifted
	// signature is never presented as byte-current.
	t.Run("grow_keeps_honesty_marker", func(t *testing.T) {
		t.Parallel()
		srv, _, _ := newTestServer(t)
		sym, dir := seedDBSkelFunc(t, srv)

		// Grow the file (strictly larger than the indexed EndByte) so the
		// size check cannot fire — proving the honesty marker is what holds.
		grown := dbSkelFuncSrc + "\n// an appended trailing comment that grows the file well past its indexed length\n"
		if err := os.WriteFile(filepath.Join(dir, "bank.go"), []byte(grown), 0o600); err != nil {
			t.Fatalf("grow fixture: %v", err)
		}

		skel, body := symbolSourceWithMode(t, srv, sym.ID, "skeleton")
		if skel == "" {
			t.Fatal("skeleton must still render from the DB after a grow")
		}
		meta, _ := body["_meta"].(map[string]any)
		if meta == nil {
			t.Fatalf("_meta missing after grow; body=%v", body)
		}
		if meta["disk_verified"] != false {
			t.Errorf("expected _meta.disk_verified=false honesty marker after grow; _meta=%v", meta)
		}
		if meta["rendered_from"] != "index" {
			t.Errorf("expected _meta.rendered_from=\"index\" after grow; _meta=%v", meta)
		}
		// Size-stat genuinely can't catch a grow, so no stale warning is
		// expected here — the honesty marker is the guarantee this PR ships.
		if slices.Contains(warningsV2Codes(body), "skeleton_index_stale") {
			t.Logf("note: grow produced a stale warning (acceptable, but not required by the size-gate)")
		}
	})
}

// ── Container skeleton ──────────────────────────────────────────────────

// seedDBSkelContainer registers a Class with two methods + one field, linked
// via Parent == container.QualifiedName (the indexer's convention).
func seedDBSkelContainer(t *testing.T, srv *Server) db.Symbol {
	t.Helper()
	dir := t.TempDir()
	// A trivial on-disk file so full mode has something to read.
	if err := os.WriteFile(filepath.Join(dir, "cart.go"), []byte("package shop\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if err := srv.store.UpsertProject(db.Project{ID: "cont", Path: dir, Name: "cont"}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}
	container := db.Symbol{
		ID:                   "cont::shop.Cart#Class",
		ProjectID:            "cont",
		FilePath:             "cart.go",
		Name:                 "Cart",
		QualifiedName:        "shop.Cart",
		Kind:                 "Class",
		Language:             "Go",
		StartByte:            0,
		EndByte:              13,
		StartLine:            1,
		EndLine:              1,
		Signature:            "type Cart struct",
		Docstring:            "Cart holds line items for a shopping session.",
		ExtractionConfidence: 1.0,
	}
	add := db.Symbol{
		ID: "cont::shop.Cart.Add#Method", ProjectID: "cont", FilePath: "cart.go",
		Name: "Add", QualifiedName: "shop.Cart.Add", Kind: "Method", Language: "Go",
		Signature: "func (c *Cart) Add(item Item) error", ReturnType: "error",
		Parent: "shop.Cart", StartByte: 1, EndByte: 2, ExtractionConfidence: 1.0,
	}
	total := db.Symbol{
		ID: "cont::shop.Cart.Total#Method", ProjectID: "cont", FilePath: "cart.go",
		Name: "Total", QualifiedName: "shop.Cart.Total", Kind: "Method", Language: "Go",
		Signature: "func (c *Cart) Total() int64", ReturnType: "int64",
		Parent: "shop.Cart", StartByte: 3, EndByte: 4, ExtractionConfidence: 1.0,
	}
	if err := srv.store.BulkUpsertSymbols([]db.Symbol{container, add, total}); err != nil {
		t.Fatalf("upsert symbols: %v", err)
	}
	return container
}

// A container skeleton lists its members' signatures (via Parent), no bodies.
func TestSymbol_ModeSkeleton_ContainerListsChildSignatures(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	container := seedDBSkelContainer(t, srv)

	skel, body := symbolSourceWithMode(t, srv, container.ID, "skeleton")
	if skel == "" {
		t.Fatal("container skeleton empty")
	}
	// Container declaration + docstring.
	if !strings.Contains(skel, "type Cart struct") {
		t.Errorf("container skeleton missing declaration:\n%s", skel)
	}
	if !strings.Contains(skel, "// Cart holds line items") {
		t.Errorf("container skeleton missing docstring:\n%s", skel)
	}
	// Both method signatures, bodies elided.
	for _, memberSig := range []string{
		"func (c *Cart) Add(item Item) error",
		"func (c *Cart) Total() int64",
	} {
		if !strings.Contains(skel, memberSig) {
			t.Errorf("container skeleton missing member signature %q:\n%s", memberSig, skel)
		}
	}
	if !strings.Contains(skel, "{ ... }") {
		t.Errorf("container members must show body elision:\n%s", skel)
	}
	if meta, _ := body["_meta"].(map[string]any); meta == nil || meta["mode"] != modeSkeletonValue {
		t.Errorf("_meta.mode=skeleton missing on container; _meta=%v", body["_meta"])
	}
}

// max_tokens truncates a large container's child list with a "+N more" note,
// never mid-line.
func TestSymbol_ModeSkeleton_ContainerMaxTokensTruncates(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "big.go"), []byte("package big\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if err := srv.store.UpsertProject(db.Project{ID: "big", Path: dir, Name: "big"}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}
	syms := []db.Symbol{{
		ID: "big::big.Huge#Class", ProjectID: "big", FilePath: "big.go",
		Name: "Huge", QualifiedName: "big.Huge", Kind: "Class", Language: "Go",
		Signature: "type Huge struct", StartByte: 0, EndByte: 12, ExtractionConfidence: 1.0,
	}}
	const nMethods = 60
	for i := 0; i < nMethods; i++ {
		name := "Method" + string(rune('A'+i%26)) + string(rune('0'+i/26))
		syms = append(syms, db.Symbol{
			ID: "big::big.Huge." + name + "#Method", ProjectID: "big", FilePath: "big.go",
			Name: name, QualifiedName: "big.Huge." + name, Kind: "Method", Language: "Go",
			Signature: "func (h *Huge) " + name + "(arg SomeVeryLongTypeName) (ResultType, error)",
			Parent:    "big.Huge", StartByte: i + 1, EndByte: i + 2, ExtractionConfidence: 1.0,
		})
	}
	if err := srv.store.BulkUpsertSymbols(syms); err != nil {
		t.Fatalf("upsert symbols: %v", err)
	}

	res, err := srv.handleSymbol(context.Background(), makeReq(map[string]any{
		"id": "big::big.Huge#Class", "project": "big", "mode": "skeleton", "max_tokens": 80,
	}))
	if err != nil {
		t.Fatalf("handleSymbol: %v", err)
	}
	body := decode(t, res)
	skel, _ := body["source"].(string)
	if skel == "" {
		t.Fatal("truncated container skeleton empty")
	}
	if !strings.Contains(skel, "… +") || !strings.Contains(skel, " more") {
		t.Errorf("max_tokens did not truncate with a '+N more' note:\n%s", skel)
	}
	// Never mid-line: every line is complete (the "+N more" note is its own
	// line, and the closing brace is present).
	if !strings.HasSuffix(strings.TrimRight(skel, "\n"), "}") {
		t.Errorf("truncated skeleton must still close the container brace:\n%s", skel)
	}
	tok := db.ApproxTokens(skel)
	t.Logf("truncated container: %d methods seeded, skeleton=%d tokens (cap 80)", nMethods, tok)
}

// ── context + symbols parity (DB-only render, no file read) ─────────────

// context with mode=skeleton renders the primary symbol from the DB and works
// after the file is deleted.
func TestContext_ModeSkeleton_WorksAfterFileDeleted(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	sym, dir := seedDBSkelFunc(t, srv)

	if err := os.Remove(filepath.Join(dir, "bank.go")); err != nil {
		t.Fatalf("remove fixture: %v", err)
	}
	res, err := srv.handleContext(context.Background(), makeReq(map[string]any{
		"id": sym.ID, "project": "dbskel", "mode": "skeleton",
	}))
	if err != nil {
		t.Fatalf("handleContext: %v", err)
	}
	body := decode(t, res)
	symEntry, _ := body["symbol"].(map[string]any)
	if symEntry == nil {
		t.Fatalf("context response missing symbol; body=%v", body)
	}
	src, _ := symEntry["source"].(string)
	if !strings.Contains(src, "func Charge(acct *Account, amount int64) (int64, error)") {
		t.Errorf("context skeleton lost signature after deletion:\n%s", src)
	}
	meta, _ := body["_meta"].(map[string]any)
	if meta == nil || meta["mode"] != modeSkeletonValue {
		t.Errorf("context _meta.mode=skeleton missing; _meta=%v", body["_meta"])
	}
	// #2047: context renders the primary symbol from the index with no file
	// read, so it carries the same silent-stale risk as the single-symbol
	// path — it must ALSO stamp the honesty marker, and (the file is gone,
	// so the os.Stat fails) surface the skeleton_index_stale advisory.
	if meta == nil || meta["disk_verified"] != false {
		t.Errorf("expected context _meta.disk_verified=false honesty marker; _meta=%v", meta)
	}
	if meta == nil || meta["rendered_from"] != "index" {
		t.Errorf("expected context _meta.rendered_from=\"index\"; _meta=%v", meta)
	}
	if !slices.Contains(warningsV2Codes(body), "skeleton_index_stale") {
		t.Errorf("expected context warnings_v2 code=skeleton_index_stale after deletion; body=%v", body)
	}
}

// symbols (batch) with mode=skeleton renders each entry from the DB and works
// after the file is deleted.
func TestSymbols_ModeSkeleton_BatchAfterFileDeleted(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	sym, dir := seedDBSkelFunc(t, srv)

	if err := os.Remove(filepath.Join(dir, "bank.go")); err != nil {
		t.Fatalf("remove fixture: %v", err)
	}
	// symbols defaults to a metadata-only compact field set; request source
	// so the skeleton render is included (same contract as detail=skeleton).
	res, err := srv.handleSymbols(context.Background(), makeReq(map[string]any{
		"ids": []any{sym.ID}, "project": "dbskel", "mode": "skeleton",
		"fields": "id,name,signature,source",
	}))
	if err != nil {
		t.Fatalf("handleSymbols: %v", err)
	}
	body := decode(t, res)
	arr, _ := body["symbols"].([]any)
	if len(arr) != 1 {
		t.Fatalf("expected 1 result; got %d (body=%v)", len(arr), body)
	}
	entry, _ := arr[0].(map[string]any)
	src, _ := entry["source"].(string)
	if !strings.Contains(src, "func Charge(acct *Account, amount int64) (int64, error)") {
		t.Errorf("batch skeleton lost signature after deletion:\n%s", src)
	}
	meta, _ := body["_meta"].(map[string]any)
	if meta == nil || meta["mode"] != modeSkeletonValue {
		t.Errorf("batch _meta.mode=skeleton missing; _meta=%v", body["_meta"])
	}
	// #2047: every entry in a skeleton batch is index-rendered with no file
	// read, so the whole batch is structure-derived and not byte-verified.
	// The call-wide honesty marker guarantees no entry is presented as
	// disk-current. (Per-entry stat gating is deliberately not done on the
	// batch path — see handleSymbols — so no skeleton_index_stale warning is
	// asserted here; the marker is the contract.)
	if meta == nil || meta["disk_verified"] != false {
		t.Errorf("expected batch _meta.disk_verified=false honesty marker; _meta=%v", meta)
	}
	if meta == nil || meta["rendered_from"] != "index" {
		t.Errorf("expected batch _meta.rendered_from=\"index\"; _meta=%v", meta)
	}
}

// contextSkelMeta calls handleContext with mode=skeleton (optionally lite) and
// returns the decoded _meta plus the full body.
func contextSkelMeta(t *testing.T, srv *Server, id string, lite bool) (map[string]any, map[string]any) {
	t.Helper()
	args := map[string]any{"id": id, "project": "dbskel", "mode": "skeleton"}
	if lite {
		args["lite"] = true
	}
	res, err := srv.handleContext(context.Background(), makeReq(args))
	if err != nil {
		t.Fatalf("handleContext(lite=%v): %v", lite, err)
	}
	body := decode(t, res)
	meta, _ := body["_meta"].(map[string]any)
	return meta, body
}

// #2047: handleContext (both the full and lite paths) renders the primary
// symbol's signature from indexed db.Symbol fields with NO file read — the
// exact silent-stale hole the single-symbol path closes. This proves the gate
// is wired on BOTH context paths: clean → honesty marker, no warning;
// shrink → stat proves drift → skeleton_index_stale.
func TestContext_ModeSkeleton_StalenessGate(t *testing.T) {
	t.Parallel()

	for _, lite := range []bool{false, true} {
		lite := lite
		name := "full_path"
		if lite {
			name = "lite_path"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// Clean: honesty marker present, no stale warning.
			t.Run("clean_marker_no_warning", func(t *testing.T) {
				t.Parallel()
				srv, _, _ := newTestServer(t)
				sym, _ := seedDBSkelFunc(t, srv)
				meta, body := contextSkelMeta(t, srv, sym.ID, lite)
				if meta == nil || meta["disk_verified"] != false {
					t.Errorf("expected context _meta.disk_verified=false; _meta=%v", meta)
				}
				if meta == nil || meta["rendered_from"] != "index" {
					t.Errorf("expected context _meta.rendered_from=\"index\"; _meta=%v", meta)
				}
				if slices.Contains(warningsV2Codes(body), "skeleton_index_stale") {
					t.Errorf("clean context skeleton must NOT carry a stale warning; body=%v", body)
				}
			})

			// Shrink: rewrite far shorter so EndByte exceeds file size → the
			// size-stat catches it.
			t.Run("shrink_emits_stale_warning", func(t *testing.T) {
				t.Parallel()
				srv, _, _ := newTestServer(t)
				sym, dir := seedDBSkelFunc(t, srv)
				if err := os.WriteFile(filepath.Join(dir, "bank.go"), []byte("// gone\n"), 0o600); err != nil {
					t.Fatalf("shrink fixture: %v", err)
				}
				meta, body := contextSkelMeta(t, srv, sym.ID, lite)
				if meta == nil || meta["disk_verified"] != false {
					t.Errorf("expected context _meta.disk_verified=false after shrink; _meta=%v", meta)
				}
				if !slices.Contains(warningsV2Codes(body), "skeleton_index_stale") {
					t.Errorf("expected context warnings_v2 code=skeleton_index_stale after shrink; body=%v", body)
				}
			})
		})
	}
}
