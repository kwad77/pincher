// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kwad77/pincher/internal/server"
)

// ── unit tests ───────────────────────────────────────────────────────────────

func TestPartitionImpactedTests_GroupsAndFilters(t *testing.T) {
	t.Parallel()
	tests := []server.ImpactedTest{
		{ID: "a", Name: "TestZeta", FilePath: "pkg/a/a_test.go", Overlap: 3},
		{ID: "b", Name: "TestAlpha", FilePath: "pkg/a/a_test.go", Overlap: 2},
		{ID: "c", Name: "TestRoot", FilePath: "root_test.go", Overlap: 1},
		// Not runnable: TS spec, Go benchmark, suite method outside _test.go.
		{ID: "d", Name: "renders login", FilePath: "web/login.spec.ts", Overlap: 1},
		{ID: "e", Name: "BenchmarkAdd", FilePath: "pkg/a/a_test.go", Overlap: 1},
	}
	pkgs, notRunnable, total := partitionImpactedTests(tests)

	if total != 3 {
		t.Errorf("totalRunnable = %d, want 3", total)
	}
	if len(pkgs) != 2 {
		t.Fatalf("packages = %d, want 2: %+v", len(pkgs), pkgs)
	}
	// Sorted by package name: "./" before "./pkg/a".
	if pkgs[0].Package != "./" || pkgs[1].Package != "./pkg/a" {
		t.Errorf("package order = %q, %q; want ./ then ./pkg/a", pkgs[0].Package, pkgs[1].Package)
	}
	// Test names sorted within the package; regex anchored and grouped.
	wantRegex := "^(TestAlpha|TestZeta)$"
	if pkgs[1].RunRegex != wantRegex {
		t.Errorf("run regex = %q, want %q", pkgs[1].RunRegex, wantRegex)
	}
	wantCmd := "go test ./pkg/a -run '^(TestAlpha|TestZeta)$' -count=1"
	if pkgs[1].Command != wantCmd {
		t.Errorf("command = %q, want %q", pkgs[1].Command, wantCmd)
	}
	if len(notRunnable) != 2 {
		t.Fatalf("notRunnable = %+v, want the .spec.ts and Benchmark entries", notRunnable)
	}
}

func TestParseFailingTests_DedupesAndKeepsSubtests(t *testing.T) {
	t.Parallel()
	out := `=== RUN   TestAdd
--- FAIL: TestAdd (0.00s)
    --- FAIL: TestAdd/negative (0.00s)
--- FAIL: TestAdd (0.00s)
FAIL
exit status 1`
	got := parseFailingTests(out)
	want := []string{"TestAdd", "TestAdd/negative"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("parseFailingTests = %v, want %v", got, want)
	}
}

func TestTailLines_Bounds(t *testing.T) {
	t.Parallel()
	if got := tailLines("a\nb\nc\n", 2); got != "b\nc" {
		t.Errorf("tailLines = %q, want %q", got, "b\nc")
	}
	if got := tailLines("a\nb", 30); got != "a\nb" {
		t.Errorf("tailLines short input = %q, want unchanged", got)
	}
}

// ── integration test ─────────────────────────────────────────────────────────

