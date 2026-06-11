// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// M17 (loop-substrate): `loop handoff` + `loop export` tests — the
// pointer manifest that replaces prose handoff.md.
//
// Git seeding mirrors changes_tests_to_run_test.go / verify_change_test.go:
// a temp repo with one committed-then-modified file so the handoff's
// working-tree summary runs the REAL shared changes analysis.

// setupHandoffServer wires the standard handoff fixture: a test server
// pointed at a dirty temp git repo (main.go lines 2-3 modified)
// registered as the session project, with one seeded symbol overlapping
// the hunk so changed_symbols is non-empty.
func setupHandoffServer(t *testing.T, name string) (*Server, *db.Store, string) {
	t.Helper()
	srv, store, _ := newTestServer(t)
	repoDir := setupChangesGitRepo(t)
	store.UpsertProject(db.Project{ID: repoDir, Path: repoDir, Name: name, IndexedAt: time.Now()})
	srv.sessionID = repoDir
	srv.sessionRoot = repoDir
	mustUpsertSymbols(t, store, []db.Symbol{
		{ID: "p::main.Foo#Function", ProjectID: repoDir, FilePath: "main.go", Name: "Foo",
			QualifiedName: "main.Foo", Kind: "Function", Language: "Go",
			StartByte: 13, EndByte: 30, StartLine: 2, EndLine: 2, ExtractionConfidence: 1.0},
	})
	return srv, store, repoDir
}

// Core feature gate: handoff composes every manifest section from
// seeded state — open triggers (AWAITING HUMAN verbatim), ADR keys,
// the working-tree summary from the shared changes analysis, recent
// receipts, and re-entry seeds parsed from evidence — and appends the
// whole thing as a regular checkpoint.
func TestLoop_Handoff_ComposesManifestFromSeededState(t *testing.T) {
	t.Parallel()
	srv, store, repoDir := setupHandoffServer(t, "handoff-compose")

	if err := store.SetADR(repoDir, "STACK", "Go+SQLite"); err != nil {
		t.Fatalf("SetADR: %v", err)
	}
	loopCall(t, srv, map[string]any{
		"action": "start", "name": "m17", "project": repoDir,
		"claim": "ship loop handoff",
	})
	loopCall(t, srv, map[string]any{
		"action": "checkpoint", "name": "m17", "project": repoDir,
		"claim": "manifest composes", "decision": "Defer",
		"reopen_trigger": "re-run benchmark B after merge",
	})
	loopCall(t, srv, map[string]any{
		"action": "checkpoint", "name": "m17", "project": repoDir,
		"claim": "schema widening", "decision": "Defer",
		"reopen_trigger": "AWAITING HUMAN: approve schema v41 widening",
	})
	loopCall(t, srv, map[string]any{
		"action": "checkpoint", "name": "m17", "project": repoDir,
		"claim": "fix lands", "decision": "Accept — suite green",
		"evidence": "fixed in p::main.Foo#Function; TestFoo passes",
	})

	b := loopCall(t, srv, map[string]any{
		"action": "handoff", "name": "m17", "project": repoDir,
		"note": "wrapping the session — benchmark still open",
	})
	if seq, _ := b["seq"].(float64); int(seq) != 5 {
		t.Fatalf("handoff should append as seq=5, got %v", b["seq"])
	}
	receipt, _ := b["receipt"].(string)
	if !strings.Contains(receipt, "m17#5") || !strings.Contains(receipt, "HANDOFF") {
		t.Errorf("receipt must carry the canonical pointer + HANDOFF marker, got %q", receipt)
	}
	manifest, _ := b["manifest"].(string)
	for _, want := range []string{
		"AWAITING HUMAN: approve schema v41 widening", // verbatim, never truncated
		"re-run benchmark B after merge",              // open trigger
		"STACK",                                       // adr key
		"main.go",                                     // working-tree file list
		"tree: 1 dirty files",                         // counts from the shared changes analysis
		"recent:",                                     // last checkpoint receipts
		"seeds: p::main.Foo#Function",                 // re-entry seed parsed from evidence
	} {
		if !strings.Contains(manifest, want) {
			t.Errorf("manifest missing %q:\n%s", want, manifest)
		}
	}

	// The handoff is a REGULAR checkpoint: claim carries the note,
	// decision carries the manifest, watermark stamped as usual.
	lst := loopCall(t, srv, map[string]any{
		"action": "list", "name": "m17", "project": repoDir, "limit": 1,
	})
	cps, _ := lst["checkpoints"].([]any)
	if len(cps) != 1 {
		t.Fatalf("expected the newest checkpoint back, got %d", len(cps))
	}
	cp, _ := cps[0].(map[string]any)
	if c, _ := cp["claim"].(string); c != "HANDOFF — wrapping the session — benchmark still open" {
		t.Errorf("handoff claim = %q", c)
	}
	if d, _ := cp["decision"].(string); d != manifest {
		t.Errorf("stored decision must equal the returned manifest; got %q", d)
	}
	if wm, _ := cp["watermark"].(string); wm == "" {
		t.Error("handoff checkpoint must stamp the watermark like any other")
	}
}

