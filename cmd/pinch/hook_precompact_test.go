// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// precompact-hook: `pincher hook-check` handles PreCompact events with
// a ledger-aware compaction advisory. The summarizer can drop facts
// already checkpointed in the loop ledger / ADR store to pointers —
// but only if it knows what is recoverable; the hook tells it.
//
// Contract under test (mirrors hook_check_test patterns):
//   - seeded ledger → advisory contains loop name + counts + the
//     recoverability line
//   - empty ledger → silent pass-through (zero noise)
//   - malformed event → fail-open `{"continue": true}`
//   - ≤3 store read queries per decision (structural budget)
//   - firing counted in hook_invocations with tool_name="compact"

// seedPrecompactProject registers an indexed project rooted at
// projectDir.
func seedPrecompactProject(t *testing.T, store *db.Store, projectDir string) (projectID string) {
	t.Helper()
	projectID = "p-" + filepath.Base(projectDir)
	if err := store.UpsertProject(db.Project{
		ID: projectID, Path: projectDir, Name: filepath.Base(projectDir),
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}
	return projectID
}

// seedPrecompactLedger gives the project one loop ("m18-rollout", 3
// checkpoints, 1 open reopen-trigger) and 2 ADRs — the canonical
// seeded fixture the advisory assertions read against.
func seedPrecompactLedger(t *testing.T, store *db.Store, projectID string) {
	t.Helper()
	cps := []db.LoopCheckpoint{
		{ProjectID: projectID, LoopName: "m18-rollout", Claim: "ship the precompact hook", Decision: "started", CreatedAt: "2026-06-10T10:00:00Z"},
		{ProjectID: projectID, LoopName: "m18-rollout", Claim: "init registration merged", Decision: "keep", ReopenTrigger: "reopen if hook latency > 50ms", CreatedAt: "2026-06-10T11:00:00Z"},
		{ProjectID: projectID, LoopName: "m18-rollout", Claim: "advisory text finalized", Decision: "keep", CreatedAt: "2026-06-10T12:00:00Z"},
	}
	for _, cp := range cps {
		if _, err := store.AppendLoopCheckpoint(cp); err != nil {
			t.Fatalf("append checkpoint: %v", err)
		}
	}
	if err := store.SetADR(projectID, "hook-budget", "PreCompact handler does <=3 store queries"); err != nil {
		t.Fatalf("set adr: %v", err)
	}
	if err := store.SetADR(projectID, "telemetry-shape", "reuse hook_invocations with tool_name=compact"); err != nil {
		t.Fatalf("set adr: %v", err)
	}
}

func TestDecidePreCompact_SeededLedger_EmitsAdvisory(t *testing.T) {
	store := newHookTestStore(t)
	projectDir := t.TempDir()
	projectID := seedPrecompactProject(t, store, projectDir)
	seedPrecompactLedger(t, store, projectID)

	in := hookCheckInput{
		HookEventName: "PreCompact",
		SessionID:     "sess-precompact",
		CWD:           projectDir,
	}
	d := decidePreCompact(store, in, false)
	if !d.Continue {
		t.Fatalf("PreCompact must NEVER block compaction; got %+v", d)
	}
	if d.Decision != "ledger_advisory" {
		t.Fatalf("decision = %q, want ledger_advisory", d.Decision)
	}
	msg := d.SystemMessage
	for _, want := range []string{
		"Durable state for this project lives in pincher",
		"loop 'm18-rollout' (3 checkpoints",
		"m18-rollout#3 advisory text finalized",
		"1 open reopen-triggers",
		"2 ADRs",
		"Prefer pointers (<loop>#<seq>, ADR keys, symbol ids) over payload reproduction in the summary",
		"recoverable via loop resume / adr get",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("advisory missing %q; got %q", want, msg)
		}
	}
	if d.SuggestedTool != "loop" {
		t.Errorf("suggested tool = %q, want loop", d.SuggestedTool)
	}
	if !strings.Contains(d.SuggestedArgs, `"action":"resume"`) || !strings.Contains(d.SuggestedArgs, "m18-rollout") {
		t.Errorf("suggested args should resume the lead loop; got %s", d.SuggestedArgs)
	}
}

func TestDecidePreCompact_SubdirCwd_ResolvesProject(t *testing.T) {
	// PreCompact carries the session cwd, which is often a
	// subdirectory of the indexed project root. Longest-prefix match,
	// same rule as matchIndexedFile.
	store := newHookTestStore(t)
	projectDir := t.TempDir()
	projectID := seedPrecompactProject(t, store, projectDir)
	seedPrecompactLedger(t, store, projectID)

	in := hookCheckInput{
		HookEventName: "PreCompact",
		CWD:           filepath.Join(projectDir, "internal", "db"),
	}
	d := decidePreCompact(store, in, false)
	if d.Decision != "ledger_advisory" {
		t.Fatalf("subdir cwd should resolve to the project; got decision %q", d.Decision)
	}
	if !strings.Contains(d.SystemMessage, "m18-rollout") {
		t.Errorf("advisory should name the seeded loop; got %q", d.SystemMessage)
	}
}

