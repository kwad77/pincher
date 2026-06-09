// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// computeBranchOverlap verdict tiers — unit-tested against a seeded
// store, no git. shared.go holds two symbols; the file lists decide
// which verdict fires.
func TestComputeBranchOverlap_VerdictTiers(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "p-overlap"
	store.UpsertProject(db.Project{ID: pid, Path: t.TempDir(), Name: "overlap", IndexedAt: time.Now()})
	srv.sessionID = pid
	mustUpsertSymbols(t, store, []db.Symbol{
		{ID: pid + "::pkg.Shared1#Function", ProjectID: pid, FilePath: "shared.go",
			Name: "Shared1", QualifiedName: "pkg.Shared1", Kind: "Function", Language: "Go",
			StartLine: 2, EndLine: 4, ExtractionConfidence: 1.0},
		{ID: pid + "::pkg.Shared2#Function", ProjectID: pid, FilePath: "shared.go",
			Name: "Shared2", QualifiedName: "pkg.Shared2", Kind: "Function", Language: "Go",
			StartLine: 8, EndLine: 10, ExtractionConfidence: 1.0},
	})

	t.Run("independent — disjoint files", func(t *testing.T) {
		got := computeBranchOverlap(srv, pid, []string{"a.go"}, []string{"b.go"}, nil, nil)
		if len(got.OverlappingFiles) != 0 || len(got.OverlappingSymbols) != 0 {
			t.Errorf("disjoint files should not overlap; got %+v", got)
		}
		if got.Verdict == "" || !strings.Contains(got.Verdict, "independent") {
			t.Errorf("verdict = %q, want 'independent'", got.Verdict)
		}
	})

	t.Run("low risk — shared file, no indexed symbols", func(t *testing.T) {
		got := computeBranchOverlap(srv, pid, []string{"config.yaml"}, []string{"config.yaml"}, nil, nil)
		if len(got.OverlappingFiles) != 1 {
			t.Fatalf("expected 1 overlapping file; got %v", got.OverlappingFiles)
		}
		if len(got.OverlappingSymbols) != 0 {
			t.Errorf("config.yaml has no seeded symbols; got %v", got.OverlappingSymbols)
		}
		if !strings.Contains(got.Verdict, "low risk") {
			t.Errorf("verdict = %q, want 'low risk'", got.Verdict)
		}
	})

	t.Run("merge-order risk — shared symbols", func(t *testing.T) {
		got := computeBranchOverlap(srv, pid, []string{"shared.go", "a.go"}, []string{"shared.go", "b.go"}, nil, nil)
		if len(got.OverlappingFiles) != 1 || got.OverlappingFiles[0] != "shared.go" {
			t.Errorf("expected shared.go to overlap; got %v", got.OverlappingFiles)
		}
		if len(got.OverlappingSymbols) != 2 {
			t.Errorf("expected 2 shared symbols from shared.go; got %v", got.OverlappingSymbols)
		}
		if !strings.Contains(got.Verdict, "merge-order risk") {
			t.Errorf("verdict = %q, want 'merge-order risk'", got.Verdict)
		}
	})

	t.Run("hunks narrow shared file to symbols touched by both branches", func(t *testing.T) {
		got := computeBranchOverlap(
			srv,
			pid,
			[]string{"shared.go"},
			[]string{"shared.go"},
			map[string][][2]int{"shared.go": {{2, 3}}},
			map[string][][2]int{"shared.go": {{3, 4}}},
		)
		if len(got.OverlappingSymbols) != 1 || !strings.Contains(got.OverlappingSymbols[0], "Shared1") {
			t.Fatalf("expected only Shared1 to overlap edited hunks; got %+v", got.OverlappingSymbols)
		}
		if got.OverlappingSymbolCount != 1 {
			t.Fatalf("OverlappingSymbolCount = %d, want 1", got.OverlappingSymbolCount)
		}
	})
}

