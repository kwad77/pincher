// SPDX-License-Identifier: MIT

package index

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// #1475 v0.78: parent project must NOT descend into nested
// .claude/worktrees/ subdirectories (Claude Code's ephemeral git-
// worktree convention). The walker's ExcludeDirectory list now
// includes `.claude` so gocodewalker skips the subtree before
// emitting any file under it.

func TestIndex_SkipsClaudeWorktrees_1475(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()

	// Parent project: one real source file at top level.
	writeFile(t, dir, "main.go", "package main\nfunc main() {}\n")

	// Simulated Claude Code worktree under .claude/worktrees/<branch>/.
	// Same shape: a Go file at the equivalent path the parent has.
	// Pre-fix the walker would emit this file under the parent's
	// project_id, producing a duplicate `main.go` row across projects.
	writeFile(t, dir, ".claude/worktrees/feature-x/main.go",
		"package main\nfunc main() {}\n")

	// Also drop a settings file the agent writes; it shouldn't index either.
	writeFile(t, dir, ".claude/settings.json", `{"foo":"bar"}`)

	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("Index: %v", err)
	}

	projectID := db.ProjectIDFromPath(dir)
	files, err := store.ListFilesForProject(projectID)
	if err != nil {
		t.Fatalf("ListFilesForProject: %v", err)
	}

	for _, f := range files {
		if filepath.ToSlash(f) == ".claude/worktrees/feature-x/main.go" {
			t.Errorf(".claude/worktrees/ file leaked into parent index: %s", f)
		}
		if filepath.ToSlash(f) == ".claude/settings.json" {
			t.Errorf(".claude/ settings file indexed when it shouldn't be: %s", f)
		}
	}

	// Sanity: the real source file MUST still be indexed.
	var sawMain bool
	for _, f := range files {
		if filepath.ToSlash(f) == "main.go" {
			sawMain = true
		}
	}
	if !sawMain {
		t.Errorf("real main.go not indexed; files=%v", files)
	}
}
