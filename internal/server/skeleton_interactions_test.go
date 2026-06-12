// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// Regression tests for the three CONFIRMED mode=skeleton interaction bugs in
// #2052 (v1.10.0). Each asserts the FIXED contract; reverting the corresponding
// server.go change reproduces the original failure.

// ---- shared seeding / call helpers ----

// seedContainerN registers a Class with n method children in project pid. Each
// child's signature is padded to control its per-entry token cost.
func seedContainerN(t *testing.T, srv *Server, pid, dir string, n int, sigLen int) db.Symbol {
	t.Helper()
	if err := srv.store.UpsertProject(db.Project{ID: pid, Path: dir, Name: pid}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}
	container := db.Symbol{
		ID: pid + "::p.Big#Class", ProjectID: pid, FilePath: "p.go",
		Name: "Big", QualifiedName: "p.Big", Kind: "Class", Language: "Go",
		Signature: "type Big struct", StartByte: 0, EndByte: 15, ExtractionConfidence: 1.0,
	}
	syms := []db.Symbol{container}
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("M%03d", i)
		pad := strings.Repeat("x", sigLen)
		syms = append(syms, db.Symbol{
			ID: pid + "::p.Big." + name + "#Method", ProjectID: pid, FilePath: "p.go",
			Name: name, QualifiedName: "p.Big." + name, Kind: "Method", Language: "Go",
			Signature:  fmt.Sprintf("func (b *Big) %s(arg_%s int) error", name, pad),
			ReturnType: "error", Parent: "p.Big", StartByte: i + 1, EndByte: i + 2,
			ExtractionConfidence: 1.0,
		})
	}
	if err := srv.store.BulkUpsertSymbols(syms); err != nil {
		t.Fatalf("bulk upsert: %v", err)
	}
	return container
}

func callSymbols(t *testing.T, srv *Server, args map[string]any) map[string]any {
	t.Helper()
	res, err := srv.handleSymbols(context.Background(), makeReq(args))
	if err != nil {
		t.Fatalf("handleSymbols: %v", err)
	}
	return decode(t, res)
}

