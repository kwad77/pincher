// SPDX-License-Identifier: MIT

package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// #1303 Phase 2a: branchDriftAdvisory pure-logic + integration tests.

// Negative: empty project list → empty advisory. The doctor handler
// always calls the helper, so the no-fixture path must be silent.
func TestBranchDriftAdvisory_EmptyProjects(t *testing.T) {
	if got := branchDriftAdvisory(nil); got != "" {
		t.Errorf("empty projects produced advisory: %q", got)
	}
	if got := branchDriftAdvisory([]db.Project{}); got != "" {
		t.Errorf("zero-length projects produced advisory: %q", got)
	}
}

// Negative: projects with empty CurrentBranch are skipped (pre-#1303
// Phase 2a rows). The helper must not invoke git on them — if it did,
// projects with `path` pointing at non-existent directories would
// either error or hang. Cross-check by passing a path that doesn't
// exist: with CurrentBranch="" the helper short-circuits.
func TestBranchDriftAdvisory_SkipsEmptyBranchRows(t *testing.T) {
	projects := []db.Project{
		{ID: "p1", Name: "legacy", Path: "/nonexistent/path", CurrentBranch: ""},
	}
	if got := branchDriftAdvisory(projects); got != "" {
		t.Errorf("legacy row (empty branch) produced advisory: %q", got)
	}
}

// Positive integration: spin up a real git repo on branch `main`,
// register it as a project with CurrentBranch=`main`, verify no
// advisory. Then switch the repo to `feat/foo` and verify the
// advisory fires with both names visible.
func TestBranchDriftAdvisory_FiresOnRealCheckout(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	runGit("add", "README.md")
	runGit("commit", "-q", "-m", "initial")

	// In-sync case: advisory silent.
	projects := []db.Project{{ID: "p1", Name: "sample", Path: dir, CurrentBranch: "main"}}
	if got := branchDriftAdvisory(projects); got != "" {
		t.Errorf("in-sync project produced advisory: %q", got)
	}

	// Drifted case: checkout a new branch, leave CurrentBranch=`main`.
	runGit("checkout", "-q", "-b", "feat/foo")
	got := branchDriftAdvisory(projects)
	if got == "" {
		t.Fatal("drifted project produced no advisory")
	}
	for _, want := range []string{"sample", "indexed=main", "on-disk=feat/foo", "#1303"} {
		if !strings.Contains(got, want) {
			t.Errorf("advisory missing %q; got: %s", want, got)
		}
	}
}

// Cross-check: multiple drifted projects are surfaced with a count
// prefix and sorted alphabetically. Beyond the maxToShow cap, a
// "+N more" suffix is appended.
func TestBranchDriftAdvisory_MultipleProjectsSortedAndCapped(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	makeRepo := func(branch string) string {
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
		runGit("init", "-q", "-b", branch)
		os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644)
		runGit("add", "f")
		runGit("commit", "-q", "-m", "i")
		return dir
	}

	var projects []db.Project
	for i := 0; i < 7; i++ {
		// All repos created on `actual` but the stored CurrentBranch
		// claims `stale`, so all 7 read as drifted.
		dir := makeRepo("actual")
		projects = append(projects, db.Project{
			ID:            "p" + string(rune('0'+i)),
			Name:          string(rune('a' + i)), // a, b, c, d, e, f, g
			Path:          dir,
			CurrentBranch: "stale",
		})
	}
	got := branchDriftAdvisory(projects)
	if got == "" {
		t.Fatal("expected advisory across 7 drifted projects")
	}
	if !strings.Contains(got, "7 projects") {
		t.Errorf("missing count prefix; got: %s", got)
	}
	if !strings.Contains(got, "+2 more") {
		t.Errorf("missing +2-more cap suffix (showed 5, drifted 7); got: %s", got)
	}
	// Sort check: the first listed project should be "a" (alphabetical).
	idxA := strings.Index(got, "a (indexed=stale")
	idxE := strings.Index(got, "e (indexed=stale")
	if idxA < 0 || idxE < 0 || idxA >= idxE {
		t.Errorf("expected a before e in sorted output; got: %s", got)
	}
}