// Budget gate: a fat ledger (long machine triggers + fat evidence on
// every iteration) must still compose a manifest ≤600 approx tokens —
// the manifest is a pointer table, not the work.
func TestLoop_Handoff_ManifestUnder600Tokens_FatFixture(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupComposeTestServer(t)

	fat := strings.Repeat("verbose iteration evidence with detail ", 12)
	for i := 0; i < 40; i++ {
		loopCall(t, srv, map[string]any{
			"action": "checkpoint", "name": "fat-handoff", "project": projectID,
			"claim":          fmt.Sprintf("iteration %d — %s", i, fat),
			"decision":       "Defer — " + fat,
			"evidence":       fat,
			"reopen_trigger": fmt.Sprintf("trigger %d: %s", i, fat),
		})
	}
	b := loopCall(t, srv, map[string]any{
		"action": "handoff", "name": "fat-handoff", "project": projectID,
	})
	manifest, _ := b["manifest"].(string)
	if manifest == "" {
		t.Fatal("expected a manifest")
	}
	if got := db.ApproxTokens(manifest); got > 600 {
		t.Errorf("manifest = %d approx tokens, budget is 600:\n%s", got, manifest)
	}
}

// Resume integration: after a handoff, the NEXT session's resume brief
// must LEAD with the handoff checkpoint — the manifest is the first
// thing a fresh reader sees, before the iteration tail.
func TestLoop_Resume_LeadsWithHandoff(t *testing.T) {
	t.Parallel()
	srv, _, repoDir := setupHandoffServer(t, "handoff-resume")

	for i := 0; i < 3; i++ {
		loopCall(t, srv, map[string]any{
			"action": "checkpoint", "name": "lead-loop", "project": repoDir,
			"claim": fmt.Sprintf("iteration %d", i), "decision": "Accept",
		})
	}
	loopCall(t, srv, map[string]any{
		"action": "handoff", "name": "lead-loop", "project": repoDir, "note": "pick up here",
	})

	r := loopCall(t, srv, map[string]any{
		"action": "resume", "name": "lead-loop", "project": repoDir,
	})
	brief, _ := r["brief"].([]any)
	if len(brief) != 4 {
		t.Fatalf("expected 4 checkpoints in the brief, got %d", len(brief))
	}
	first, _ := brief[0].(map[string]any)
	if c, _ := first["claim"].(string); !strings.HasPrefix(c, "HANDOFF") {
		t.Fatalf("a handoff checkpoint must LEAD the brief; first claim = %q", c)
	}
	if d, _ := first["decision"].(string); !strings.Contains(d, "recent:") {
		t.Errorf("leading checkpoint should carry the manifest as its decision, got %q", d)
	}
	// The iteration tail still reads oldest-first after the manifest.
	second, _ := brief[1].(map[string]any)
	if c, _ := second["claim"].(string); c != "iteration 0" {
		t.Errorf("tail must stay oldest-first after the hoisted handoff; brief[1].claim = %q", c)
	}
}

