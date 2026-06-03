// SPDX-License-Identifier: MIT

package index

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/zeebo/xxh3"
)

// Cross-process project lock.
//
// The in-process per-project mutex on Indexer.active prevents one Indexer
// from running two Index() calls for the same project concurrently. It does
// nothing across processes — when several pincher binaries share the same
// data dir (multiple Claude Code sessions, MCP server + manual CLI, etc.)
// they could otherwise both pile heavy index transactions onto the same
// SQLite file. WAL keeps it correct but the contention can cascade and the
// duplicated work is wasted.
//
// We solve this with a per-project lockfile in <dataDir>/locks/<hash>.lock,
// created with O_EXCL. The lockfile carries the holder's PID and start
// time so a crashed-and-not-cleaned-up holder can be identified as stale.

type lockInfo struct {
	PID       int    `json:"pid"`
	StartTime int64  `json:"start_time_unix"`
	ProjectID string `json:"project_id"`
	// BinaryVersion (#1312 v0.71): the holder's `--version` string at
	// lock-acquisition. Surfaced in contention error messages so the
	// caller can distinguish "another live concurrent indexer of the
	// same version" from "an orphan from a prior binary blocking the
	// fresh child after `make install`". Empty on legacy lockfiles
	// written by pre-#1312 binaries — the error message degrades to
	// the pre-fix shape.
	BinaryVersion string `json:"binary_version,omitempty"`
}

// lockStaleAge: lockfiles older than this are treated as abandoned. Real
// indexes never run this long; cap is generous to avoid false positives.
const lockStaleAge = 24 * time.Hour

// acquireProjectLock creates an exclusive cross-process lockfile for the
// given projectID under dataDir/locks/. Returns a release function the
// caller must defer-call. Returns an error if another live process holds
// the lock. binaryVersion is the caller's `--version` string; empty
// string is accepted (callers without SetBinaryVersion plumbed) and
// just yields a legacy-shaped lockfile.
func acquireProjectLock(dataDir, projectID, binaryVersion string) (func(), error) {
	lockPath := projectLockPath(dataDir, projectID)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, fmt.Errorf("create lock dir: %w", err)
	}

	payload, err := json.Marshal(lockInfo{
		PID:           os.Getpid(),
		StartTime:     time.Now().Unix(),
		ProjectID:     projectID,
		BinaryVersion: binaryVersion,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal lock info: %w", err)
	}

	// Try to create exclusively. On EEXIST, check staleness and retry once.
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			if _, werr := f.Write(payload); werr != nil {
				f.Close()
				_ = os.Remove(lockPath)
				return nil, fmt.Errorf("write lockfile: %w", werr)
			}
			f.Close()
			return func() { _ = os.Remove(lockPath) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("create lockfile: %w", err)
		}
		if attempt == 0 && isStaleLockfile(lockPath) {
			_ = os.Remove(lockPath)
			continue
		}
		holder, _ := readLockInfo(lockPath)
		return nil, fmt.Errorf(
			"project %q already being indexed: %s",
			projectID, describeLockHolder(holder, binaryVersion),
		)
	}
	return nil, fmt.Errorf("acquire lock: exhausted retries")
}

// describeLockHolder formats a human-readable description of who is
// holding the project lock. When binary_version is available on both
// sides and they differ, a hint flags the version skew so callers can
// distinguish an orphan from an earlier session vs a legitimate same-
// version concurrent indexer. #1312 v0.71.
func describeLockHolder(holder lockInfo, callerBinaryVersion string) string {
	base := fmt.Sprintf("pincher PID %d (since %s)",
		holder.PID, time.Unix(holder.StartTime, 0).Format(time.RFC3339))
	if holder.BinaryVersion == "" {
		// Pre-#1312 binary wrote the lockfile, OR caller didn't plumb
		// SetBinaryVersion. No skew info available.
		return base
	}
	base = fmt.Sprintf("%s, binary_version=%s", base, holder.BinaryVersion)
	if callerBinaryVersion != "" && holder.BinaryVersion != callerBinaryVersion {
		base = fmt.Sprintf("%s — version skew vs caller (caller=%s); the holder may be an orphan from a prior binary, consider `kill %d` after confirming",
			base, callerBinaryVersion, holder.PID)
	}
	return base
}

// projectLockPath maps a projectID to a fixed-length lockfile path.
// Hashing keeps the filename safe regardless of slashes/colons in the
// projectID and bounds the name length on every filesystem.
func projectLockPath(dataDir, projectID string) string {
	h := xxh3.HashString(projectID)
	return filepath.Join(dataDir, "locks", fmt.Sprintf("%016x.lock", h))
}

func readLockInfo(path string) (lockInfo, error) {
	var info lockInfo
	data, err := os.ReadFile(path)
	if err != nil {
		return info, err
	}
	err = json.Unmarshal(data, &info)
	return info, err
}

// isStaleLockfile reports whether a lockfile can be removed safely:
// modification time older than lockStaleAge, holder PID gone, or the
// payload is unparseable (corrupt → reclaim).
func isStaleLockfile(path string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	if time.Since(fi.ModTime()) > lockStaleAge {
		return true
	}
	info, err := readLockInfo(path)
	if err != nil {
		return true
	}
	return !processExists(info.PID)
}

// processExists reports whether a process with the given PID is currently
// running. Cross-platform: on Unix, send signal 0; on Windows, rely on
// os.FindProcess returning an error for missing PIDs.
func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	if err := proc.Signal(syscall.Signal(0)); err == nil {
		return true
	} else if errors.Is(err, syscall.EPERM) {
		// Process exists but we can't signal it (different user).
		return true
	}
	return false
}
