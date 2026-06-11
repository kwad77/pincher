// SPDX-License-Identifier: MIT

package main

import (
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// hook-redirect-v2: the per-redirect savings telemetry must surface on
// both reporting paths — `pincher hook-stats --export-7d` (savings
// block) and `pincher stats` (HOOK 7D section).

func seedMeasuredRedirect(t *testing.T, store *db.Store, served, baseline int64) {
	t.Helper()
	if err := store.LogHookInvocation(db.HookInvocation{
		TS: time.Now().UnixNano(), SessionID: "s", ToolName: "Read",
		FilePath: "f.go", FileBytes: baseline * 4,
		Decision: "redirect_advisory", SuggestedTool: "context",
		SuggestedArgs:   `{"id":"x","lite":true,"max_tokens":24000}`,
		EstTokensServed: served, BaselineTokens: baseline,
	}); err != nil {
		t.Fatalf("seed redirect: %v", err)
	}
}

func TestBuildHookStatsExport_SavingsBlock(t *testing.T) {
	store := newHookTestStore(t)
	seedMeasuredRedirect(t, store, 400, 12500)
	seedMeasuredRedirect(t, store, 600, 7500)

	report, err := buildHookStatsExport(store, false)
	if err != nil {
		t.Fatalf("buildHookStatsExport: %v", err)
	}
	if report.Savings.EstTokensServed != 1000 {
		t.Errorf("EstTokensServed = %d, want 1000", report.Savings.EstTokensServed)
	}
	if report.Savings.BaselineTokens != 20000 {
		t.Errorf("BaselineTokens = %d, want 20000", report.Savings.BaselineTokens)
	}
	if got, want := report.Savings.EstSavedPct, 95.0; got != want {
		t.Errorf("EstSavedPct = %v, want %v", got, want)
	}
}

func TestBuildHookStatsExport_SavingsZeroWhenUnmeasured(t *testing.T) {
	store := newHookTestStore(t)
	// Legacy-shaped redirect: no telemetry recorded.
	if err := store.LogHookInvocation(db.HookInvocation{
		TS: time.Now().UnixNano(), SessionID: "s", ToolName: "Read",
		FilePath: "f.go", Decision: "redirect_advisory", SuggestedTool: "context",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	report, err := buildHookStatsExport(store, false)
	if err != nil {
		t.Fatalf("buildHookStatsExport: %v", err)
	}
	if report.Savings.EstTokensServed != 0 || report.Savings.BaselineTokens != 0 || report.Savings.EstSavedPct != 0 {
		t.Errorf("unmeasured rows must leave the savings block zeroed; got %+v", report.Savings)
	}
}

func TestBuildStatsReport_Hook7dPresent(t *testing.T) {
	store := newHookTestStore(t)
	seedMeasuredRedirect(t, store, 500, 10000)

	report, err := buildStatsReport(store, t.TempDir())
	if err != nil {
		t.Fatalf("buildStatsReport: %v", err)
	}
	if report.Hook7d == nil {
		t.Fatal("Hook7d block missing despite measured redirect telemetry")
	}
	if report.Hook7d.Redirects != 1 {
		t.Errorf("Redirects = %d, want 1", report.Hook7d.Redirects)
	}
	if report.Hook7d.EstTokensServed != 500 || report.Hook7d.BaselineTokens != 10000 {
		t.Errorf("savings = served %d / baseline %d, want 500 / 10000",
			report.Hook7d.EstTokensServed, report.Hook7d.BaselineTokens)
	}
	if report.Hook7d.EstSavedPct != 95.0 {
		t.Errorf("EstSavedPct = %v, want 95.0", report.Hook7d.EstSavedPct)
	}
}

func TestBuildStatsReport_Hook7dOmittedWithoutTelemetry(t *testing.T) {
	store := newHookTestStore(t)
	report, err := buildStatsReport(store, t.TempDir())
	if err != nil {
		t.Fatalf("buildStatsReport: %v", err)
	}
	if report.Hook7d != nil {
		t.Errorf("Hook7d must be omitted when no measured redirects exist; got %+v", report.Hook7d)
	}
}

func TestFormatStatsText_Hook7dSection(t *testing.T) {
	r := &StatsReport{
		DataDir: "/tmp/x",
		Hook7d: &HookSavingsReport{
			Redirects:       3,
			EstTokensServed: 1200,
			BaselineTokens:  30000,
			EstSavedPct:     96.0,
		},
	}
	out := formatStatsText(r)
	if !strings.Contains(out, "HOOK 7D") {
		t.Errorf("HOOK 7D header missing:\n%s", out)
	}
	for _, want := range []string{"Redirects (7d):", "Est. served:", "Read baseline:", "Est. saved:", "96.0%"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	// Section must be absent when the block is nil.
	r.Hook7d = nil
	if out := formatStatsText(r); strings.Contains(out, "HOOK 7D") {
		t.Errorf("HOOK 7D section rendered without data:\n%s", out)
	}
}
