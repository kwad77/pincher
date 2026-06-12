// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zeebo/xxh3"

	"github.com/kwad77/pincher/internal/db"
)

// Bug-hunt repro (HIGH-1): handleSymbol never applies the max_tokens
// budget. Siblings handleContext / handleSymbols gate the source read on
// the budget and emit a budget_truncated warning; handleSymbol returns the
// full body regardless of max_tokens, so a caller that asked for ~49
// tokens gets the whole ~500-token function.

func bsHash(content string) string {
	return fmt.Sprintf("%x", xxh3.Hash([]byte(content)))
}

// seedBigSymbol writes a multi-line function, indexes one symbol covering
// its whole byte range, and records the matching file hash so staleness
// validation passes on the happy path.
func seedBigSymbol(t *testing.T, srv *Server, store *db.Store, pid string) (root, src string) {
	t.Helper()
	root = t.TempDir()
	srv.sessionRoot = root
	srv.sessionID = pid
	store.UpsertProject(db.Project{ID: pid, Path: root, Name: pid, IndexedAt: time.Now()})

	var sb strings.Builder
	sb.WriteString("func Big() {\n")
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&sb, "\tline%02d := %d // padding padding padding padding padding\n", i, i)
	}
	sb.WriteString("}\n")
	src = sb.String()

	if err := os.WriteFile(filepath.Join(root, "big.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	hash := bsHash(src)
	store.BulkUpsertSymbols([]db.Symbol{{
		ID: pid + "::big.Big#Function", ProjectID: pid, FilePath: "big.go",
		Name: "Big", QualifiedName: "big.Big", Kind: "Function", Language: "Go",
		StartByte: 0, EndByte: len(src), StartLine: 1, EndLine: 42,
		FileHash: hash, ExtractionConfidence: 1.0,
	}})
	store.SetFileHash(pid, "big.go", hash)
	return root, src
}

// TestHandleSymbol_MaxTokens_TruncatesAndWarns: a small max_tokens budget
// must cut the source at a line boundary (db.ApproxTokens within budget)
// AND emit a budget_truncated warning — parity with context/symbols/lite.
func TestHandleSymbol_MaxTokens_TruncatesAndWarns(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	_, src := seedBigSymbol(t, srv, store, "mtsym1")

	full := db.ApproxTokens(src)
	budget := full / 4 // well below the full body
	if budget < 1 {
		budget = 1
	}

	result, err := srv.handleSymbol(context.Background(), makeReq(map[string]any{
		"id": "mtsym1::big.Big#Function", "project": "mtsym1", "max_tokens": budget,
	}))
	if err != nil {
		t.Fatalf("handleSymbol: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", decode(t, result))
	}
	body := decode(t, result)

	got, _ := body["source"].(string)
	if got == "" {
		t.Fatalf("expected a (truncated) source, got empty")
	}
	if len(got) >= len(src) {
		t.Errorf("expected truncation: got %d bytes, full is %d", len(got), len(src))
	}
	if db.ApproxTokens(got) > budget {
		t.Errorf("source exceeds budget: %d > %d tokens", db.ApproxTokens(got), budget)
	}
	if !strings.HasPrefix(src, got) {
		t.Error("line-boundary cut must be a prefix of the original source")
	}

	codes := budgetWarningCodes(t, body)
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

// TestHandleSymbol_MaxTokensOmitted_FullBody: the legacy shape — no budget
// means the full body, no budget_truncated warning.
func TestHandleSymbol_MaxTokensOmitted_FullBody(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	_, src := seedBigSymbol(t, srv, store, "mtsym2")

	result, err := srv.handleSymbol(context.Background(), makeReq(map[string]any{
		"id": "mtsym2::big.Big#Function", "project": "mtsym2",
	}))
	if err != nil {
		t.Fatalf("handleSymbol: %v", err)
	}
	body := decode(t, result)
	got, _ := body["source"].(string)
	if got != src {
		t.Errorf("expected full body (%d bytes), got %d", len(src), len(got))
	}
	for _, c := range budgetWarningCodes(t, body) {
		if c == "budget_truncated" {
			t.Errorf("no budget set — should not emit budget_truncated; codes=%v", budgetWarningCodes(t, body))
		}
	}
}

// TestHandleSymbol_StaleEdit_NoWrongBytes (HIGH-2 at the server layer): a
// file edited after indexing must NOT ship a different symbol's bytes. The
// reader's staleness validation makes the source empty and the handler
// surfaces the staleness signal — never arbitrary wrong content.
func TestHandleSymbol_StaleEdit_NoWrongBytes(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	root := t.TempDir()
	srv.sessionRoot = root
	srv.sessionID = "stale1"
	store.UpsertProject(db.Project{ID: "stale1", Path: root, Name: "stale1", IndexedAt: time.Now()})

	indexed := "func A() {\n\treturn 1\n}\nfunc B() { return 2 }\n"
	hash := bsHash(indexed)
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(indexed), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	store.BulkUpsertSymbols([]db.Symbol{{
		ID: "stale1::main.A#Function", ProjectID: "stale1", FilePath: "main.go",
		Name: "A", QualifiedName: "main.A", Kind: "Function", Language: "Go",
		StartByte: 0, EndByte: 22, StartLine: 1, EndLine: 3,
		FileHash: hash, ExtractionConfidence: 1.0,
	}})
	store.SetFileHash("stale1", "main.go", hash)

	// Edit the file so bytes 0..22 now hold different content of the same
	// length-ish — the WRONG symbol's bytes would be returned without
	// validation.
	wrong := "func ZZZ() { x := 99 }\nfunc A() { return 1 }\n"
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(wrong), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	result, err := srv.handleSymbol(context.Background(), makeReq(map[string]any{
		"id": "stale1::main.A#Function", "project": "stale1",
	}))
	if err != nil {
		t.Fatalf("handleSymbol: %v", err)
	}
	body := decode(t, result)
	got, _ := body["source"].(string)
	// The critical invariant: never ship the wrong bytes.
	if got == wrong[0:22] {
		t.Fatalf("shipped WRONG bytes from stale file: %q", got)
	}
	if got != "" {
		t.Fatalf("expected empty source on stale read, got %q", got)
	}
	// And the agent must be told why source is empty.
	if !hasStaleSignal(t, body) {
		t.Fatalf("expected a staleness warning; meta=%v", body["_meta"])
	}
}

// TestHandleSymbol_NoHashFile_StaleStillSignalled (MED-3): a symbol whose
// file has NO stored hash (Document/URL kinds, pre-#236 rows) must still be
// guarded. With HIGH-2's size check, a shrunk no-hash file errors and the
// handler surfaces the signal instead of shipping a silent short read.
func TestHandleSymbol_NoHashFile_StaleStillSignalled(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	root := t.TempDir()
	srv.sessionRoot = root
	srv.sessionID = "nohash1"
	store.UpsertProject(db.Project{ID: "nohash1", Path: root, Name: "nohash1", IndexedAt: time.Now()})

	indexed := "0123456789abcdefghijABCDEFGHIJ klmnopqrstuvwxyz\n"
	if err := os.WriteFile(filepath.Join(root, "blob.txt"), []byte(indexed), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// FileHash deliberately empty (no SetFileHash, no sym.FileHash).
	store.BulkUpsertSymbols([]db.Symbol{{
		ID: "nohash1::blob#Symbol", ProjectID: "nohash1", FilePath: "blob.txt",
		Name: "blob", QualifiedName: "blob", Kind: "Symbol", Language: "Text",
		StartByte: 5, EndByte: len(indexed), StartLine: 1, EndLine: 1,
		ExtractionConfidence: 1.0,
	}})

	// Shrink the file so EndByte now exceeds the file size.
	if err := os.WriteFile(filepath.Join(root, "blob.txt"), []byte("0123\n"), 0o644); err != nil {
		t.Fatalf("shrink: %v", err)
	}

	result, err := srv.handleSymbol(context.Background(), makeReq(map[string]any{
		"id": "nohash1::blob#Symbol", "project": "nohash1",
	}))
	if err != nil {
		t.Fatalf("handleSymbol: %v", err)
	}
	body := decode(t, result)
	got, _ := body["source"].(string)
	if got != "" {
		t.Fatalf("expected empty source on shrunk no-hash file, got %q", got)
	}
	if !hasStaleSignal(t, body) {
		t.Fatalf("expected a staleness signal for shrunk no-hash file; meta=%v", body["_meta"])
	}
}

// hasStaleSignal reports whether the response carries any staleness
// indication — either a string warning mentioning the file changed, or a
// structured warnings_v2 entry coded stale/modified.
func hasStaleSignal(t *testing.T, body map[string]any) bool {
	t.Helper()
	meta, _ := body["_meta"].(map[string]any)
	if meta == nil {
		return false
	}
	if ws, ok := meta["warnings"].([]any); ok {
		for _, w := range ws {
			s := strings.ToLower(fmt.Sprint(w))
			if strings.Contains(s, "modified since last index") || strings.Contains(s, "stale") {
				return true
			}
		}
	}
	for _, c := range budgetWarningCodes(t, body) {
		if strings.Contains(c, "stale") {
			return true
		}
	}
	return false
}