func TestDecidePreCompact_EmptyLedger_SilentPassThrough(t *testing.T) {
	// Indexed project, but no loops and no ADRs → zero noise. An
	// advisory about nothing trains the summarizer to ignore the hook.
	store := newHookTestStore(t)
	projectDir := t.TempDir()
	seedPrecompactProject(t, store, projectDir)

	in := hookCheckInput{HookEventName: "PreCompact", CWD: projectDir}
	d := decidePreCompact(store, in, false)
	if !d.Continue {
		t.Fatalf("empty ledger must pass through; got %+v", d)
	}
	if d.Decision != "pass_through" {
		t.Errorf("decision = %q, want pass_through", d.Decision)
	}
	if d.SystemMessage != "" {
		t.Errorf("empty ledger must emit no advisory; got %q", d.SystemMessage)
	}
}

func TestDecidePreCompact_ADRsOnly_StillAdvises(t *testing.T) {
	// No loops, but ADRs exist → the recoverability advisory still
	// fires (ADR keys are pointers too), without a loop fragment.
	store := newHookTestStore(t)
	projectDir := t.TempDir()
	projectID := seedPrecompactProject(t, store, projectDir)
	if err := store.SetADR(projectID, "conventions", "tabs not spaces"); err != nil {
		t.Fatalf("set adr: %v", err)
	}

	in := hookCheckInput{HookEventName: "PreCompact", CWD: projectDir}
	d := decidePreCompact(store, in, false)
	if d.Decision != "ledger_advisory" {
		t.Fatalf("ADRs-only ledger should advise; got %q", d.Decision)
	}
	if !strings.Contains(d.SystemMessage, "1 ADRs") {
		t.Errorf("advisory should count ADRs; got %q", d.SystemMessage)
	}
	if strings.Contains(d.SystemMessage, "loop '") {
		t.Errorf("no loops seeded — advisory must not invent a loop fragment; got %q", d.SystemMessage)
	}
	if d.SuggestedTool != "adr" {
		t.Errorf("suggested tool = %q, want adr", d.SuggestedTool)
	}
}

func TestDecidePreCompact_CwdOutsideProjects_SilentPassThrough(t *testing.T) {
	store := newHookTestStore(t)
	projectDir := t.TempDir()
	projectID := seedPrecompactProject(t, store, projectDir)
	seedPrecompactLedger(t, store, projectID)

	in := hookCheckInput{HookEventName: "PreCompact", CWD: t.TempDir()} // unrelated dir
	d := decidePreCompact(store, in, false)
	if !d.Continue || d.Decision != "pass_through" || d.SystemMessage != "" {
		t.Errorf("cwd outside every indexed project must pass through silently; got %+v", d)
	}
}

func TestDecidePreCompact_MissingCwd_FailsOpen(t *testing.T) {
	// A malformed / partial PreCompact event (no cwd) must fail open.
	store := newHookTestStore(t)
	in := hookCheckInput{HookEventName: "PreCompact"}
	d := decidePreCompact(store, in, false)
	if !d.Continue || d.Decision != "pass_through" || d.SystemMessage != "" {
		t.Errorf("missing cwd must fail open silently; got %+v", d)
	}
}

// countingPrecompactStore wraps the real store and counts read calls —
// the ≤3-query budget is a structural contract: the handler's read
// surface is the 3-method precompactStore interface, one query each.
type countingPrecompactStore struct {
	inner *db.Store
	calls int
}

func (c *countingPrecompactStore) ListProjects() ([]db.Project, error) {
	c.calls++
	return c.inner.ListProjects()
}

func (c *countingPrecompactStore) LoopLedgerStats(projectID string) ([]db.LoopLedgerStat, error) {
	c.calls++
	return c.inner.LoopLedgerStats(projectID)
}

func (c *countingPrecompactStore) CountADRs(projectID string) (int, error) {
	c.calls++
	return c.inner.CountADRs(projectID)
}

func TestDecidePreCompact_QueryBudget_AtMostThreeReads(t *testing.T) {
	store := newHookTestStore(t)
	projectDir := t.TempDir()
	projectID := seedPrecompactProject(t, store, projectDir)
	seedPrecompactLedger(t, store, projectID)

	counting := &countingPrecompactStore{inner: store}
	in := hookCheckInput{HookEventName: "PreCompact", CWD: projectDir}
	d := decidePreCompact(counting, in, false)
	if d.Decision != "ledger_advisory" {
		t.Fatalf("fixture should advise; got %q", d.Decision)
	}
	if counting.calls > 3 {
		t.Errorf("PreCompact handler made %d store read calls, budget is 3", counting.calls)
	}
}

