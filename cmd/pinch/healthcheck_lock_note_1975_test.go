// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// #1975: when the health-check probe FAILs while another pincher
// process holds the writer, the diagnostic must NAME that writer (from
// the locks/ files) so "starved by an index" is distinguishable from
// "server broken".

func writeLockfile(t *testing.T, dataDir string, pid int, project, binVer string) {
	t.Helper()
	lockDir := filepath.Join(dataDir, "locks")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		t.Fatalf("mkdir locks: %v", err)
	}
	payload, err := json.Marshal(map[string]any{
		"pid":             pid,
		"start_time_unix": time.Now().Add(-time.Minute).Unix(),
		"project_id":      project,
		"binary_version":  binVer,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(lockDir, "deadbeefdeadbeef.lock"), payload, 0o644); err != nil {
		t.Fatalf("write lockfile: %v", err)
	}
}

func TestWriterContentionNote_NamesLiveHolder(t *testing.T) {
	dir := t.TempDir()
	writeLockfile(t, dir, os.Getpid(), "/home/u/bigrepo", "1.4.0")

	note := writerContentionNote(dir)
	if note == "" {
		t.Fatal("expected a contention note for a live lock holder, got empty")
	}
	for _, want := range []string{
		"DB locked by PID " + strconv.Itoa(os.Getpid()),
		"/home/u/bigrepo",
		"1.4.0",
		"starved",
	} {
		if !strings.Contains(note, want) {
			t.Errorf("note missing %q:\n%s", want, note)
		}
	}
}

func TestWriterContentionNote_EmptyWhenNoLocks(t *testing.T) {
	if note := writerContentionNote(t.TempDir()); note != "" {
		t.Errorf("expected empty note with no locks dir, got:\n%s", note)
	}
}

func TestWriterContentionNote_IgnoresDeadHolder(t *testing.T) {
	dir := t.TempDir()
	// PID 1<<30 is far above any real pid_max; processExists → false.
	writeLockfile(t, dir, 1<<30, "/home/u/bigrepo", "1.4.0")
	if note := writerContentionNote(dir); note != "" {
		t.Errorf("dead holder must not produce a note, got:\n%s", note)
	}
}
