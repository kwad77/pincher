// SPDX-License-Identifier: MIT

package index

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// #1303 Phase 2a: indexer stamps git branch on Symbol/Edge/Project
// rows at index time. Pinned end-to-end against a real git repo —
// pure-unit testing the cache wouldn't catch the wiring drift where
// branch detection runs but the value never reaches the Symbol rows.

// Positive: indexing a real git repo on branch `main` stamps `main`
// on every emitted symbol and on the project row's current_branch.
func TestIndex_StampsCurrentBranch(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package test\nfunc Hello() {}\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	runGit("add", ".")
	runGit("commit", "-q", "-m", "i")

	dbDir := t.TempDir()
	store, err := db.Open(dbDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()
	idx := New(store)
	if _, err := idx.Index(context.Background(), dir, true); err != nil {
		t.Fatalf("Index: %v", err)
	}

	projectID := db.ProjectIDFromPath(dir)
	p, err := store.GetProject(projectID)
	if err != nil || p == nil {
		t.Fatalf("GetProject: %v / %v", p, err)
	}
	if p.CurrentBranch != "main" {
		t.Errorf("Project.CurrentBranch = %q, want %q", p.CurrentBranch, "main")
	}

	syms, err := store.GetSymbolsByQN(projectID, "test.Hello")
	if err != nil {
		t.Fatalf("GetSymbolsByQN: %v", err)
	}
	if len(syms) == 0 {
		t.Fatalf("no symbols matched test.Hello — Go extractor QN shape may have changed")
	}
	for _, s := range syms {
		if s.Branch != "main" {
			t.Errorf("Symbol %q Branch = %q, want %q", s.QualifiedName, s.Branch, "main")
		}
	}
}

// Cross-check: after switching branches and re-indexing with force,
// the project's CurrentBranch flips and newly-written symbols carry
// the new branch.
func TestIndex_BranchSwitchUpdatesCurrentBranch(t *testing.T) {
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
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module t\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "x.go"), []byte("package t\nfunc F() {}\n"), 0o644)
	runGit("add", ".")
	runGit("commit", "-q", "-m", "i")

	dbDir := t.TempDir()
	store, _ := db.Open(dbDir)
	defer store.Close()
	idx := New(store)

	idx.Index(context.Background(), dir, true)
	projectID := db.ProjectIDFromPath(dir)
	p, _ := store.GetProject(projectID)
	if p.CurrentBranch != "main" {
		t.Fatalf("initial branch = %q, want main", p.CurrentBranch)
	}

	runGit("checkout", "-q", "-b", "feat/foo")
	// Force re-index so the no-change skip doesn't bypass the upsert.
	idx.Index(context.Background(), dir, true)
	p, _ = store.GetProject(projectID)
	if p.CurrentBranch != "feat/foo" {
		t.Errorf("after switch + force-reindex: CurrentBranch = %q, want feat/foo", p.CurrentBranch)
	}
}

// Negative: indexing a non-git directory leaves CurrentBranch empty
// (and doesn't error). The advisory + lookup paths both short-circuit
// on empty so this is the "git not in use" idle state.
func TestIndex_NonGitDirHasEmptyBranch(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module t\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "x.go"), []byte("package t\nfunc G() {}\n"), 0o644)

	dbDir := t.TempDir()
	store, _ := db.Open(dbDir)
	defer store.Close()
	idx := New(store)
	if _, err := idx.Index(context.Background(), dir, true); err != nil {
		t.Fatalf("Index non-git dir: %v", err)
	}
	p, _ := store.GetProject(db.ProjectIDFromPath(dir))
	if p == nil {
		t.Fatal("project row missing")
	}
	if p.CurrentBranch != "" {
		t.Errorf("non-git CurrentBranch = %q, want empty", p.CurrentBranch)
	}
}

// Negative — cache TTL: rapid back-to-back Index() calls reuse the
// cached branch so the subprocess spawn doesn't happen on every
// watcher tick. Indirect test: run Index twice in quick succession,
// verify the second one's project row still reflects the cached
// branch (a broken cache that returned empty would clobber it).
func TestIndex_BranchCacheReusedOnRapidReindex(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	runGit := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=t@t.t",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=t@t.t",
		)
		cmd.CombinedOutput()
	}
	runGit("init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module t\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "x.go"), []byte("package t\nfunc H() {}\n"), 0o644)
	runGit("add", ".")
	runGit("commit", "-q", "-m", "i")

	dbDir := t.TempDir()
	store, _ := db.Open(dbDir)
	defer store.Close()
	idx := New(store)

	idx.Index(context.Background(), dir, true)
	idx.Index(context.Background(), dir, false) // no force; exercise cache path
	p, _ := store.GetProject(db.ProjectIDFromPath(dir))
	if p.CurrentBranch != "main" {
		t.Errorf("second index lost cached branch: %q", p.CurrentBranch)
	}
}
