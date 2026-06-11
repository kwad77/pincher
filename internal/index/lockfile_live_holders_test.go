// SPDX-License-Identifier: MIT

package index

import (
	"os"
	"testing"
)

// LiveLockHolders (#1975) backs `pincher health-check`'s "DB locked by
// PID X" attribution.

func TestLiveLockHolders_EmptyWhenNoLockDir(t *testing.T) {
	holders, err := LiveLockHolders(t.TempDir())
	if err != nil {
		t.Fatalf("LiveLockHolders: %v", err)
	}
	if len(holders) != 0 {
		t.Errorf("holders = %+v, want none", holders)
	}
}

func TestLiveLockHolders_ReportsLiveAndSkipsDead(t *testing.T) {
	dir := t.TempDir()
	release, err := acquireProjectLock(dir, "projA", "1.2.3")
	if err != nil {
		t.Fatalf("acquire live lock: %v", err)
	}
	defer release()
	// A second lockfile whose holder is gone must be filtered out.
	dead, err := acquireProjectLock(dir, "projB", "1.2.3")
	if err != nil {
		t.Fatalf("acquire second lock: %v", err)
	}
	_ = dead // rewrite its payload with a dead PID
	path := projectLockPath(dir, "projB")
	if err := os.WriteFile(path, []byte(`{"pid":1073741824,"start_time_unix":1,"project_id":"projB"}`), 0o644); err != nil {
		t.Fatalf("rewrite lockfile: %v", err)
	}

	holders, err := LiveLockHolders(dir)
	if err != nil {
		t.Fatalf("LiveLockHolders: %v", err)
	}
	if len(holders) != 1 {
		t.Fatalf("holders = %+v, want exactly the live one", holders)
	}
	h := holders[0]
	if h.PID != os.Getpid() || h.ProjectID != "projA" || h.BinaryVersion != "1.2.3" {
		t.Errorf("holder = %+v, want PID %d / projA / 1.2.3", h, os.Getpid())
	}
}
