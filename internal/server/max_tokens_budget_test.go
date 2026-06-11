package server

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// seedMaxTokensProject seeds a project with one large function ("Big",
// ~40 padded lines) that CALLS one medium helper ("Helper", ~30 lines).
// Returns the two source strings so tests can budget against their
// real ApproxTokens counts.
func seedMaxTokensProject(t *testing.T, srv *Server, store *db.Store, pid string) (bigSrc, depSrc string) {
	t.Helper()
	repoDir := t.TempDir()

	var sb strings.Builder
	sb.WriteString("func Big() {\n")
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&sb, "\tline%02d := %d // padding padding padding padding\n", i, i)
	}
	sb.WriteString("}\n")
	bigSrc = sb.String()

	var db2 strings.Builder
	db2.WriteString("func Helper() {\n")
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&db2, "\thelp%02d := %d // padding padding padding padding\n", i, i)
	}
	db2.WriteString("}\n")
	depSrc = db2.String()

	writeGoFile(t, repoDir, "pkg/big.go", bigSrc)
	writeGoFile(t, repoDir, "pkg/dep.go", depSrc)

	store.UpsertProject(db.Project{ID: pid, Path: repoDir, Name: pid, IndexedAt: time.Now()})
	store.BulkUpsertSymbols([]db.Symbol{
		{ID: pid + "-big", ProjectID: pid, FilePath: "pkg/big.go", Name: "Big",
			QualifiedName: "pkg.Big", Kind: "Function", Language: "Go",
			StartByte: 0, EndByte: len(bigSrc), StartLine: 1, EndLine: 42},
		{ID: pid + "-dep", ProjectID: pid, FilePath: "pkg/dep.go", Name: "Helper",
			QualifiedName: "pkg.Helper", Kind: "Function", Language: "Go",
			StartByte: 0, EndByte: len(depSrc), StartLine: 1, EndLine: 32},
	})
	store.BulkUpsertEdges([]db.Edge{
		{ProjectID: pid, FromID: pid + "-big", ToID: pid + "-dep", Kind: "CALLS", Confidence: 1.0},
	})
	srv.sessionID = pid
	return bigSrc, depSrc
}

func budgetWarningCodes(t *testing.T, m map[string]any) []string {
	t.Helper()
	meta, _ := m["_meta"].(map[string]any)
	if meta == nil {
		return nil
	}
	raw, _ := meta["warnings_v2"].([]any)
	var codes []string
	for _, w := range raw {
		if wm, ok := w.(map[string]any); ok {
			if c, _ := wm["code"].(string); c != "" {
				codes = append(codes, c)
			}
		}
	}
	return codes
}

