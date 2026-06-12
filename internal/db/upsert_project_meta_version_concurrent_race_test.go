// SPDX-License-Identifier: MIT

package db

import (
	"sync"
	"testing"
	"time"
)

// Concurrent regression for the binary_version downgrade-guard TOCTOU.
//
// The #1154 / #1818 guard reads binary_version, compares it in Go, then
// writes the clamped value back. Pre-fix that read-modify-write was split
// across two pools with no transaction and no mutex: the SELECT ran on the
// reader pool (s.ro) and the conditional INSERT...ON CONFLICT ran on the
// writer pool (s.db). Two concurrent writers — one stamping a NEWER version,
// one re-stamping an OLDER version — could both observe the same stale
// existing value, so the older writer's "keep existing" decision was made
// against a pre-upgrade snapshot and it clobbered the newer write.
//
// The seed-older / concurrent (newer-upgrade + older-restamp) shape is the
// exact production race: an MCP server on a fresh release build upgrades the
// stamp while an orphaned watcher from the previous (older) build re-stamps
// on its indexing pass. The invariant under test: once a newer version has
// landed, no interleaving with an older re-stamp may downgrade it. After the
// dust settles the row must hold the newer version, every trial.
//
// Pre-fix this reproduces the downgrade (observed ~63/200 trials in the bug
// report). Post-fix (atomic read-modify-write on the writer connection) the
// newer version is preserved on every trial. Run with -race.

const (
	concurrentRaceOlder = "0.58.0-10-gdeb797d"
	concurrentRaceNewer = "0.58.0-44-g91e9c0f"
	concurrentRaceTrials = 200
)

func TestUpsertProjectMeta_ConcurrentStampNeverDowngrades(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	now := time.Now()

	for trial := 0; trial < concurrentRaceTrials; trial++ {
		pid := "proj-concurrent-meta"

		// Seed the row with the OLDER version so both racing writers
		// observe a real pre-existing value to compare against.
		if err := s.UpsertProjectMeta(Project{
			ID: pid, Path: "/tmp/" + pid, Name: pid,
			IndexedAt: now, BinaryVersion: concurrentRaceOlder,
		}); err != nil {
			t.Fatalf("trial %d seed: %v", trial, err)
		}

		var wg sync.WaitGroup
		var startGate sync.WaitGroup
		startGate.Add(1)
		wg.Add(2)

		// Writer A: the NEWER binary upgrading the stamp.
		go func() {
			defer wg.Done()
			startGate.Wait()
			if err := s.UpsertProjectMeta(Project{
				ID: pid, Path: "/tmp/" + pid, Name: pid,
				IndexedAt: now.Add(1 * time.Second), BinaryVersion: concurrentRaceNewer,
			}); err != nil {
				t.Errorf("trial %d newer stamp: %v", trial, err)
			}
		}()

		// Writer B: the OLDER orphan re-stamping its own version.
		go func() {
			defer wg.Done()
			startGate.Wait()
			if err := s.UpsertProjectMeta(Project{
				ID: pid, Path: "/tmp/" + pid, Name: pid,
				IndexedAt: now.Add(2 * time.Second), BinaryVersion: concurrentRaceOlder,
			}); err != nil {
				t.Errorf("trial %d older restamp: %v", trial, err)
			}
		}()

		startGate.Done() // release both writers as close to simultaneously as possible
		wg.Wait()

		got, err := s.GetProject(pid)
		if err != nil {
			t.Fatalf("trial %d GetProject: %v", trial, err)
		}
		// Regardless of interleaving, the newer version has landed at some
		// point and an older re-stamp must never displace it.
		if got.BinaryVersion != concurrentRaceNewer {
			t.Fatalf("trial %d: binary_version downgraded by concurrent older re-stamp: got %q, want %q",
				trial, got.BinaryVersion, concurrentRaceNewer)
		}

		// Reset for the next trial so each starts from the seeded-older state.
		if _, err := s.db.Exec(`DELETE FROM projects WHERE id = ?`, pid); err != nil {
			t.Fatalf("trial %d cleanup: %v", trial, err)
		}
	}
}

// Same TOCTOU shape exercised through UpsertProject (the #1818 path, which
// shares the identical read-on-reader / write-on-writer guard). UpsertProject
// also enforces a schema_version_at_index monotonic CASE, so both racing
// writers stamp the same currentSchema, isolating the binary_version guard.
func TestUpsertProject_ConcurrentStampNeverDowngrades(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	now := time.Now()

	for trial := 0; trial < concurrentRaceTrials; trial++ {
		pid := "proj-concurrent-full"

		if err := s.UpsertProject(Project{
			ID: pid, Path: "/tmp/" + pid, Name: pid,
			IndexedAt: now, BinaryVersion: concurrentRaceOlder,
		}); err != nil {
			t.Fatalf("trial %d seed: %v", trial, err)
		}

		var wg sync.WaitGroup
		var startGate sync.WaitGroup
		startGate.Add(1)
		wg.Add(2)

		go func() {
			defer wg.Done()
			startGate.Wait()
			if err := s.UpsertProject(Project{
				ID: pid, Path: "/tmp/" + pid, Name: pid,
				IndexedAt: now.Add(1 * time.Second), BinaryVersion: concurrentRaceNewer,
			}); err != nil {
				t.Errorf("trial %d newer stamp: %v", trial, err)
			}
		}()

		go func() {
			defer wg.Done()
			startGate.Wait()
			if err := s.UpsertProject(Project{
				ID: pid, Path: "/tmp/" + pid, Name: pid,
				IndexedAt: now.Add(2 * time.Second), BinaryVersion: concurrentRaceOlder,
			}); err != nil {
				t.Errorf("trial %d older restamp: %v", trial, err)
			}
		}()

		startGate.Done()
		wg.Wait()

		got, err := s.GetProject(pid)
		if err != nil {
			t.Fatalf("trial %d GetProject: %v", trial, err)
		}
		if got.BinaryVersion != concurrentRaceNewer {
			t.Fatalf("trial %d: binary_version downgraded by concurrent older re-stamp: got %q, want %q",
				trial, got.BinaryVersion, concurrentRaceNewer)
		}

		if _, err := s.db.Exec(`DELETE FROM projects WHERE id = ?`, pid); err != nil {
			t.Fatalf("trial %d cleanup: %v", trial, err)
		}
	}
}
