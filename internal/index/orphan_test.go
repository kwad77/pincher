package index

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// writeTestLock drops a lockfile with the given holder info into
// dataDir/locks and returns its path.
func writeTestLock(t *testing.T, dataDir string, info lockInfo) string {
	t.Helper()
	dir := filepath.Join(dataDir, "locks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir locks: %v", err)
	}
	path := filepath.Join(dir, fmt.Sprintf("%d-%s.lock", info.PID, info.ProjectID))
	b, _ := json.Marshal(info)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("write lock: %v", err)
	}
	return path
}

// stubProcessIdentity swaps the injectable process-identity hooks for
// the duration of a test and restores them after.
func stubProcessIdentity(t *testing.T, exe func(int) (string, error), kill func(int)) {
	t.Helper()
	origExe, origKill := processExecutablePath, killProcess
	processExecutablePath = exe
	killProcess = kill
	t.Cleanup(func() { processExecutablePath, killProcess = origExe, origKill })
}

func TestLooksLikePincher(t *testing.T) {
	yes := []string{"/usr/local/bin/pincher", `C:\tools\pincher.exe`, "/tmp/pincher-dash-test.exe", "PINCHER"}
	no := []string{"/usr/bin/bash", `C:\Windows\explorer.exe`, "/tmp/index.test", ""}
	for _, p := range yes {
		if !looksLikePincher(p) {
			t.Errorf("looksLikePincher(%q) = false, want true", p)
		}
	}
	for _, p := range no {
		if looksLikePincher(p) {
			t.Errorf("looksLikePincher(%q) = true, want false", p)
		}
	}
}

func TestReapOrphanLocks_NoLocksDir(t *testing.T) {
	res, err := ReapOrphanLocks(t.TempDir(), "1.0.0", false)
	if err != nil {
		t.Fatalf("ReapOrphanLocks on a dir with no locks/: %v", err)
	}
	if res.Scanned != 0 {
		t.Errorf("scanned = %d, want 0", res.Scanned)
	}
}

func TestReapOrphanLocks_DeadHolder(t *testing.T) {
	dataDir := t.TempDir()
	// PID 4_000_001 is far above any real process table — processExists
	// reports it gone.
	lockPath := writeTestLock(t, dataDir, lockInfo{PID: 4_000_001, ProjectID: "abandoned"})

	res, err := ReapOrphanLocks(dataDir, "1.0.0", false)
	if err != nil {
		t.Fatalf("ReapOrphanLocks: %v", err)
	}
	if len(res.StaleRemoved) != 1 {
		t.Errorf("StaleRemoved = %v, want the dead-holder lockfile", res.StaleRemoved)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Error("dead-holder lockfile should have been removed")
	}
}

func TestReapOrphanLocks_CorruptLockfile(t *testing.T) {
	dataDir := t.TempDir()
	dir := filepath.Join(dataDir, "locks")
	os.MkdirAll(dir, 0o755)
	lockPath := filepath.Join(dir, "garbage.lock")
	os.WriteFile(lockPath, []byte("{not json"), 0o644)

	res, err := ReapOrphanLocks(dataDir, "1.0.0", false)
	if err != nil {
		t.Fatalf("ReapOrphanLocks: %v", err)
	}
	if len(res.StaleRemoved) != 1 {
		t.Errorf("a corrupt lockfile should be reclaimed as stale; got %+v", res)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Error("corrupt lockfile should have been removed")
	}
}

func TestReapOrphanLocks_PIDReuse(t *testing.T) {
	dataDir := t.TempDir()
	// Holder PID is alive (this test process) but its executable is not
	// a pincher binary — the original holder is gone, the PID recycled.
	writeTestLock(t, dataDir, lockInfo{PID: os.Getpid(), ProjectID: "reused", BinaryVersion: "0.9.0"})
	stubProcessIdentity(t,
		func(int) (string, error) { return "/usr/bin/bash", nil },
		func(int) { t.Fatal("killProcess must NOT be called for a recycled PID") },
	)

	res, err := ReapOrphanLocks(dataDir, "1.0.0", true)
	if err != nil {
		t.Fatalf("ReapOrphanLocks: %v", err)
	}
	if len(res.StaleRemoved) != 1 {
		t.Errorf("a PID recycled by a non-pincher process should be reclaimed as stale; got %+v", res)
	}
	if len(res.OrphansFound) != 0 {
		t.Errorf("a recycled PID is not an orphan pincher; got OrphansFound=%v", res.OrphansFound)
	}
}