// TestHandleBranchOverlap_GitIntegration drives the full handler: a
// real repo, two branches that both touch a shared file, indexed
// symbols, and the git diff / merge-base plumbing.
func TestHandleBranchOverlap_GitIntegration(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	dir := t.TempDir()
	if out, err := runCmd(t, dir, "git", "init"); err != nil {
		t.Skipf("git not available: %v (%s)", err, out)
	}
	runCmd(t, dir, "git", "config", "user.email", "test@test.com")
	runCmd(t, dir, "git", "config", "user.name", "Test")
	runCmd(t, dir, "git", "config", "commit.gpgsign", "false")

	// Base commit on the default branch: shared.go + base.go.
	os.WriteFile(filepath.Join(dir, "shared.go"), []byte("package p\nfunc Shared() {}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "base.go"), []byte("package p\nfunc Base() {}\n"), 0o644)
	runCmd(t, dir, "git", "add", ".")
	runCmd(t, dir, "git", "commit", "-m", "base")
	baseBranch, _ := runCmd(t, dir, "git", "rev-parse", "--abbrev-ref", "HEAD")
	baseBranch = strings.TrimSpace(baseBranch)

	// feat-a: touches shared.go + adds a.go.
	runCmd(t, dir, "git", "checkout", "-b", "feat-a")
	os.WriteFile(filepath.Join(dir, "shared.go"), []byte("package p\nfunc Shared() { _ = 1 }\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package p\nfunc A() {}\n"), 0o644)
	runCmd(t, dir, "git", "add", ".")
	runCmd(t, dir, "git", "commit", "-m", "feat-a")

	// feat-b: from base, touches shared.go + adds b.go.
	runCmd(t, dir, "git", "checkout", baseBranch)
	runCmd(t, dir, "git", "checkout", "-b", "feat-b")
	os.WriteFile(filepath.Join(dir, "shared.go"), []byte("package p\nfunc Shared() { _ = 2 }\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.go"), []byte("package p\nfunc B() {}\n"), 0o644)
	runCmd(t, dir, "git", "add", ".")
	runCmd(t, dir, "git", "commit", "-m", "feat-b")

	store.UpsertProject(db.Project{ID: dir, Path: dir, Name: "overlap-git", IndexedAt: time.Now()})
	srv.sessionID = dir
	srv.sessionRoot = dir
	mustUpsertSymbols(t, store, []db.Symbol{
		{ID: dir + "::p.Shared#Function", ProjectID: dir, FilePath: "shared.go",
			Name: "Shared", QualifiedName: "p.Shared", Kind: "Function", Language: "Go",
			StartLine: 2, EndLine: 2, ExtractionConfidence: 1.0},
	})

	res, err := srv.handleBranchOverlap(context.Background(), makeReq(map[string]any{
		"branch_a": "feat-a", "branch_b": "feat-b",
	}))
	if err != nil {
		t.Fatalf("handleBranchOverlap: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success; got %s", textOf(t, res))
	}
	body := decode(t, res)

	files, _ := body["overlapping_files"].([]any)
	if len(files) != 1 || files[0] != "shared.go" {
		t.Errorf("overlapping_files = %v, want [shared.go]", files)
	}
	syms, _ := body["overlapping_symbols"].([]any)
	if len(syms) != 1 {
		t.Errorf("overlapping_symbols = %v, want the one Shared symbol", syms)
	}
	if v, _ := body["verdict"].(string); !strings.Contains(v, "merge-order risk") {
		t.Errorf("verdict = %q, want 'merge-order risk'", v)
	}
}

// TestHandleBranchOverlap_MissingBranches is the negative path.
func TestHandleBranchOverlap_MissingBranches(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "p-overlap-neg"
	store.UpsertProject(db.Project{ID: pid, Path: t.TempDir(), Name: pid, IndexedAt: time.Now()})
	srv.sessionID = pid

	res, err := srv.handleBranchOverlap(context.Background(), makeReq(map[string]any{
		"branch_a": "feat-a", // branch_b omitted
	}))
	if err != nil {
		t.Fatalf("handleBranchOverlap: %v", err)
	}
	if !res.IsError {
		t.Error("expected an error result when branch_b is omitted")
	}
}
