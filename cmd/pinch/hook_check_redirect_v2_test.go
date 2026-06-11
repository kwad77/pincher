// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
	"github.com/zeebo/xxh3"
)

// hook-redirect-v2: budgeted Read redirects, repeat-read awareness, and
// per-redirect savings telemetry. Mirrors the decideHook-level pattern
// of hook_check_test.go — each branch unit-isolated, no stdin/stdout
// shim.

func TestDecideReadHook_LimitSet_RedirectsWithBudget(t *testing.T) {
	t.Parallel()
	// Pre-v2 the hook passed through whenever `limit` was set. Now a
	// limit (without offset) still redirects, carrying a max_tokens
	// budget of limit × 12 so the suggested context call can't cost
	// more window than the Read it replaces.
	store := newHookTestStore(t)
	projectDir := t.TempDir()
	relPath := "internal/server/server.go"
	indexLargeFakeFile(t, store, projectDir, relPath, 50000)

	in := hookCheckInput{
		ToolName: "Read",
		ToolInput: map[string]any{
			"file_path": filepath.Join(projectDir, relPath),
			"limit":     float64(50), // JSON numbers decode as float64
		},
	}
	d := decideHook(store, in, false)
	if !d.Continue {
		t.Fatalf("advisory mode must never block; got %+v", d)
	}
	if d.Decision != "redirect_advisory" {
		t.Fatalf("decision = %q, want redirect_advisory (limit no longer forces pass-through)", d.Decision)
	}
	if !strings.Contains(d.SuggestedArgs, `"max_tokens":600`) {
		t.Errorf("suggested args should carry max_tokens=600 (50 lines × 12); got %s", d.SuggestedArgs)
	}
	if !strings.Contains(d.SuggestedArgs, `"lite":true`) {
		t.Errorf("budgeted redirect must still request lite mode; got %s", d.SuggestedArgs)
	}
}

func TestDecideReadHook_TinyLimit_BudgetFloor400(t *testing.T) {
	t.Parallel()
	store := newHookTestStore(t)
	projectDir := t.TempDir()
	relPath := "f.go"
	indexLargeFakeFile(t, store, projectDir, relPath, 50000)

	in := hookCheckInput{
		ToolName: "Read",
		ToolInput: map[string]any{
			"file_path": filepath.Join(projectDir, relPath),
			"limit":     float64(10), // 10 × 12 = 120 < floor
		},
	}
	d := decideHook(store, in, false)
	if d.Decision != "redirect_advisory" {
		t.Fatalf("decision = %q, want redirect_advisory", d.Decision)
	}
	if !strings.Contains(d.SuggestedArgs, `"max_tokens":400`) {
		t.Errorf("budget must floor at 400 tokens; got %s", d.SuggestedArgs)
	}
}

func TestDecideReadHook_NoLimit_DefaultBudgetMirrorsReadPage(t *testing.T) {
	t.Parallel()
	// Without an explicit limit, native Read still truncates at 2000
	// lines — the default budget mirrors that implicit page (2000 × 12)
	// so an uncapped redirect matches Read's own worst case.
	store := newHookTestStore(t)
	projectDir := t.TempDir()
	relPath := "f.go"
	indexLargeFakeFile(t, store, projectDir, relPath, 50000)

	in := hookCheckInput{
		ToolName:  "Read",
		ToolInput: map[string]any{"file_path": filepath.Join(projectDir, relPath)},
	}
	d := decideHook(store, in, false)
	if d.Decision != "redirect_advisory" {
		t.Fatalf("decision = %q, want redirect_advisory", d.Decision)
	}
	want := fmt.Sprintf(`"max_tokens":%d`, readDefaultLimitLines*tokensPerLineHeuristic)
	if !strings.Contains(d.SuggestedArgs, want) {
		t.Errorf("default budget should be %s; got %s", want, d.SuggestedArgs)
	}
}

func TestDecideReadHook_OffsetStillPassesThrough_V2(t *testing.T) {
	t.Parallel()
	// Offset means the agent narrowed to a position context can't
	// reproduce — the v2 budget plumbing must not regress this.
	store := newHookTestStore(t)
	projectDir := t.TempDir()
	relPath := "f.go"
	indexLargeFakeFile(t, store, projectDir, relPath, 50000)

	in := hookCheckInput{
		ToolName: "Read",
		ToolInput: map[string]any{
			"file_path": filepath.Join(projectDir, relPath),
			"offset":    float64(100),
			"limit":     float64(50),
		},
	}
	d := decideHook(store, in, false)
	if d.Decision != "pass_through" {
		t.Errorf("offset-set Read must pass through; got %+v", d)
	}
}