func TestRunHookCheckCLI_PreCompact_EndToEnd_AdvisoryAndTelemetry(t *testing.T) {
	dataDir := t.TempDir()
	projectDir := t.TempDir()

	// Seed through a store handle, then close it — hook-check opens
	// its own (single-writer DB).
	store, err := db.Open(dataDir)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	projectID := seedPrecompactProject(t, store, projectDir)
	seedPrecompactLedger(t, store, projectID)
	store.Close()

	in, _ := os.CreateTemp(t.TempDir(), "stdin")
	in.WriteString(`{"hook_event_name":"PreCompact","session_id":"sess-e2e","trigger":"auto","cwd":` + jsonQuote(projectDir) + `}`)
	in.Close()
	stdinFile, _ := os.Open(in.Name())
	defer stdinFile.Close()

	outFile, _ := os.CreateTemp(t.TempDir(), "stdout")
	defer outFile.Close()

	origStdin, origStdout := os.Stdin, os.Stdout
	os.Stdin = stdinFile
	os.Stdout = outFile
	defer func() { os.Stdin = origStdin; os.Stdout = origStdout }()

	runHookCheckCLI([]string{"--data-dir", dataDir})

	outFile.Sync()
	body, _ := os.ReadFile(outFile.Name())
	got := string(body)
	if !strings.Contains(got, `"continue":true`) {
		t.Errorf("PreCompact must never block; got %q", got)
	}
	if !strings.Contains(got, "m18-rollout") || !strings.Contains(got, "recoverable via loop resume / adr get") {
		t.Errorf("advisory missing from response; got %q", got)
	}
	if !strings.Contains(got, `"hookEventName":"PreCompact"`) || !strings.Contains(got, "additionalContext") {
		t.Errorf("advisory should also ride hookSpecificOutput.additionalContext; got %q", got)
	}

	// Telemetry: the firing lands in hook_invocations with the
	// distinct tool value "compact" — existing columns, no schema
	// change.
	store2, err := db.Open(dataDir)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer store2.Close()
	var count int
	if err := store2.DB().QueryRow(
		`SELECT COUNT(*) FROM hook_invocations WHERE tool_name='compact' AND decision='ledger_advisory' AND session_id='sess-e2e'`,
	).Scan(&count); err != nil {
		t.Fatalf("query telemetry: %v", err)
	}
	if count != 1 {
		t.Errorf("hook_invocations compact rows = %d, want 1", count)
	}
}

func TestRunHookCheckCLI_PreCompact_MalformedEvent_FailsOpen(t *testing.T) {
	// hook_event_name says PreCompact but the rest of the payload is
	// the wrong shape entirely — fields ignored, silent pass-through.
	dataDir := t.TempDir()
	in, _ := os.CreateTemp(t.TempDir(), "stdin")
	in.WriteString(`{"hook_event_name":"PreCompact","cwd":12345,"trigger":{"weird":true}}`)
	in.Close()
	stdinFile, _ := os.Open(in.Name())
	defer stdinFile.Close()

	outFile, _ := os.CreateTemp(t.TempDir(), "stdout")
	defer outFile.Close()

	origStdin, origStdout := os.Stdin, os.Stdout
	os.Stdin = stdinFile
	os.Stdout = outFile
	defer func() { os.Stdin = origStdin; os.Stdout = origStdout }()

	runHookCheckCLI([]string{"--data-dir", dataDir})

	outFile.Sync()
	body, _ := os.ReadFile(outFile.Name())
	got := string(body)
	if !strings.Contains(got, `"continue":true`) {
		t.Errorf("malformed PreCompact event must fail open; got %q", got)
	}
	if strings.Contains(got, "systemMessage") || strings.Contains(got, "additionalContext") {
		t.Errorf("malformed event must produce no advisory chrome; got %q", got)
	}
}

func TestPrecompactAdvisory_MultiLoopAndTriggerTotals(t *testing.T) {
	loops := []db.LoopLedgerStat{
		{LoopName: "lead", Checkpoints: 5, LatestSeq: 5, OpenTriggers: 2, LatestReceipt: "lead claim"},
		{LoopName: "older", Checkpoints: 2, LatestSeq: 2, OpenTriggers: 1, LatestReceipt: "older claim"},
	}
	msg := precompactAdvisory(loops, 4)
	for _, want := range []string{
		"loop 'lead' (5 checkpoints, latest: lead#5 lead claim)",
		"+1 more loop(s)",
		"3 open reopen-triggers", // summed across loops
		"4 ADRs",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("advisory missing %q; got %q", want, msg)
		}
	}
}

// jsonQuote JSON-escapes a string (Windows paths carry backslashes).
func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