// writeImpactedFixtureFile writes content and fails the test on error.
func writeImpactedFixtureFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func runGitTI(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// TestTestImpactedCLI_Binary_EndToEnd exercises `pincher test-impacted`
// against a real temp repo: index two functions + their tests, edit one,
// and assert the command selects ONLY that function's test (dry-run),
// runs it (pass exit 0), reports failure (exit 1) when the test breaks,
// and handles the clean-tree and zero-impacted-tests paths. Mirrors the
// TestIndexCLI_Binary_* patterns (#185 coverage workaround).
func TestTestImpactedCLI_Binary_EndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping CLI binary build in -short mode")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}

	bin := buildPincherBinary(t)
	dataDir := t.TempDir()
	projDir := t.TempDir()
	// Canonicalize so the cwd-derived project ID matches the index-time ID
	// even when the temp dir sits behind a symlink (macOS /tmp).
	if resolved, err := filepath.EvalSymlinks(projDir); err == nil {
		projDir = resolved
	}

	const calcOriginal = "package tiproj\n\nfunc Add(a, b int) int { return a + b }\n\nfunc Mul(a, b int) int { return a * b }\n"
	writeImpactedFixtureFile(t, filepath.Join(projDir, "go.mod"), "module tiproj\n\ngo 1.22\n")
	writeImpactedFixtureFile(t, filepath.Join(projDir, "calc.go"), calcOriginal)
	writeImpactedFixtureFile(t, filepath.Join(projDir, "orphan.go"), "package tiproj\n\nfunc Orphan() int { return 1 }\n")
	writeImpactedFixtureFile(t, filepath.Join(projDir, "calc_test.go"),
		"package tiproj\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 {\n\t\tt.Fatal(\"Add broken\")\n\t}\n}\n\nfunc TestMul(t *testing.T) {\n\tif Mul(2, 3) != 6 {\n\t\tt.Fatal(\"Mul broken\")\n\t}\n}\n")
	runGitTI(t, projDir, "init", "-q")
	runGitTI(t, projDir, "config", "user.email", "test@test.com")
	runGitTI(t, projDir, "config", "user.name", "Test")
	runGitTI(t, projDir, "add", ".")
	runGitTI(t, projDir, "commit", "-q", "-m", "init")

	// Index the fixture so the call graph (TestAdd → Add, TestMul → Mul)
	// and IsTest flags exist.
	idxCmd := exec.Command(bin, "index", "--data-dir", dataDir, projDir)
	idxCmd.Env = pincherCoverEnv()
	if out, err := idxCmd.CombinedOutput(); err != nil {
		t.Fatalf("pincher index: %v\n%s", err, out)
	}

	runImpacted := func(args ...string) (string, int) {
		t.Helper()
		cmd := exec.Command(bin, append([]string{"test-impacted", "--data-dir", dataDir}, args...)...)
		cmd.Dir = projDir
		cmd.Env = pincherCoverEnv()
		out, err := cmd.CombinedOutput()
		code := 0
		if err != nil {
			exitErr, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatalf("pincher test-impacted %v: %v\n%s", args, err, out)
			}
			code = exitErr.ExitCode()
		}
		return string(out), code
	}

	t.Run("clean tree reports no changes, exit 0", func(t *testing.T) {
		out, code := runImpacted()
		if code != 0 {
			t.Fatalf("exit code = %d, want 0\n%s", code, out)
		}
		if !strings.Contains(out, "no changes detected") {
			t.Errorf("expected 'no changes detected'; got:\n%s", out)
		}
	})

	// Edit Add (same line, behaviour preserved) so the diff hunk
	// intersects only Add's symbol range.
	writeImpactedFixtureFile(t, filepath.Join(projDir, "calc.go"),
		"package tiproj\n\nfunc Add(a, b int) int { return b + a }\n\nfunc Mul(a, b int) int { return a * b }\n")

	t.Run("dry-run selects only the edited function's test", func(t *testing.T) {
		out, code := runImpacted("--dry-run")
		if code != 0 {
			t.Fatalf("exit code = %d, want 0\n%s", code, out)
		}
		if !strings.Contains(out, "TestAdd") {
			t.Errorf("dry-run should select TestAdd; got:\n%s", out)
		}
		if strings.Contains(out, "TestMul") {
			t.Errorf("dry-run must NOT select TestMul (Mul is unchanged); got:\n%s", out)
		}
		if !strings.Contains(out, "-run") || !strings.Contains(out, "-count=1") {
			t.Errorf("dry-run should print runnable go test commands; got:\n%s", out)
		}
		if !strings.Contains(out, "DRY RUN") {
			t.Errorf("dry-run summary line missing; got:\n%s", out)
		}
	})

	t.Run("impacted test passes, exit 0", func(t *testing.T) {
		out, code := runImpacted()
		if code != 0 {
			t.Fatalf("exit code = %d, want 0\n%s", code, out)
		}
		if !strings.Contains(out, "ok") || !strings.Contains(out, "1 tests") {
			t.Errorf("expected per-package ok line; got:\n%s", out)
		}
		if !strings.Contains(out, "IMPACTED: 1 packages, 1 tests — PASS") {
			t.Errorf("expected PASS summary; got:\n%s", out)
		}
	})

	t.Run("json output is well-formed", func(t *testing.T) {
		out, code := runImpacted("--json")
		if code != 0 {
			t.Fatalf("exit code = %d, want 0\n%s", code, out)
		}
		var body map[string]any
		if err := json.Unmarshal([]byte(out), &body); err != nil {
			t.Fatalf("invalid JSON: %v\n%s", err, out)
		}
		summary, _ := body["summary"].(map[string]any)
		if summary["status"] != "PASS" {
			t.Errorf("summary.status = %v, want PASS\n%s", summary["status"], out)
		}
		if tests, _ := summary["tests"].(float64); tests != 1 {
			t.Errorf("summary.tests = %v, want 1\n%s", summary["tests"], out)
		}
	})

	t.Run("broken function fails its test, exit 1", func(t *testing.T) {
		writeImpactedFixtureFile(t, filepath.Join(projDir, "calc.go"),
			"package tiproj\n\nfunc Add(a, b int) int { return a + b + 1 }\n\nfunc Mul(a, b int) int { return a * b }\n")
		out, code := runImpacted()
		if code != 1 {
			t.Fatalf("exit code = %d, want 1\n%s", code, out)
		}
		if !strings.Contains(out, "FAIL") {
			t.Errorf("expected FAIL line; got:\n%s", out)
		}
		if !strings.Contains(out, "failing: TestAdd") {
			t.Errorf("expected failing test names; got:\n%s", out)
		}
		if !strings.Contains(out, "— FAIL") {
			t.Errorf("expected FAIL summary; got:\n%s", out)
		}
	})

	t.Run("diff with zero impacted tests says so, exit 0", func(t *testing.T) {
		runGitTI(t, projDir, "checkout", "--", "calc.go")
		writeImpactedFixtureFile(t, filepath.Join(projDir, "orphan.go"),
			"package tiproj\n\nfunc Orphan() int { return 2 }\n")
		out, code := runImpacted()
		if code != 0 {
			t.Fatalf("exit code = %d, want 0\n%s", code, out)
		}
		if !strings.Contains(out, "no indexed test reaches the changed symbols") {
			t.Errorf("expected zero-impacted message; got:\n%s", out)
		}
		if !strings.Contains(out, "changes") {
			t.Errorf("message should point at the `changes` tool for the blast radius; got:\n%s", out)
		}
	})

	t.Run("unindexed repo errors pedagogically, exit 1", func(t *testing.T) {
		strangerDir := t.TempDir()
		cmd := exec.Command(bin, "test-impacted", "--data-dir", dataDir)
		cmd.Dir = strangerDir
		cmd.Env = pincherCoverEnv()
		out, err := cmd.CombinedOutput()
		exitErr, ok := err.(*exec.ExitError)
		if !ok || exitErr.ExitCode() != 1 {
			t.Fatalf("expected exit 1 in unindexed dir; err=%v\n%s", err, out)
		}
		if !strings.Contains(string(out), "pincher index") {
			t.Errorf("error should name `pincher index`; got:\n%s", out)
		}
	})
}