func TestHandleContext_MaxTokens_TrimsPrimaryAndCallees(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	bigSrc, _ := seedMaxTokensProject(t, srv, store, "mtctx1")

	budget := db.ApproxTokens(bigSrc) / 2
	result, err := srv.handleContext(context.Background(), makeReq(map[string]any{
		"id": "mtctx1-big", "max_tokens": budget,
	}))
	if err != nil {
		t.Fatalf("handleContext: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", decode(t, result))
	}
	m := decode(t, result)

	symMap, _ := m["symbol"].(map[string]any)
	if symMap == nil {
		t.Fatal("missing symbol section")
	}
	src, _ := symMap["source"].(string)
	if len(src) >= len(bigSrc) {
		t.Errorf("expected primary source truncation: got %d bytes, full is %d", len(src), len(bigSrc))
	}
	if got := db.ApproxTokens(src); got > budget {
		t.Errorf("trimmed source exceeds budget: %d > %d", got, budget)
	}
	if !strings.HasPrefix(bigSrc, src) {
		t.Error("line-boundary cut must be a prefix of the original source")
	}

	callees, _ := m["callees"].([]any)
	if len(callees) != 1 {
		t.Fatalf("expected 1 callee entry, got %d", len(callees))
	}
	entry, _ := callees[0].(map[string]any)
	if entry["source_omitted"] != true {
		t.Errorf("expected callee source_omitted:true past budget, got %v", entry)
	}
	if _, hasSource := entry["source"]; hasSource {
		t.Error("omitted callee entry must not carry a source key")
	}
	if entry["id"] == nil || entry["name"] == nil {
		t.Error("omitted callee entry must keep id+name metadata")
	}

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

func TestHandleContext_MaxTokensOmitted_LegacyShape(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	bigSrc, depSrc := seedMaxTokensProject(t, srv, store, "mtctx2")

	result, err := srv.handleContext(context.Background(), makeReq(map[string]any{
		"id": "mtctx2-big",
	}))
	if err != nil {
		t.Fatalf("handleContext: %v", err)
	}
	m := decode(t, result)

	symMap, _ := m["symbol"].(map[string]any)
	if src, _ := symMap["source"].(string); src != bigSrc {
		t.Error("without max_tokens the full primary source must ship")
	}
	callees, _ := m["callees"].([]any)
	if len(callees) != 1 {
		t.Fatalf("expected 1 callee, got %d", len(callees))
	}
	entry, _ := callees[0].(map[string]any)
	if src, _ := entry["source"].(string); src != depSrc {
		t.Error("without max_tokens the full callee source must ship")
	}
	for _, c := range budgetWarningCodes(t, m) {
		if c == "budget_truncated" {
			t.Error("no budget warning expected when max_tokens is omitted")
		}
	}
}

func TestHandleContext_MaxTokens_LitePath(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	bigSrc, _ := seedMaxTokensProject(t, srv, store, "mtctx3")

	budget := db.ApproxTokens(bigSrc) / 3
	result, err := srv.handleContext(context.Background(), makeReq(map[string]any{
		"id": "mtctx3-big", "lite": true, "max_tokens": budget,
	}))
	if err != nil {
		t.Fatalf("handleContext lite: %v", err)
	}
	m := decode(t, result)
	src, _ := m["source"].(string)
	if len(src) == 0 || len(src) >= len(bigSrc) {
		t.Errorf("lite source should be a non-empty truncation: got %d bytes of %d", len(src), len(bigSrc))
	}
	if got := db.ApproxTokens(src); got > budget {
		t.Errorf("lite trimmed source exceeds budget: %d > %d", got, budget)
	}
}

func TestHandleSymbols_MaxTokens_OmitsSourcePastBudget(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	bigSrc, _ := seedMaxTokensProject(t, srv, store, "mtsym1")

	// Budget covers the first symbol with a little headroom but not
	// the second (~30-line helper, far above the slack).
	budget := db.ApproxTokens(bigSrc) + 5
	result, err := srv.handleSymbols(context.Background(), makeReq(map[string]any{
		"ids":        []any{"mtsym1-big", "mtsym1-dep"},
		"fields":     "id,source",
		"max_tokens": budget,
	}))
	if err != nil {
		t.Fatalf("handleSymbols: %v", err)
	}
	m := decode(t, result)
	arr, _ := m["symbols"].([]any)
	if len(arr) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(arr))
	}
	first, _ := arr[0].(map[string]any)
	second, _ := arr[1].(map[string]any)
	if src, _ := first["source"].(string); src != bigSrc {
		t.Error("first entry should carry its full source within budget")
	}
	if second["source_omitted"] != true {
		t.Errorf("second entry should be source_omitted past budget, got %v", second)
	}
	if src, _ := second["source"].(string); src != "" {
		t.Error("omitted entry must not carry source bytes")
	}

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

func TestTruncateSourceToTokens_SingleGiantLine_HardCut(t *testing.T) {
	t.Parallel()
	src := strings.Repeat("x", 4000) // one line, ~1000 tokens
	out, dropped, truncated := truncateSourceToTokens(src, 50)
	if !truncated {
		t.Fatal("expected truncation")
	}
	if len(out) == 0 {
		t.Error("hard cut should keep a partial first line, not return empty")
	}
	if got := db.ApproxTokens(out); got > 50 {
		t.Errorf("hard cut exceeds budget: %d > 50", got)
	}
	if dropped == 0 {
		t.Error("dropped count should be non-zero")
	}
}
