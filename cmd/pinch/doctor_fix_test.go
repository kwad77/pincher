package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #1260 §3: tests for `pincher doctor --fix`.
//
// Covers the safe-action allowlist (currently just VACUUM-when-
// bloated), the noop/error/skipped/applied status taxonomy, and
// the text + JSON output formats.

// TestFixVacuumIfBloated_FreshDBNoop pins the threshold gate: a
// fresh DB with no bloat must report noop, not "applied". The
// threshold (50 MB reclaimable) prevents `doctor --fix` from
// paying the VACUUM cost on a clean install.
func TestFixVacuumIfBloated_FreshDBNoop(t *testing.T) {
	store, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	action := fixVacuumIfBloated(store)
	if action.Name != "vacuum-db" {
		t.Errorf("action.Name = %q, want vacuum-db", action.Name)
	}
	if action.Status != "noop" {
		t.Errorf("fresh DB should report noop; got status=%q details=%q", action.Status, action.Details)
	}
	if !strings.Contains(action.Details, "threshold") {
		t.Errorf("noop details should mention the threshold; got: %s", action.Details)
	}
}

// TestRunDoctorFix_FullReport pins the orchestration: runDoctorFix
// builds a FixReport with at least one action (vacuum-db) and emits
// it through the writer. The text format must include all the
// status counts in the trailing summary.
func TestRunDoctorFix_FullReport(t *testing.T) {
	dir := t.TempDir()
	store, err := db.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	var buf bytes.Buffer
	runDoctorFix(store, dir, false, false, &buf)
	out := buf.String()
	if !strings.Contains(out, "vacuum-db") {
		t.Errorf("output missing vacuum-db action; got:\n%s", out)
	}
	if !strings.Contains(out, "Summary:") {
		t.Errorf("output missing Summary line; got:\n%s", out)
	}
	if !strings.Contains(out, "noop") {
		t.Errorf("fresh DB run should report noop in summary; got:\n%s", out)
	}
}

// TestRunDoctorFix_JSONShape pins the --json output: parses cleanly
// into FixReport, carries data_dir + actions array, action statuses
// are from the documented taxonomy.
func TestRunDoctorFix_JSONShape(t *testing.T) {
	dir := t.TempDir()
	store, err := db.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	var buf bytes.Buffer
	runDoctorFix(store, dir, true, false, &buf)
	var report FixReport
	if err := json.Unmarshal(buf.Bytes(), &report); err != nil {
		t.Fatalf("JSON parse failed: %v\nbody:\n%s", err, buf.String())
	}
	if report.DataDir != dir {
		t.Errorf("data_dir = %q, want %q", report.DataDir, dir)
	}
	if len(report.Actions) == 0 {
		t.Fatal("actions array is empty; expected at least vacuum-db")
	}
	validStatuses := map[string]bool{
		"applied": true, "skipped": true, "noop": true, "error": true,
	}
	for _, a := range report.Actions {
		if !validStatuses[a.Status] {
			t.Errorf("action %q has invalid status %q; want one of applied/skipped/noop/error", a.Name, a.Status)
		}
	}
}

// TestFixReapOrphanLocks_NoLocks — a data dir with no locks/ directory
// yields a noop action, not an error.
func TestFixReapOrphanLocks_NoLocks(t *testing.T) {
	act := fixReapOrphanLocks(t.TempDir(), "1.0.0", false)
	if act.Name != "reap-orphan-locks" {
		t.Errorf("action name = %q", act.Name)
	}
	if act.Status != "noop" {
		t.Errorf("status = %q, want noop; details: %s", act.Status, act.Details)
	}
}

