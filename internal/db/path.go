// SPDX-License-Identifier: MIT

package db

import (
	"os"
	"path/filepath"
	"strings"
)

// CanonicalProjectPath returns a canonical form of `absPath` suitable for
// use as a `project_id`. Two invocations with the same physical
// directory MUST return the same string, even if the caller passed
// different casings or paths through symlinks.
//
// Closes #84. Pre-fix, `pincher index ~/Projects/foo` and
// `pincher index ~/projects/foo` produced two distinct project rows on
// case-insensitive filesystems (macOS APFS default, Windows NTFS
// default), each accumulating its own subset of symbols.
//
// The canonical form:
//  1. Resolve symlinks via filepath.EvalSymlinks. A path that's a
//     symlink to /some/canonical/place becomes /some/canonical/place.
//  2. If the underlying filesystem is case-insensitive at this path,
//     lowercase the result. We probe the FS rather than relying on
//     OS-level heuristics because (a) macOS allows case-sensitive APFS
//     volumes alongside the default case-insensitive ones, and (b)
//     Linux can mount case-insensitive filesystems via ciopfs / SMB.
//
// On any error (path doesn't exist, EvalSymlinks fails, etc.) the
// function falls back to the cleaned absolute path. This preserves the
// pre-fix behavior for paths that don't exist yet — the caller sees
// the literal path; if they later create it and re-canonicalize, the
// canonical form may differ. That's acceptable: the migration
// (dedupProjectsByCanonicalPath) cleans up the stable case.
func CanonicalProjectPath(absPath string) string {
	clean := filepath.Clean(absPath)
	if !filepath.IsAbs(clean) {
		// Should never happen — callers always pass absolute paths via
		// filepath.Abs — but be defensive.
		if abs, err := filepath.Abs(clean); err == nil {
			clean = abs
		}
	}

	resolved, err := filepath.EvalSymlinks(clean)
	if err != nil {
		// Path doesn't exist or symlink chain is broken. Fall back to
		// the cleaned absolute path; lose canonicalisation.
		resolved = clean
	}

	if isCaseInsensitiveFS(resolved) {
		resolved = strings.ToLower(resolved)
	}
	return resolved
}

// isCaseInsensitiveFS probes the filesystem at `path` to determine if
// it's case-insensitive. The probe: stat the path; stat the path with
// one letter's case flipped; if both succeed and refer to the same
// inode, the FS is case-insensitive.
//
// Returns false on any error or for paths with no flippable letters.
// False is the safe default (treat as case-sensitive — paths that
// differ only in casing are distinct projects).
func isCaseInsensitiveFS(path string) bool {
	flipped, ok := flipFirstLetterCase(path)
	if !ok {
		return false
	}
	a, errA := os.Stat(path)
	b, errB := os.Stat(flipped)
	if errA != nil || errB != nil {
		return false
	}
	return os.SameFile(a, b)
}

// flipFirstLetterCase flips the case of the first ASCII letter in `s`
// and returns (flipped, true). If no flippable letter exists, returns
// (s, false). Used for the case-insensitivity probe — we just need a
// path that differs from the original in casing.
//
// Why ASCII-only: case folding for non-ASCII (e.g. Turkish dotted-I,
// German ß) is locale-dependent, and we just need ANY letter that
// changes case. ASCII letters cover the realistic project-path universe.
func flipFirstLetterCase(s string) (string, bool) {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			return s[:i] + string(c+32) + s[i+1:], true
		}
		if c >= 'a' && c <= 'z' {
			return s[:i] + string(c-32) + s[i+1:], true
		}
	}
	return s, false
}