func TestReapOrphanLocks_SameVersionPeerSkipped(t *testing.T) {
	dataDir := t.TempDir()
	writeTestLock(t, dataDir, lockInfo{PID: os.Getpid(), ProjectID: "peer", BinaryVersion: "1.0.0"})
	stubProcessIdentity(t,
		func(int) (string, error) { return "/usr/local/bin/pincher", nil },
		func(int) { t.Fatal("a same-version peer must never be killed") },
	)

	res, err := ReapOrphanLocks(dataDir, "1.0.0", true)
	if err != nil {
		t.Fatalf("ReapOrphanLocks: %v", err)
	}
	if len(res.SkippedLive) != 1 {
		t.Errorf("a live same-version pincher peer should be skipped; got %+v", res)
	}
	if len(res.OrphansFound) != 0 {
		t.Errorf("a same-version peer is not an orphan; got %v", res.OrphansFound)
	}
}

func TestReapOrphanLocks_OrphanReported_NotKilledWithoutOptIn(t *testing.T) {
	dataDir := t.TempDir()
	lockPath := writeTestLock(t, dataDir, lockInfo{PID: os.Getpid(), ProjectID: "orphan", BinaryVersion: "0.9.0-dev"})
	stubProcessIdentity(t,
		func(int) (string, error) { return "/tmp/pincher-dash-test.exe", nil },
		func(int) { t.Fatal("killProcess must NOT run when killOrphans is false") },
	)

	res, err := ReapOrphanLocks(dataDir, "1.0.0", false)
	if err != nil {
		t.Fatalf("ReapOrphanLocks: %v", err)
	}
	if len(res.OrphansFound) != 1 {
		t.Fatalf("expected the version-skewed orphan to be found; got %+v", res)
	}
	if res.Killed {
		t.Error("Killed must be false when killOrphans=false")
	}
	if _, err := os.Stat(lockPath); err != nil {
		t.Error("lockfile must survive when the orphan was only reported, not reaped")
	}
}

func TestReapOrphanLocks_OrphanKilledWithOptIn(t *testing.T) {
	dataDir := t.TempDir()
	lockPath := writeTestLock(t, dataDir, lockInfo{PID: os.Getpid(), ProjectID: "orphan", BinaryVersion: "0.9.0-dev"})
	var killed []int
	stubProcessIdentity(t,
		func(int) (string, error) { return "/tmp/pincher-dash-test.exe", nil },
		func(pid int) { killed = append(killed, pid) },
	)

	res, err := ReapOrphanLocks(dataDir, "1.0.0", true)
	if err != nil {
		t.Fatalf("ReapOrphanLocks: %v", err)
	}
	if len(res.OrphansFound) != 1 || !res.Killed {
		t.Fatalf("expected one killed orphan; got %+v", res)
	}
	if len(killed) != 1 || killed[0] != os.Getpid() {
		t.Errorf("killProcess called with %v, want [%d]", killed, os.Getpid())
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Error("a reaped orphan's lockfile should be removed")
	}
}

func TestReapOrphanLocks_UnverifiableLeftAlone(t *testing.T) {
	dataDir := t.TempDir()
	lockPath := writeTestLock(t, dataDir, lockInfo{PID: os.Getpid(), ProjectID: "mystery", BinaryVersion: "0.9.0"})
	stubProcessIdentity(t,
		func(int) (string, error) { return "", errors.New("access denied") },
		func(int) { t.Fatal("must never kill a process whose identity could not be verified") },
	)

	res, err := ReapOrphanLocks(dataDir, "1.0.0", true)
	if err != nil {
		t.Fatalf("ReapOrphanLocks: %v", err)
	}
	if len(res.Unverifiable) != 1 {
		t.Errorf("an unverifiable live holder should be left alone; got %+v", res)
	}
	if _, err := os.Stat(lockPath); err != nil {
		t.Error("an unverifiable holder's lockfile must survive")
	}
}