func TestDecideReadHook_SavingsTelemetryPopulated(t *testing.T) {
	t.Parallel()
	store := newHookTestStore(t)
	projectDir := t.TempDir()
	relPath := "f.go"
	indexLargeFakeFile(t, store, projectDir, relPath, 50000)

	in := hookCheckInput{
		ToolName:  "Read",
		ToolInput: map[string]any{"file_path": filepath.Join(projectDir, relPath)},
	}
	d := decideHook(store, in, false)
	if d.Decision != "redirect_advisory" {
		t.Fatalf("decision = %q, want redirect_advisory", d.Decision)
	}
	// Baseline: 50000 bytes / 4 = 12500 tokens (no limit cap).
	if d.BaselineTokens != 12500 {
		t.Errorf("BaselineTokens = %d, want 12500 (stat-ed size / 4)", d.BaselineTokens)
	}
	// Largest fake symbol spans 50 bytes → 13 tokens + 400 envelope.
	if d.EstTokensServed != 413 {
		t.Errorf("EstTokensServed = %d, want 413 (span/4 + envelope)", d.EstTokensServed)
	}
	if d.EstTokensServed >= d.BaselineTokens {
		t.Errorf("redirect must beat the baseline on this shape: served %d >= baseline %d",
			d.EstTokensServed, d.BaselineTokens)
	}
}

func TestDecideReadHook_SavingsBaselineCappedByLimit(t *testing.T) {
	t.Parallel()
	// The honest baseline is what the Read would have RETURNED, not the
	// full file: with limit=50 the Read returns at most ~600 tokens, so
	// the baseline must not claim the whole 50 KB.
	store := newHookTestStore(t)
	projectDir := t.TempDir()
	relPath := "f.go"
	indexLargeFakeFile(t, store, projectDir, relPath, 50000)

	in := hookCheckInput{
		ToolName: "Read",
		ToolInput: map[string]any{
			"file_path": filepath.Join(projectDir, relPath),
			"limit":     float64(50),
		},
	}
	d := decideHook(store, in, false)
	if d.BaselineTokens != 600 {
		t.Errorf("BaselineTokens = %d, want 600 (limit 50 × 12, not file size)", d.BaselineTokens)
	}
}

func TestDecideReadHook_RepeatRead_UnchangedAdvertised(t *testing.T) {
	t.Parallel()
	store := newHookTestStore(t)
	projectDir := t.TempDir()
	relPath := "f.go"
	projectID := indexLargeFakeFile(t, store, projectDir, relPath, 50000)
	absPath := filepath.Join(projectDir, relPath)

	// Align the stored hash with the actual on-disk content (the test
	// helper writes "fakehash") so the unchanged check can pass.
	body, err := os.ReadFile(absPath)
	if err != nil {
		t.Fatalf("read seeded file: %v", err)
	}
	if err := store.SetFileHash(projectID, relPath, fmt.Sprintf("%x", xxh3.Hash(body))); err != nil {
		t.Fatalf("set file hash: %v", err)
	}
	// Simulate the earlier Read this session.
	if err := store.LogHookInvocation(db.HookInvocation{
		TS: time.Now().UnixNano(), SessionID: "sess-1", ToolName: "Read",
		FilePath: absPath, Decision: "redirect_advisory",
	}); err != nil {
		t.Fatalf("seed prior invocation: %v", err)
	}

	in := hookCheckInput{
		ToolName:  "Read",
		SessionID: "sess-1",
		ToolInput: map[string]any{"file_path": absPath},
	}
	d := decideHook(store, in, false)
	if d.Decision != "redirect_advisory" {
		t.Fatalf("decision = %q, want redirect_advisory", d.Decision)
	}
	if !strings.Contains(d.SystemMessage, "Repeat read") {
		t.Errorf("repeat read of unchanged file should be advertised; got %q", d.SystemMessage)
	}
}

func TestDecideReadHook_RepeatRead_ChangedContentNotAdvertised(t *testing.T) {
	t.Parallel()
	store := newHookTestStore(t)
	projectDir := t.TempDir()
	relPath := "f.go"
	indexLargeFakeFile(t, store, projectDir, relPath, 50000)
	absPath := filepath.Join(projectDir, relPath)

	// Stored hash stays "fakehash" — on-disk content can't match, which
	// models "file edited since the index recorded it."
	if err := store.LogHookInvocation(db.HookInvocation{
		TS: time.Now().UnixNano(), SessionID: "sess-2", ToolName: "Read",
		FilePath: absPath, Decision: "redirect_advisory",
	}); err != nil {
		t.Fatalf("seed prior invocation: %v", err)
	}

	in := hookCheckInput{
		ToolName:  "Read",
		SessionID: "sess-2",
		ToolInput: map[string]any{"file_path": absPath},
	}
	d := decideHook(store, in, false)
	if strings.Contains(d.SystemMessage, "Repeat read") {
		t.Errorf("changed content must not be advertised as unchanged; got %q", d.SystemMessage)
	}
}

func TestDecideReadHook_FirstRead_NoRepeatLine(t *testing.T) {
	t.Parallel()
	store := newHookTestStore(t)
	projectDir := t.TempDir()
	relPath := "f.go"
	indexLargeFakeFile(t, store, projectDir, relPath, 50000)

	in := hookCheckInput{
		ToolName:  "Read",
		SessionID: "sess-3",
		ToolInput: map[string]any{"file_path": filepath.Join(projectDir, relPath)},
	}
	d := decideHook(store, in, false)
	if strings.Contains(d.SystemMessage, "Repeat read") {
		t.Errorf("first read this session must not carry the repeat-read line; got %q", d.SystemMessage)
	}
}
