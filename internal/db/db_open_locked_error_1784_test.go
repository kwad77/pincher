package db

import (
	"errors"
	"strings"
	"testing"
)

// #1784: db.Open's migrate() step takes a write lock; when another
// pincher process holds the writer past the busy_timeout the open
// fails with a raw SQLITE_BUSY. isDBLockedErr classifies that error so
// Open can substitute a friendly, actionable message.

func TestIsDBLockedErr_Classification_1784(t *testing.T) {
	t.Parallel()
	locked := []error{
		errors.New("migrate: init schema_version: database is locked (5) (SQLITE_BUSY)"),
		errors.New("database is locked (5)"),
		errors.New("SQLITE_BUSY"),
		errors.New("database table is locked"),
	}
	for _, e := range locked {
		if !isDBLockedErr(e) {
			t.Errorf("isDBLockedErr(%q) = false; want true", e)
		}
		if !IsLockedErr(e) {
			t.Errorf("IsLockedErr(%q) = false; want true", e)
		}
	}
	notLocked := []error{
		nil,
		errors.New("no such table: symbols"),
		errors.New("disk I/O error"),
		errors.New("migrate: init schema_version: out of memory (14)"),
	}
	for _, e := range notLocked {
		if isDBLockedErr(e) {
			t.Errorf("isDBLockedErr(%v) = true; want false", e)
		}
		if IsLockedErr(e) {
			t.Errorf("IsLockedErr(%v) = true; want false", e)
		}
	}
}

// The friendly message must lead with actionable guidance while the raw
// cause stays wrapped (errors.Unwrap-able) for debugging.
func TestOpen_LockedErrorMessage_IsActionable_1784(t *testing.T) {
	t.Parallel()
	raw := errors.New("init schema_version: database is locked (5) (SQLITE_BUSY)")
	// Mirror the exact wrap Open applies on the isDBLockedErr branch.
	wrapped := errorsLockedWrap(raw)
	msg := wrapped.Error()
	if !strings.HasPrefix(msg, "database is locked — another pincher process is writing it") {
		t.Errorf("locked-open message must lead with the friendly guidance; got %q", msg)
	}
	if !strings.Contains(msg, "retry in a few seconds") {
		t.Errorf("message must tell the user to retry; got %q", msg)
	}
	if !errors.Is(wrapped, raw) {
		t.Errorf("wrapped error must keep the raw cause inspectable via errors.Is")
	}
}

// A normal Open against a fresh data dir still succeeds — the new
// classification branch must not perturb the happy path.
func TestOpen_FreshDir_StillSucceeds_1784(t *testing.T) {
	t.Parallel()
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open on a fresh dir: %v", err)
	}
	store.Close()
}

// #1817: retryOnDBLock bounded-retries a SQLITE_BUSY-class failure so
// transient writer-lock contention (another pincher mid-index) doesn't
// fail db.Open outright. Backoff 0 keeps the test instant.
func TestRetryOnDBLock_1817(t *testing.T) {
	t.Parallel()
	busy := errors.New("init schema_version: database is locked (5) (SQLITE_BUSY)")

	// Retries past transient BUSY, then succeeds.
	calls := 0
	if err := retryOnDBLock(func() error {
		calls++
		if calls < 3 {
			return busy
		}
		return nil
	}, 3, 0); err != nil {
		t.Errorf("expected success after transient BUSY; got %v", err)
	}
	if calls != 3 {
		t.Errorf("expected 3 attempts; got %d", calls)
	}

	// Persistent BUSY: exhausts attempts, returns the lock error.
	calls = 0
	if err := retryOnDBLock(func() error { calls++; return busy }, 3, 0); !isDBLockedErr(err) {
		t.Errorf("persistent BUSY should return the lock error; got %v", err)
	}
	if calls != 3 {
		t.Errorf("persistent BUSY should use all 3 attempts; got %d", calls)
	}

	// A non-lock error returns immediately — a real migration bug must
	// not be masked by retrying.
	calls = 0
	nonLock := errors.New("no such table: symbols")
	if err := retryOnDBLock(func() error { calls++; return nonLock }, 3, 0); err != nonLock {
		t.Errorf("non-lock error should return as-is; got %v", err)
	}
	if calls != 1 {
		t.Errorf("non-lock error must not retry; got %d calls", calls)
	}

	// First-try success — exactly one call.
	calls = 0
	if err := retryOnDBLock(func() error { calls++; return nil }, 3, 0); err != nil || calls != 1 {
		t.Errorf("first-try success: err=%v calls=%d, want nil/1", err, calls)
	}
}
