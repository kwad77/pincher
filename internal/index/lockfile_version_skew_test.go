// SPDX-License-Identifier: MIT

package index

import (
	"strings"
	"testing"
)

// #1312 v0.71: when a lock-contention happens between a newer-binary
// MCP child and an older-binary orphan watcher (the user's repro:
// fresh v0.68 child blocked by leftover v0.58 watchers after
// `make install`), the error message must surface the version skew so
// the operator can identify the orphan and clean it up. Pre-fix the
// error just named a PID; "is that PID a legitimate concurrent indexer
// or an orphan I should kill?" was unanswerable without out-of-band
// process inspection.

func TestAcquireProjectLock_VersionSkewSurfaced_1312(t *testing.T) {
	dir := t.TempDir()

	// First acquire records binary_version "v0.58.0".
	release1, err := acquireProjectLock(dir, "proj", "v0.58.0")
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer release1()

	// Second acquire identifies as "v0.71.0" — must fail AND the error
	// must flag the version skew + name the holder's version + suggest
	// the kill remediation.
	_, err = acquireProjectLock(dir, "proj", "v0.71.0")
	if err == nil {
		t.Fatal("second acquire should have failed (live holder)")
	}
	msg := err.Error()
	for _, want := range []string{"v0.58.0", "v0.71.0", "version skew", "consider `kill"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message missing %q\n  got: %s", want, msg)
		}
	}
}

// Negative control: when caller and holder are the SAME binary_version,
// the error message records both versions but does NOT include the
// "version skew" hint — they're legitimate same-version contenders, not
// an orphan-vs-fresh-binary mismatch.
func TestAcquireProjectLock_SameVersionNoSkewHint_1312(t *testing.T) {
	dir := t.TempDir()

	release1, err := acquireProjectLock(dir, "proj", "v0.71.0")
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer release1()

	_, err = acquireProjectLock(dir, "proj", "v0.71.0")
	if err == nil {
		t.Fatal("second acquire should have failed")
	}
	msg := err.Error()
	if !strings.Contains(msg, "binary_version=v0.71.0") {
		t.Errorf("error should still record holder's binary_version; got: %s", msg)
	}
	if strings.Contains(msg, "version skew") {
		t.Errorf("error must NOT include skew hint when versions match; got: %s", msg)
	}
}

// Cross-check: legacy lockfile (no binary_version recorded — pre-#1312
// holder, or caller without SetBinaryVersion plumbed) degrades to the
// pre-fix message shape without the version field. No crash, no nil-
// dereference.
func TestAcquireProjectLock_LegacyHolder_NoBinaryVersionField_1312(t *testing.T) {
	dir := t.TempDir()

	// Holder records no version (empty string).
	release1, err := acquireProjectLock(dir, "proj", "")
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer release1()

	_, err = acquireProjectLock(dir, "proj", "v0.71.0")
	if err == nil {
		t.Fatal("second acquire should have failed")
	}
	msg := err.Error()
	if strings.Contains(msg, "binary_version=") {
		t.Errorf("legacy holder (empty version) must NOT include a binary_version= clause; got: %s", msg)
	}
	if strings.Contains(msg, "version skew") {
		t.Errorf("legacy holder must NOT trigger skew hint; got: %s", msg)
	}
	// Pre-fix shape still present.
	if !strings.Contains(msg, "already being indexed") {
		t.Errorf("base 'already being indexed' phrase missing; got: %s", msg)
	}
}
