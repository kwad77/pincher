// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/kwad77/pincher/internal/index"
)

// #1787 / #1788 / #1789: v0.89 composite-audit hardening.

// #1787: parseStackFrames must not emit Go import-path components
// (github.com / org / repo / internal) or panic-message English
// (out / range / with / length / running) as candidate symbol tokens —
// they appear in every Go trace and BM25-match unrelated symbols (the
// repo name `pincher` matched a Homebrew Ruby class; `out` matched
// `runGoInstall(out io.Writer)`).
func TestParseStackFrames_DropsImportPathAndPanicNoise_1787(t *testing.T) {
	t.Parallel()
	trace := `panic: runtime error: index out of range [3] with length 3

goroutine 1 [running]:
github.com/kwad77/pincher/internal/cypher.(*Engine).runBFS(...)
	/repo/internal/cypher/engine.go:412
github.com/kwad77/pincher/internal/cypher.Execute(...)
	/repo/internal/cypher/engine.go:88`

	names, _ := parseStackFrames(trace)
	got := map[string]bool{}
	for _, n := range names {
		got[n] = true
	}

	// The load-bearing token survives.
	if !got["runBFS"] {
		t.Errorf("runBFS (the real culprit) must survive tokenization; got %v", names)
	}

	// VCS-host / org / repo / interior path segments must be dropped.
	for _, noise := range []string{"github.com", "github", "kwad77", "pincher", "internal", "com", "repo"} {
		if got[noise] {
			t.Errorf("import-path noise token %q survived — would BM25-match unrelated symbols", noise)
		}
	}
	// Go panic-message English must be dropped.
	for _, noise := range []string{"out", "range", "with", "length", "running"} {
		if got[noise] {
			t.Errorf("panic-message noise token %q survived", noise)
		}
	}
}

// #1806: parseStackFrames must filter the full Go SIGSEGV panic header
// (invalid / memory / pointer / signal / SIGSEGV / segmentation /
// violation / code / addr) and drop hyphenated path segments. #1787
// caught the import-path and `index out of range` vocabulary but missed
// the nil-pointer-dereference header — the single most common Go crash
// shape — and a local-checkout path like `/home/me/pincher-repo/...`
// still leaks the repo name `pincher` because the hyphen sits where the
// next path slash would. Both leak false-positive suspects that evict
// the real top frame from the capped rank.
func TestParseStackFrames_DropsSIGSEGVHeaderAndHyphenatedPath_1806(t *testing.T) {
	t.Parallel()
	trace := `panic: runtime error: invalid memory address or nil pointer dereference
[signal SIGSEGV: segmentation violation code=0x1 addr=0x0 pc=0x10a3f4c]

goroutine 51 [running]:
github.com/kwad77/pincher/internal/index.(*Indexer).resolveCalls(...)
	/d/ClaudeCode/pincher-repo/internal/index/indexer.go:3700
github.com/kwad77/pincher/internal/index.(*Indexer).indexImpl(...)
	/d/ClaudeCode/pincher-repo/internal/index/indexer.go:1265`

	names, _ := parseStackFrames(trace)
	got := map[string]bool{}
	for _, n := range names {
		got[n] = true
	}

	// The load-bearing token — the panic's deepest frame — survives.
	if !got["resolveCalls"] {
		t.Errorf("resolveCalls (the real crash site) must survive tokenization; got %v", names)
	}

	// SIGSEGV panic-header vocabulary must be dropped.
	for _, noise := range []string{"invalid", "memory", "pointer", "signal", "SIGSEGV", "segmentation", "violation", "code", "addr"} {
		if got[noise] {
			t.Errorf("SIGSEGV-header noise token %q survived — would BM25-match an unrelated symbol", noise)
		}
	}
	// The repo name must not leak from the hyphenated local-checkout dir.
	if got["pincher"] {
		t.Error("repo name `pincher` leaked from `pincher-repo` — would BM25-match the Homebrew Pincher class")
	}
}

// #1788: plan_change against a hub symbol must cap its blast-radius
// lists so the envelope stays under the MCP token limit — pre-fix,
// `plan_change db.Open` returned a 61 KB response that exceeded the cap
// and gave the agent nothing usable.
func TestPlanChange_HubTarget_CapsBlastRadius_1788(t *testing.T) {
	t.Parallel()
	srv, store, root := newTestServer(t)
	srv.sessionRoot = root

	writeGoFile(t, root, "go.mod", "module example.com/hub\n\ngo 1.22\n")
	writeGoFile(t, root, "hub/hub.go", "package hub\n\nfunc Target() int { return 1 }\n")
	var sb strings.Builder
	sb.WriteString("package hub\n\n")
	for i := 0; i < 30; i++ {
		sb.WriteString(fmt.Sprintf("func Caller%02d() int { return Target() }\n", i))
	}
	writeGoFile(t, root, "hub/callers.go", sb.String())

	idx := index.New(store)
	res, err := idx.Index(context.Background(), root, false)
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	srv.sessionID = res.ProjectID

	r, err := srv.handlePlanChange(context.Background(), makeReq(map[string]any{
		"target":  "hub/hub.go::hub.Target#Function",
		"project": res.ProjectID,
	}))
	if err != nil {
		t.Fatalf("handlePlanChange: %v", err)
	}
	body := decode(t, r)
	br, _ := body["blast_radius"].(map[string]any)
	if br == nil {
		t.Fatalf("no blast_radius in response: %v", body)
	}
	d1, _ := br["depth_1_callers"].([]any)
	if len(d1) > 25 {
		t.Errorf("depth_1_callers not capped: %d rows, cap is 25", len(d1))
	}
	summary, _ := br["summary"].(map[string]any)
	if c, _ := summary["depth_1_count"].(float64); int(c) < 30 {
		t.Errorf("summary.depth_1_count = %v; want >=30 — the true count must survive the cap", c)
	}
	if tr, _ := br["truncated"].(bool); !tr {
		t.Error("blast_radius.truncated must be true once the cap fired")
	}
}

// #1789: onboard_module must caveat a module whose boundary looks
// isolated — an empty external_consumers list usually means the
// cross-package method-call edges are under-resolved, not that nothing
// depends on the module.
func TestOnboardModule_EmptyConsumers_StampsBoundaryWarning_1789(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupOnboardTestServer(t)

	hasBoundaryWarning := func(body map[string]any) bool {
		meta, _ := body["_meta"].(map[string]any)
		w2, _ := meta["warnings_v2"].([]any)
		for _, w := range w2 {
			if m, ok := w.(map[string]any); ok && m["code"] == "boundary_calls_under_resolved" {
				return true
			}
		}
		return false
	}

	// Positive: `consumers/` has zero inbound edges (nothing consumes
	// it) — the boundary-incomplete warning must fire.
	res, err := srv.handleOnboardModule(context.Background(), makeReq(map[string]any{
		"directory": "consumers/",
		"project":   projectID,
	}))
	if err != nil {
		t.Fatalf("handler (consumers/): %v", err)
	}
	if !hasBoundaryWarning(decode(t, res)) {
		t.Error("onboard_module consumers/ has empty external_consumers — expected the boundary_calls_under_resolved warning")
	}

	// Control: `core/` IS consumed (UseCore → core.Foo) and its deps
	// aren't all-test — the warning must NOT fire.
	res, err = srv.handleOnboardModule(context.Background(), makeReq(map[string]any{
		"directory": "core/",
		"project":   projectID,
	}))
	if err != nil {
		t.Fatalf("handler (core/): %v", err)
	}
	if hasBoundaryWarning(decode(t, res)) {
		t.Error("core/ has a real external consumer — the boundary warning must not fire")
	}
}
