// SPDX-License-Identifier: MIT

package index

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// orphan.go — reap orphaned project locks.
//
// A pincher process can outlive its purpose: a detached `--http` server
// from a closed session, a dev build superseded by `make install`, an
// MCP child whose client disconnected. It stays alive holding a project
// lockfile, and the next pincher hits "project already being indexed".
// Today the user reads the doctor advisory, finds the PID, and kills it
// by hand. ReapOrphanLocks does that triage deterministically.
//
// Two tiers, by risk:
//   - Stale-lockfile reclamation (holder dead, or its PID recycled by
//     an unrelated process) — pure file removal, no process touched.
//     Always performed; it's the same reclamation acquireProjectLock
//     does lazily, just done eagerly.
//   - Live-orphan termination — killing a running pincher. Gated behind
//     FOUR conditions (holds a pincher lockfile · alive · executable
//     verifiably a pincher binary · binary_version differs from the
//     caller's) AND an explicit killOrphans opt-in. A same-version peer
//     is a legitimate concurrent indexer and is never touched.

// processExecutablePath resolves a PID to its executable, and
// killProcess terminates a PID. Both are package vars so orphan tests
// can substitute deterministic fakes — a test must never depend on real
// process identity or actually kill anything.
var (
	processExecutablePath = platformProcessExecutablePath
	killProcess           = func(pid int) {
		if p, err := os.FindProcess(pid); err == nil {
			_ = p.Kill()
		}
	}
)

// OrphanKill records one live version-skewed orphan pincher process.
type OrphanKill struct {
	PID           int    `json:"pid"`
	ProjectID     string `json:"project_id"`
	BinaryVersion string `json:"binary_version"`
	LockPath      string `json:"lock_path"`
}

// ReapResult is the outcome of one ReapOrphanLocks pass.
type ReapResult struct {
	Scanned      int          `json:"scanned"`
	StaleRemoved []string     `json:"stale_removed"` // lockfiles of dead / PID-reused holders — removed
	OrphansFound []OrphanKill `json:"orphans_found"` // live version-skewed pincher orphans
	Killed       bool         `json:"killed"`        // whether OrphansFound were terminated
	SkippedLive  []string     `json:"skipped_live"`  // legitimate same-version peers, left alone
	Unverifiable []string     `json:"unverifiable"`  // live PID, executable identity unknown — left alone
}

// ReapOrphanLocks scans dataDir/locks and clears orphaned project locks.
// currentVersion is the reaping binary's version string. When
// killOrphans is true, live version-skewed orphan pincher processes are
// terminated and their lockfiles removed; when false they are only
// reported in OrphansFound. Stale-lockfile reclamation happens
// regardless — it touches no processes.
func ReapOrphanLocks(dataDir, currentVersion string, killOrphans bool) (ReapResult, error) {
	res := ReapResult{
		StaleRemoved: []string{},
		OrphansFound: []OrphanKill{},
		Killed:       killOrphans,
		SkippedLive:  []string{},
		Unverifiable: []string{},
	}
	lockDir := filepath.Join(dataDir, "locks")
	entries, err := os.ReadDir(lockDir)
	if err != nil {
		if os.IsNotExist(err) {
			return res, nil // no locks dir → nothing to reap
		}
		return res, fmt.Errorf("read lock dir: %w", err)
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".lock") {
			continue
		}
		res.Scanned++
		lockPath := filepath.Join(lockDir, e.Name())

		info, err := readLockInfo(lockPath)
		if err != nil {
			// Corrupt payload — treat as stale and reclaim.
			_ = os.Remove(lockPath)
			res.StaleRemoved = append(res.StaleRemoved, lockPath)
			continue
		}

		// Tier 1: holder process gone → reclaim the lockfile.
		if !processExists(info.PID) {
			_ = os.Remove(lockPath)
			res.StaleRemoved = append(res.StaleRemoved, lockPath)
			continue
		}

		// Holder PID is alive — verify it is still a pincher process.
		exe, exeErr := processExecutablePath(info.PID)
		if exeErr != nil {
			// Can't verify identity — be conservative, never act blind.
			res.Unverifiable = append(res.Unverifiable, lockPath)
			continue
		}
		if !looksLikePincher(exe) {
			// PID recycled by an unrelated process — the real pincher
			// holder is gone. Reclaim the lockfile; never touch the
			// unrelated process.
			_ = os.Remove(lockPath)
			res.StaleRemoved = append(res.StaleRemoved, lockPath)
			continue
		}

		// A live pincher process. Orphan only if its build differs from
		// ours — a same-version peer is a legitimate concurrent indexer.
		// Missing version on either side → cannot judge → skip.
		skew := info.BinaryVersion != "" && currentVersion != "" && info.BinaryVersion != currentVersion
		if !skew {
			res.SkippedLive = append(res.SkippedLive, lockPath)
			continue
		}

		// Tier 2: a confirmed live version-skewed orphan.
		res.OrphansFound = append(res.OrphansFound, OrphanKill{
			PID:           info.PID,
			ProjectID:     info.ProjectID,
			BinaryVersion: info.BinaryVersion,
			LockPath:      lockPath,
		})
		if killOrphans {
			killProcess(info.PID)
			_ = os.Remove(lockPath)
		}
	}
	return res, nil
}

// looksLikePincher reports whether an executable path is a pincher
// binary. Matches the basename so dev builds (`pincher-dash-test.exe`,
// `pincher.exe`) and the installed binary all qualify.
func looksLikePincher(exePath string) bool {
	return strings.Contains(strings.ToLower(filepath.Base(exePath)), "pincher")
}
