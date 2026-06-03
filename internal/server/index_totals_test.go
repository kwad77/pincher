// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"testing"
)

// #734: the MCP `index` handler reported IndexResult.Symbols/.Edges/.Files
// raw — but those are per-run accumulators. .Symbols/.Files count only
// files reprocessed this run, and .Edges is additionally inflated by the
// whole-project resolve passes. On an incremental re-index (every file
// hash-skipped) the handler therefore reported symbols:0 / a bogus edge
// count, disagreeing with `health`'s true graph totals. The handler now
// fetches totals from GraphStats, matching the CLI json/text path.
func TestHandleIndex_IncrementalReindexReportsGraphTotals(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	repoDir := t.TempDir()
	writeGoFile(t, repoDir, "pkg/service.go", simpleGoSrc)

	// First index — fresh, so per-run counts == graph totals.
	r1, err := srv.handleIndex(context.Background(), makeReq(map[string]any{"path": repoDir}))
	if err != nil || r1.IsError {
		t.Fatalf("first handleIndex: err=%v isErr=%v body=%v", err, r1.IsError, decode(t, r1))
	}
	m1 := decode(t, r1)
	syms1 := int(m1["symbols"].(float64))
	edges1 := int(m1["edges"].(float64))
	files1 := int(m1["files"].(float64))
	if syms1 == 0 {
		t.Fatalf("first index reported 0 symbols on a real Go file: %v", m1)
	}

	// Second index — nothing changed on disk, so every file hash-skips.
	// Pre-#734 this returned symbols:0 (per-run accumulator) even though
	// the graph still holds syms1 symbols.
	r2, err := srv.handleIndex(context.Background(), makeReq(map[string]any{"path": repoDir}))
	if err != nil || r2.IsError {
		t.Fatalf("second handleIndex: err=%v isErr=%v body=%v", err, r2.IsError, decode(t, r2))
	}
	m2 := decode(t, r2)
	syms2 := int(m2["symbols"].(float64))
	edges2 := int(m2["edges"].(float64))
	files2 := int(m2["files"].(float64))

	if syms2 != syms1 {
		t.Errorf("incremental re-index symbols = %d, want %d (graph total, not per-run delta)", syms2, syms1)
	}
	if edges2 != edges1 {
		t.Errorf("incremental re-index edges = %d, want %d (graph total, not per-run delta)", edges2, edges1)
	}
	if files2 != files1 {
		t.Errorf("incremental re-index files = %d, want %d (total in graph)", files2, files1)
	}

	// reprocessed is the per-run signal — 0 on a fully hash-skipped run.
	if rp, ok := m2["reprocessed"].(float64); !ok || int(rp) != 0 {
		t.Errorf("incremental re-index reprocessed = %v, want 0 (nothing changed on disk)", m2["reprocessed"])
	}
	// skipped should account for the unchanged file(s).
	if sk, ok := m2["skipped"].(float64); !ok || int(sk) < 1 {
		t.Errorf("incremental re-index skipped = %v, want >=1 (the unchanged file)", m2["skipped"])
	}
}