func callContext(t *testing.T, srv *Server, args map[string]any) map[string]any {
	t.Helper()
	res, err := srv.handleContext(context.Background(), makeReq(args))
	if err != nil {
		t.Fatalf("handleContext: %v", err)
	}
	return decode(t, res)
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// ===========================================================================
// Bug 1 (H1): batch skeleton must budget against the RUNNING budgetRemaining,
// not the full max_tokens. Pre-fix an N-container skeleton batch shipped ~N×
// the budget because each entry was rendered against the full max_tokens with
// no source_omitted gate.
// ===========================================================================

func TestSkeletonBatch_BudgetNotOverrun(t *testing.T) {
	srv, _, _ := newTestServer(t)
	d1, d2, d3 := t.TempDir(), t.TempDir(), t.TempDir()
	c1 := seedContainerN(t, srv, "h1a", d1, 40, 40)
	c2 := seedContainerN(t, srv, "h1b", d2, 40, 40)
	c3 := seedContainerN(t, srv, "h1c", d3, 40, 40)

	const maxTok = 60
	body := callSymbols(t, srv, map[string]any{
		"ids":           []string{c1.ID, c2.ID, c3.ID},
		"mode":          "skeleton",
		"max_tokens":    maxTok,
		"cross_project": true,
		"fields":        "id,source",
	})
	results, _ := body["symbols"].([]any)
	if len(results) != 3 {
		t.Fatalf("want 3 results, got %d", len(results))
	}

	totalSrcTokens := 0
	beyondFirstTokens := 0
	omittedCount := 0
	for i, r := range results {
		m := r.(map[string]any)
		src, _ := m["source"].(string)
		tk := db.ApproxTokens(src)
		totalSrcTokens += tk
		if i > 0 {
			beyondFirstTokens += tk
		}
		omitted, _ := m["source_omitted"].(bool)
		if omitted {
			omittedCount++
		}
		t.Logf("entry %d: %d source tokens, source_omitted=%v", i, tk, omitted)
	}
	t.Logf("total source tokens=%d (max_tokens=%d), omitted entries=%d", totalSrcTokens, maxTok, omittedCount)

	// The first entry may consume up to ~max_tokens; every entry AFTER the
	// budget is spent must be omitted, so their combined source must not exceed
	// the budget. Pre-fix entries 2 and 3 each rendered ~max_tokens worth.
	if beyondFirstTokens > maxTok {
		t.Errorf("Bug 1: entries past the first carry %d source tokens, exceeding the remaining max_tokens=%d budget — batch skeleton renders against the full budget, not budgetRemaining", beyondFirstTokens, maxTok)
	}
	// Whole-batch source must stay within ~one entry of the budget.
	if totalSrcTokens > maxTok*2 {
		t.Errorf("Bug 1: whole-batch skeleton source=%d tokens, >2x max_tokens=%d", totalSrcTokens, maxTok)
	}
	// Over-budget entries must be marked, not silently emptied.
	if omittedCount == 0 {
		t.Errorf("Bug 1: no entry carries source_omitted=true even though the batch exceeded max_tokens=%d — over-budget skeleton entries are not gated", maxTok)
	}
}

// ===========================================================================
// Bug 2 (H4): the lite handleContext path must NOT re-truncate a mode=skeleton
// source. renderDBSkeleton self-budgets and appends "}" / "… +N more" without
// counting them, so a second truncateSourceToTokens cut dropped trailing lines
// including the closing brace. Adds the !modeSkeleton guard (matching
// handleSymbol). Asserts the rendered skeleton is always well-formed.
// ===========================================================================

func TestSkeletonLite_NeverDropsClosingBrace(t *testing.T) {
	srv, _, _ := newTestServer(t)
	dir := t.TempDir()
	writeGoFile(t, dir, "p.go", "package p\n")

	bad := 0
	for _, maxTok := range []int{8, 10, 12, 14, 16, 18, 20, 24, 28, 32, 40, 48} {
		for _, sigLen := range []int{0, 8, 20, 30} {
			pid := fmt.Sprintf("h4_%d_%d", maxTok, sigLen)
			c := seedContainerN(t, srv, pid, dir, 30, sigLen)
			body := callContext(t, srv, map[string]any{
				"id": c.ID, "project": pid, "mode": "skeleton", "lite": true,
				"max_tokens": maxTok, "cross_project": true,
			})
			src, _ := body["source"].(string)
			trimmed := strings.TrimRight(src, "\n")
			if trimmed == "" {
				continue
			}
			openBraces := strings.Count(src, "{")
			closeBraces := strings.Count(src, "}")
			endsOK := strings.HasSuffix(trimmed, "}") || strings.HasSuffix(trimmed, "more")
			if openBraces != closeBraces || !endsOK {
				bad++
				if bad <= 3 {
					t.Logf("malformed (maxTok=%d sigLen=%d) braces{%d}/}%d ends=%q:\n%q",
						maxTok, sigLen, openBraces, closeBraces, tail(trimmed, 60), src)
				}
			}
		}
	}
	if bad > 0 {
		t.Errorf("Bug 2: %d (max_tokens,sigLen) combos produced a malformed lite skeleton (unbalanced braces or dangling open structure) — the lite path re-truncated an already-budgeted skeleton", bad)
	}
}

// ===========================================================================
// Bug 3 (H1b): the batch skeleton marker block (_meta.mode / rendered_from /
// disk_verified) must be gated on includeSource. With the default compact
// fieldset (no "source"), renderDBSkeleton never runs, so the response must NOT
// advertise an index render. With fields=...,source the markers stay present.
// ===========================================================================

func TestSkeletonBatch_MarkerGatedOnIncludeSource(t *testing.T) {
	srv, _, _ := newTestServer(t)
	dir := t.TempDir()
	writeGoFile(t, dir, "p.go", "package p\n")
	c := seedContainerN(t, srv, "h1b", dir, 5, 10)

	// No fields= -> default compact fieldset excludes "source" -> no render.
	noSrc := callSymbols(t, srv, map[string]any{
		"ids":           []string{c.ID},
		"mode":          "skeleton",
		"cross_project": true,
	})
	results, _ := noSrc["symbols"].([]any)
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if _, hasSource := results[0].(map[string]any)["source"]; hasSource {
		t.Fatalf("default fieldset should exclude source; entry=%v", results[0])
	}
	meta, _ := noSrc["_meta"].(map[string]any)
	for _, k := range []string{"mode", "rendered_from", "disk_verified"} {
		if _, ok := meta[k]; ok {
			t.Errorf("Bug 3: symbols(mode=skeleton) with no source field stamped _meta.%s=%v — advertises an index render that never produced source", k, meta[k])
		}
	}

	// fields=id,source -> includeSource=true -> markers MUST be present.
	withSrc := callSymbols(t, srv, map[string]any{
		"ids":           []string{c.ID},
		"mode":          "skeleton",
		"cross_project": true,
		"fields":        "id,source",
	})
	wmeta, _ := withSrc["_meta"].(map[string]any)
	if wmeta == nil || wmeta["mode"] != modeSkeletonValue {
		t.Errorf("Bug 3 regression guard: fields=id,source must keep _meta.mode=skeleton; meta=%v", wmeta)
	}
	if wmeta["rendered_from"] != "index" {
		t.Errorf("Bug 3 regression guard: fields=id,source must keep _meta.rendered_from=index; meta=%v", wmeta)
	}
	if dv, ok := wmeta["disk_verified"].(bool); !ok || dv {
		t.Errorf("Bug 3 regression guard: fields=id,source must keep _meta.disk_verified=false; meta=%v", wmeta)
	}
}
