// SPDX-License-Identifier: MIT

package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

func TestSavingsReport_CompleteDataAllowsAggregateClaim(t *testing.T) {
	t.Parallel()
	store := newSavingsTestStore(t)
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	saved := int64(900)
	pct := 90.0
	if err := store.RecordToolCalls([]db.ToolCallEvent{
		{SessionID: "s1", Tool: "search", ComplexityTier: "lite", TS: now.Add(-1 * time.Hour), TokensUsed: 100, TokensSaved: &saved, TokensSavedPct: &pct, RequestID: "r1"},
	}); err != nil {
		t.Fatalf("RecordToolCalls: %v", err)
	}

	report, err := buildSavingsReport(store, t.TempDir(), 7*24*time.Hour, "", now)
	if err != nil {
		t.Fatalf("buildSavingsReport: %v", err)
	}
	if !report.RecentWindow.AggregateClaimAllowed {
		t.Fatalf("complete data should allow aggregate claim: %+v", report.RecentWindow.SchemaGaps)
	}
	if report.RecentWindow.BaselineTokens != 1000 {
		t.Fatalf("baseline_tokens = %d, want 1000", report.RecentWindow.BaselineTokens)
	}
	if report.RecentWindow.SavingsPct == nil || *report.RecentWindow.SavingsPct < 89.9 || *report.RecentWindow.SavingsPct > 90.1 {
		t.Fatalf("savings_pct = %v, want ~90", report.RecentWindow.SavingsPct)
	}
	if got := report.RecentWindow.ByTool[0].BaselineMethod; got != "full_file_read" {
		t.Fatalf("baseline_method = %q, want full_file_read", got)
	}
}

func TestSavingsReport_PartialDataRefusesAggregateClaim(t *testing.T) {
	t.Parallel()
	store := newSavingsTestStore(t)
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	saved := int64(300)
	pct := 75.0
	if err := store.RecordToolCalls([]db.ToolCallEvent{
		{SessionID: "s1", Tool: "search", ComplexityTier: "lite", TS: now.Add(-1 * time.Hour), TokensUsed: 100, TokensSaved: &saved, TokensSavedPct: &pct},
		{SessionID: "s1", Tool: "architecture", ComplexityTier: "standard", TS: now.Add(-30 * time.Minute), TokensUsed: 40, TokensSaved: nil, TokensSavedPct: nil},
	}); err != nil {
		t.Fatalf("RecordToolCalls: %v", err)
	}

	report, err := buildSavingsReport(store, t.TempDir(), 7*24*time.Hour, "", now)
	if err != nil {
		t.Fatalf("buildSavingsReport: %v", err)
	}
	if report.RecentWindow.AggregateClaimAllowed {
		t.Fatalf("partial data should refuse aggregate claim")
	}
	if report.RecentWindow.SchemaGaps.MissingTokensSaved != 1 || report.RecentWindow.SchemaGaps.NoBaselineMethod != 1 {
		t.Fatalf("schema gaps = %+v, want one missing saved and one no-baseline", report.RecentWindow.SchemaGaps)
	}
	text := formatSavingsReportText(report)
	if !strings.Contains(text, "savings_pct: refused") {
		t.Fatalf("text report should refuse savings_pct claim; got:\n%s", text)
	}
}

func TestSavingsReport_SeparatesAllTimeFromNoRecentWindow(t *testing.T) {
	t.Parallel()
	store := newSavingsTestStore(t)
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	saved := int64(500)
	pct := 83.3
	if err := store.RecordToolCalls([]db.ToolCallEvent{
		{SessionID: "s1", Tool: "context", ComplexityTier: "standard", TS: now.Add(-30 * 24 * time.Hour), TokensUsed: 100, TokensSaved: &saved, TokensSavedPct: &pct},
	}); err != nil {
		t.Fatalf("RecordToolCalls: %v", err)
	}

	report, err := buildSavingsReport(store, t.TempDir(), 7*24*time.Hour, "", now)
	if err != nil {
		t.Fatalf("buildSavingsReport: %v", err)
	}
	if report.AllTime.Calls != 1 {
		t.Fatalf("all-time calls = %d, want 1", report.AllTime.Calls)
	}
	if !report.RecentWindow.NoRecentData || report.RecentWindow.Calls != 0 {
		t.Fatalf("recent window should be empty/stale; got %+v", report.RecentWindow)
	}
	if report.RecentWindow.AggregateClaimAllowed {
		t.Fatalf("no recent data must refuse aggregate claims")
	}
}

func TestSavingsReport_ProjectFilterIsAnnotatedNotApplied(t *testing.T) {
	t.Parallel()
	store := newSavingsTestStore(t)
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	report, err := buildSavingsReport(store, t.TempDir(), 7*24*time.Hour, "/tmp/project", now)
	if err != nil {
		t.Fatalf("buildSavingsReport: %v", err)
	}
	if report.ProjectFilter.Applied {
		t.Fatalf("project filter should not be marked applied until per-call project_id exists")
	}
	if !strings.Contains(report.ProjectFilter.Reason, "do not persist project_id") {
		t.Fatalf("unexpected project filter reason: %q", report.ProjectFilter.Reason)
	}
}

func TestParseSavingsSince_DaysSuffix(t *testing.T) {
	t.Parallel()
	d, err := parseSavingsSince("7d")
	if err != nil {
		t.Fatalf("parseSavingsSince: %v", err)
	}
	if d != 7*24*time.Hour {
		t.Fatalf("duration = %v, want 168h", d)
	}
}

func newSavingsTestStore(t *testing.T) *db.Store {
	t.Helper()
	store, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