// Export: renders deterministic Markdown FROM the ledger, covering the
// window since the previous handoff, with claims/decisions/open
// triggers/awaiting-human sections. Never writes files — the response
// is the document.
func TestLoop_Export_DeterministicMarkdown_SinceLastHandoff(t *testing.T) {
	t.Parallel()
	srv, _, repoDir := setupHandoffServer(t, "handoff-export")

	loopCall(t, srv, map[string]any{
		"action": "start", "name": "exp", "project": repoDir, "claim": "old era work",
	})
	loopCall(t, srv, map[string]any{
		"action": "checkpoint", "name": "exp", "project": repoDir,
		"claim": "ancient iteration", "decision": "Accept",
	})
	loopCall(t, srv, map[string]any{"action": "handoff", "name": "exp", "project": repoDir}) // seq 3
	loopCall(t, srv, map[string]any{
		"action": "checkpoint", "name": "exp", "project": repoDir,
		"claim": "new era begins", "decision": "Accept — measured",
		"confidence": "measured", "evidence": "TestNewEra green",
	})
	loopCall(t, srv, map[string]any{
		"action": "checkpoint", "name": "exp", "project": repoDir,
		"claim": "deferred bit", "decision": "Defer",
		"reopen_trigger": "AWAITING HUMAN: choose flag default",
	})
	loopCall(t, srv, map[string]any{"action": "handoff", "name": "exp", "project": repoDir, "note": "era two"}) // seq 6

	e1 := loopCall(t, srv, map[string]any{"action": "export", "name": "exp", "project": repoDir})
	e2 := loopCall(t, srv, map[string]any{"action": "export", "name": "exp", "project": repoDir})
	md1, _ := e1["markdown"].(string)
	md2, _ := e2["markdown"].(string)
	if md1 == "" || md1 != md2 {
		t.Fatalf("export must render deterministic markdown; equal=%v len=%d", md1 == md2, len(md1))
	}
	if from, _ := e1["from_seq"].(float64); int(from) != 4 {
		t.Errorf("default window starts after the previous handoff (from_seq=4), got %v", e1["from_seq"])
	}
	if to, _ := e1["to_seq"].(float64); int(to) != 6 {
		t.Errorf("default window ends at the latest handoff (to_seq=6), got %v", e1["to_seq"])
	}
	for _, want := range []string{
		"# Handoff: exp",
		"## Iterations",
		"### exp#4 — new era begins",
		"- decision: Accept — measured",
		"- confidence: measured",
		"- evidence: TestNewEra green",
		"## Open reopen triggers",
		"## Awaiting human",
		"- #5: AWAITING HUMAN: choose flag default",
	} {
		if !strings.Contains(md1, want) {
			t.Errorf("export missing %q:\n%s", want, md1)
		}
	}
	// The pre-handoff era must NOT leak into the default window.
	if strings.Contains(md1, "ancient iteration") || strings.Contains(md1, "old era work") {
		t.Errorf("export must cover only the window since the previous handoff:\n%s", md1)
	}

	// max_tokens bounds the document: a tiny budget trims older
	// iterations and reports the cut.
	small := loopCall(t, srv, map[string]any{
		"action": "export", "name": "exp", "project": repoDir, "max_tokens": 160,
	})
	if omitted, _ := small["omitted_checkpoints"].(float64); int(omitted) == 0 {
		t.Errorf("max_tokens=160 must omit at least one iteration, got omitted=%v", small["omitted_checkpoints"])
	}
}

// Negative space: handoff on an empty ledger, a missing name, and an
// over-long note each rich-error instead of writing a hollow manifest.
func TestLoop_Handoff_RichErrors(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupComposeTestServer(t)

	// Empty ledger: a handoff is a pointer over recorded work.
	res, err := srv.handleLoop(context.Background(), makeReq(map[string]any{
		"action": "handoff", "name": "ghost-loop", "project": projectID,
	}))
	if err != nil {
		t.Fatalf("handleLoop: %v", err)
	}
	if !res.IsError {
		t.Fatal("handoff on an empty ledger must be a rich error, not a success envelope")
	}
	if msg := textOf(t, res); !strings.Contains(msg, "ghost-loop") {
		t.Errorf("error should name the loop, got %q", msg)
	}

	// Missing name.
	res, err = srv.handleLoop(context.Background(), makeReq(map[string]any{
		"action": "handoff", "project": projectID,
	}))
	if err != nil {
		t.Fatalf("handleLoop: %v", err)
	}
	if !res.IsError {
		t.Fatal("handoff without name must error")
	}

	// Note over 200 chars: prose creeping back in.
	loopCall(t, srv, map[string]any{
		"action": "start", "name": "noted", "project": projectID, "claim": "x",
	})
	res, err = srv.handleLoop(context.Background(), makeReq(map[string]any{
		"action": "handoff", "name": "noted", "project": projectID,
		"note": strings.Repeat("n", 201),
	}))
	if err != nil {
		t.Fatalf("handleLoop: %v", err)
	}
	if !res.IsError {
		t.Fatal("a 201-char note must rich-error (max 200)")
	}

	// Export of a loop with no checkpoints errors too.
	res, err = srv.handleLoop(context.Background(), makeReq(map[string]any{
		"action": "export", "name": "ghost-loop", "project": projectID,
	}))
	if err != nil {
		t.Fatalf("handleLoop: %v", err)
	}
	if !res.IsError {
		t.Fatal("export on an empty ledger must be a rich error")
	}
}
