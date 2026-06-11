// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"regexp"
	"testing"

	"github.com/kwad77/pincher/internal/index"
)

// PR-4' (loop-substrate): watermark stamp + diff-context default-ON.

var watermarkRE = regexp.MustCompile(`^g\d+\.c\d+$`)

// Positive: when the server has an indexer, every JSON envelope stamps
// _meta.watermark = g<generation>.c<callseq>, and a completed index
// pass bumps the generation.
func TestMeta_Watermark_StampedAndBumpsOnReindex(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupComposeTestServer(t)
	srv.indexer = index.New(srv.store)

	res, err := srv.handleSearch(context.Background(), makeReq(map[string]any{
		"query": "Compute", "project": projectID,
	}))
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	body := decode(t, res)
	meta, _ := body["_meta"].(map[string]any)
	wm1, _ := meta["watermark"].(string)
	if !watermarkRE.MatchString(wm1) {
		t.Fatalf("expected watermark gN.cM, got %q", wm1)
	}

	// Complete an index pass on this server's indexer → generation bumps.
	if _, err := srv.indexer.Index(context.Background(), srv.sessionRoot, false); err != nil {
		t.Fatalf("reindex: %v", err)
	}
	res2, err := srv.handleSearch(context.Background(), makeReq(map[string]any{
		"query": "Compute", "project": projectID,
	}))
	if err != nil {
		t.Fatalf("search 2: %v", err)
	}
	meta2, _ := decode(t, res2)["_meta"].(map[string]any)
	wm2, _ := meta2["watermark"].(string)
	if !watermarkRE.MatchString(wm2) {
		t.Fatalf("expected watermark on second call, got %q", wm2)
	}
	gen := regexp.MustCompile(`^g(\d+)\.`)
	g1 := gen.FindStringSubmatch(wm1)[1]
	g2 := gen.FindStringSubmatch(wm2)[1]
	if g1 == g2 {
		t.Errorf("generation must bump after a completed index pass: %q vs %q", wm1, wm2)
	}
}

// Default-ON: a repeat context call on an unchanged file short-circuits
// to {unchanged:true} with no env var set.
func TestHandleContext_DiffContext_DefaultOn_RepeatUnchanged(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupComposeTestServer(t)

	// Resolve Compute's ID via the composite (exact-name → full envelope).
	res, err := srv.handleContextForTask(context.Background(), makeReq(map[string]any{
		"task": "Compute", "project": projectID,
	}))
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	seeds, _ := decode(t, res)["seeds"].([]any)
	if len(seeds) == 0 {
		t.Fatal("fixture must seed Compute")
	}
	id, _ := seeds[0].(map[string]any)["id"].(string)

	first, err := srv.handleContext(context.Background(), makeReq(map[string]any{"id": id}))
	if err != nil {
		t.Fatalf("context 1: %v", err)
	}
	b1 := decode(t, first)
	if b1["unchanged"] == true {
		t.Fatal("first fetch must ship the full body, not unchanged:true")
	}

	second, err := srv.handleContext(context.Background(), makeReq(map[string]any{"id": id}))
	if err != nil {
		t.Fatalf("context 2: %v", err)
	}
	b2 := decode(t, second)
	if b2["unchanged"] != true {
		t.Errorf("repeat context on an unchanged file should short-circuit to unchanged:true by default (PR-4'); got keys %v", mapKeysContextForTask(b2))
	}
}

// Opt-out: PINCHER_DIFF_CONTEXT=0 restores ship-full-body-every-time.
func TestHandleContext_DiffContext_EnvZero_Disables(t *testing.T) {
	t.Setenv("PINCHER_DIFF_CONTEXT", "0")
	srv, _, projectID := setupComposeTestServer(t)

	res, err := srv.handleContextForTask(context.Background(), makeReq(map[string]any{
		"task": "Compute", "project": projectID,
	}))
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	seeds, _ := decode(t, res)["seeds"].([]any)
	if len(seeds) == 0 {
		t.Fatal("fixture must seed Compute")
	}
	id, _ := seeds[0].(map[string]any)["id"].(string)

	for i := 0; i < 2; i++ {
		r, err := srv.handleContext(context.Background(), makeReq(map[string]any{"id": id}))
		if err != nil {
			t.Fatalf("context %d: %v", i, err)
		}
		b := decode(t, r)
		if b["unchanged"] == true {
			t.Errorf("call %d: diff-context must be disabled under PINCHER_DIFF_CONTEXT=0", i)
		}
		symMap, _ := b["symbol"].(map[string]any)
		if symMap == nil || symMap["source"] == nil {
			t.Errorf("call %d: full source must ship when diff-context is off", i)
		}
	}
}