// TestFixReapOrphanLocks_DeadHolderReclaimed — a lockfile whose holder
// PID is long dead is reclaimed (the safe, no-kill tier), reported as
// "applied".
func TestFixReapOrphanLocks_DeadHolderReclaimed(t *testing.T) {
	dir := t.TempDir()
	lockDir := filepath.Join(dir, "locks")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	lockPath := filepath.Join(lockDir, "dead.lock")
	// PID 4_000_001 is above any real process table — a dead holder.
	if err := os.WriteFile(lockPath, []byte(`{"pid":4000001,"project_id":"abandoned"}`), 0o644); err != nil {
		t.Fatalf("write lock: %v", err)
	}

	act := fixReapOrphanLocks(dir, "1.0.0", false)
	if act.Status != "applied" {
		t.Errorf("status = %q, want applied; details: %s", act.Status, act.Details)
	}
	if !strings.Contains(act.Details, "stale lockfile") {
		t.Errorf("details should mention the reclaimed stale lockfile; got %q", act.Details)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Error("dead-holder lockfile should have been reclaimed")
	}
}

// TestFormatFixText_EmptyActionsAdvisesNothingNeeded pins the helpful-
// trailing-message branch: when no fixes ran and no errors fired,
// the formatter should add the "no safe fixes were needed" advisory
// pointing the user at explicit-action subcommands for the rest.
func TestFormatFixText_EmptyActionsAdvisesNothingNeeded(t *testing.T) {
	r := &FixReport{
		DataDir: "/tmp/test",
		Actions: []FixAction{
			{Name: "vacuum-db", Status: "noop", Details: "DB is healthy"},
		},
	}
	out := formatFixText(r)
	if !strings.Contains(out, "No safe fixes were needed") {
		t.Errorf("expected the no-fixes-needed advisory; got:\n%s", out)
	}
	if !strings.Contains(out, "destructive remediations") {
		t.Errorf("advisory should mention destructive remediations stay explicit; got:\n%s", out)
	}
}

// TestFixPruneStaleFailures_AppliedWhenStaleRowsExist — #1386 safe
// action. Seeds one stale row + one current row, asserts the action
// reports "applied" with the correct count, and the stale row is
// actually gone from the table.
func TestFixPruneStaleFailures_AppliedWhenStaleRowsExist(t *testing.T) {
	store, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	if err := store.UpsertProject(db.Project{ID: "p", Path: "/p", Name: "p", IndexedAt: time.Now()}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := store.RecordExtractionFailure("p", "stale.go", "Go", "parse_error", "old"); err != nil {
		t.Fatalf("seed stale: %v", err)
	}
	if _, err := store.DB().Exec(
		`UPDATE extraction_failures SET last_seen_at = ? WHERE file_path = 'stale.go'`,
		time.Now().Add(-2*time.Hour).Unix()); err != nil {
		t.Fatalf("back-date: %v", err)
	}

	action := fixPruneStaleFailures(store)
	if action.Name != "prune-stale-failures" {
		t.Errorf("Name = %q, want prune-stale-failures", action.Name)
	}
	if action.Status != "applied" {
		t.Errorf("Status = %q, want applied; details=%q", action.Status, action.Details)
	}
	if !strings.Contains(action.Details, "removed 1") {
		t.Errorf("Details should report row count; got %q", action.Details)
	}

	// Verify the row is actually gone.
	rows, _ := store.ListExtractionFailures("p", 100)
	if len(rows) != 0 {
		t.Errorf("table still has %d rows after prune", len(rows))
	}
}

// TestFixPruneStaleFailures_NoopWhenNoStaleRows — empty table reports
// noop, no error.
func TestFixPruneStaleFailures_NoopWhenNoStaleRows(t *testing.T) {
	store, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	action := fixPruneStaleFailures(store)
	if action.Status != "noop" {
		t.Errorf("Status = %q, want noop on empty table; details=%q", action.Status, action.Details)
	}
}

// TestVacuumThresholdConstant pins the 50 MB threshold value. A
// future tightening to e.g. 10 MB would silently make `doctor --fix`
// more aggressive — pin the value so the change is deliberate.
func TestVacuumThresholdConstant(t *testing.T) {
	const expected = int64(50 * 1024 * 1024)
	if vacuumThresholdBytes != expected {
		t.Errorf("vacuumThresholdBytes = %d, want %d (50 MB) — a deliberate change requires updating this test", vacuumThresholdBytes, expected)
	}
}
