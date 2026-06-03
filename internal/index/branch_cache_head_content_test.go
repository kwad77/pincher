// SPDX-License-Identifier: MIT

package index

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// TestDetectGitBranchCached_ForceSkipsSubprocessOnUnchangedHEAD pins the
// #1474 perf follow-up: force=true now reuses the cached branch when
// `.git/HEAD` is byte-for-byte unchanged since the cached entry was
// written. Verified by inspecting the cache state after rapid-fire
// force calls — entry's cachedAt does NOT advance on the second call
// because the subprocess path didn't run.
func TestDetectGitBranchCached_ForceSkipsSubprocessOnUnchangedHEAD(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=t@t.t",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=t@t.t",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x\n"), 0o644)
	runGit("add", ".")
	runGit("commit", "-q", "-m", "i")

	dbDir := t.TempDir()
	store, _ := db.Open(dbDir)
	defer store.Close()
	idx := New(store)

	projectID := db.ProjectIDFromPath(dir)

	// First force call populates cache.
	got1 := idx.detectGitBranchCached(projectID, dir, true)
	if got1 != "main" {
		t.Fatalf("first force call: got %q, want main", got1)
	}
	v1, ok := idx.branchCacheByProject.Load(projectID)
	if !ok {
		t.Fatal("cache not populated after first call")
	}
	e1 := v1.(branchCacheEntry)
	if e1.headContent == "" {
		t.Fatal("cache entry has empty headContent — HEAD file not read")
	}
	cachedAt1 := e1.cachedAt

	// Second force call — HEAD unchanged, must reuse cache entry.
	// cachedAt must NOT advance: if it did, the subprocess path ran.
	got2 := idx.detectGitBranchCached(projectID, dir, true)
	if got2 != "main" {
		t.Fatalf("second force call: got %q, want main", got2)
	}
	v2, _ := idx.branchCacheByProject.Load(projectID)
	e2 := v2.(branchCacheEntry)
	if !e2.cachedAt.Equal(cachedAt1) {
		t.Errorf("second force call refreshed cachedAt — subprocess re-ran. before=%v after=%v", cachedAt1, e2.cachedAt)
	}
}

// TestDetectGitBranchCached_ForceReDetectsOnHEADChange is the correctness
// cross-check for the perf optimisation: when `.git/HEAD` content
// actually changes (real `git checkout`), force=true must re-detect the
// branch. Without this, the HEAD-content shortcut could mask a stale
// branch stamp.
func TestDetectGitBranchCached_ForceReDetectsOnHEADChange(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=t@t.t",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=t@t.t",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x\n"), 0o644)
	runGit("add", ".")
	runGit("commit", "-q", "-m", "i")

	dbDir := t.TempDir()
	store, _ := db.Open(dbDir)
	defer store.Close()
	idx := New(store)

	projectID := db.ProjectIDFromPath(dir)

	if got := idx.detectGitBranchCached(projectID, dir, true); got != "main" {
		t.Fatalf("initial: got %q, want main", got)
	}
	runGit("checkout", "-q", "-b", "feat/x")
	// Force=true after real branch switch: HEAD content changed, must
	// re-detect.
	if got := idx.detectGitBranchCached(projectID, dir, true); got != "feat/x" {
		t.Errorf("after checkout: got %q, want feat/x — HEAD-content check failed to invalidate", got)
	}
}

// TestDetectGitBranchCached_NonGitDirReadsBlank is the fail-open
// regression: a directory that isn't a git working tree must not
// produce an error from the HEAD-content read. Empty content counts as
// a cache miss so the subprocess path stays in effect (the existing
// detectGitBranch returns "" for non-git dirs).
func TestDetectGitBranchCached_NonGitDirReadsBlank(t *testing.T) {
	dir := t.TempDir() // no `git init`
	dbDir := t.TempDir()
	store, _ := db.Open(dbDir)
	defer store.Close()
	idx := New(store)

	projectID := db.ProjectIDFromPath(dir)
	got := idx.detectGitBranchCached(projectID, dir, true)
	if got != "" {
		t.Errorf("non-git dir: got %q, want empty", got)
	}
	if v, ok := idx.branchCacheByProject.Load(projectID); ok {
		e := v.(branchCacheEntry)
		if e.headContent != "" {
			t.Errorf("non-git dir cache headContent = %q, want empty", e.headContent)
		}
	}
}

// TestReadGitHEAD_TrimsTrailingNewline confirms the small "stable across
// trailing-newline variants" guarantee in the readGitHEAD doc.
func TestReadGitHEAD_TrimsTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Write with explicit trailing newline; readGitHEAD must strip it.
	if err := os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := readGitHEAD(dir)
	if got != "ref: refs/heads/main" {
		t.Errorf("readGitHEAD = %q, want %q", got, "ref: refs/heads/main")
	}
}
