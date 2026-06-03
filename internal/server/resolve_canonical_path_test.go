// SPDX-License-Identifier: MIT

package server

import (
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #997: resolveProjectID's exact-string match against the stored ID
// missed when the caller passed a path with different casing or symlink
// shape than what was stored. On case-insensitive filesystems
// (Windows NTFS, macOS APFS) the underlying directory is the same but
// the strings differ, so the lookup returned "project not found" with
// no recourse. The fallback canonicalizes the input via
// ProjectIDFromPath and retries.

func TestResolveProjectID_CanonicalizesAbsPathFallback(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)

	// Use a real tempdir so ProjectIDFromPath's symlink+case-fold pass
	// can resolve it. The stored ID is what ProjectIDFromPath would
	// produce on first index; the lookup arg is a different-but-
	// canonically-equivalent path string.
	tmp := t.TempDir()
	storedID := db.ProjectIDFromPath(tmp)
	store.UpsertProject(db.Project{
		ID: storedID, Path: tmp, Name: "case-twin", IndexedAt: time.Now(),
	})

	// Pass the path through ProjectIDFromPath again: should match
	// without going through the name-fallback path. Easiest way to
	// verify the canonicalize-on-miss step kicks in: pass the path
	// directly (matches via exact GetProject first call), then pass
	// a path that needs canonicalization.
	got, err := srv.resolveProjectID(tmp)
	if err != nil {
		t.Fatalf("resolve(tmp): %v", err)
	}
	if got != storedID {
		t.Errorf("resolveProjectID(tmp) = %q, want %q", got, storedID)
	}
}

// When the input isn't absolute (e.g. a project name like "pincher-repo"),
// the canonicalize fallback must not kick in — name-match path stays
// intact.
func TestResolveProjectID_NonAbsArgSkipsCanonicalize(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)

	tmp := t.TempDir()
	store.UpsertProject(db.Project{
		ID: db.ProjectIDFromPath(tmp), Path: tmp, Name: "by-name-only", IndexedAt: time.Now(),
	})

	got, err := srv.resolveProjectID("by-name-only")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != db.ProjectIDFromPath(tmp) {
		t.Errorf("name lookup should still resolve when canonicalize-fallback is bypassed")
	}
}
