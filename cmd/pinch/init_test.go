// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	pinit "github.com/kwad77/pincher/internal/init"
)

// TestResolveInitTargetChoice covers every branch of the
// host-resolution → target decision: conclusive resolver result,
// inconclusive + interactive picker, inconclusive + non-interactive
// (caller must refuse). #1862.
func TestResolveInitTargetChoice(t *testing.T) {
	t.Parallel()

	// Conclusive resolver result → that target, ignoring streams.
	got, ok := resolveInitTargetChoice(
		pinit.AutoResolveResult{Target: "codex", Reason: "running under host codex", Decided: true},
		&bytes.Buffer{}, strings.NewReader(""), false)
	if !ok || got != "codex" {
		t.Errorf("decided result: got (%q,%v), want (codex,true)", got, ok)
	}

	// Inconclusive + interactive → the picker resolves it.
	got, ok = resolveInitTargetChoice(
		pinit.AutoResolveResult{Decided: false},
		&bytes.Buffer{}, strings.NewReader("2\n"), true)
	if !ok || got != "codex" {
		t.Errorf("inconclusive+interactive: got (%q,%v), want (codex,true)", got, ok)
	}

	// Inconclusive + non-interactive → no guess; caller refuses.
	if _, ok := resolveInitTargetChoice(
		pinit.AutoResolveResult{Decided: false},
		&bytes.Buffer{}, strings.NewReader(""), false); ok {
		t.Error("inconclusive + non-interactive must report ok=false, not guess")
	}

	// Inconclusive + interactive + unusable picker input → ok=false.
	if _, ok := resolveInitTargetChoice(
		pinit.AutoResolveResult{Decided: false},
		&bytes.Buffer{}, strings.NewReader("garbage\n"), true); ok {
		t.Error("inconclusive + bad picker input must report ok=false")
	}
}

// TestInitCLI_Binary_AutoDetectsHostFromEnv — #1862. Bare `pincher
// init` (no --target) auto-resolves the host: CLAUDECODE in the env
// ⇒ claude. Exercises the runInitCLI auto-resolve wiring and
// autoResolveInitTarget end to end through the instrumented binary.
func TestInitCLI_Binary_AutoDetectsHostFromEnv(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping CLI binary build in -short mode")
	}
	bin := buildPincherBinary(t)
	workdir := t.TempDir()
	cmd := exec.Command(bin, "init", "--dry-run") // no --target
	cmd.Dir = workdir
	// Force CLAUDECODE so resolution is deterministic regardless of the
	// host the test itself runs under (CI has it unset, a dev box may
	// have it set).
	cmd.Env = append(pincherCoverEnv(), "CLAUDECODE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bare init with CLAUDECODE set should resolve to claude: %v\n%s", err, out)
	}
	got := string(out)
	if !strings.Contains(got, "no --target given") || !strings.Contains(got, "claude") {
		t.Errorf("expected a host-detection note naming claude; got:\n%s", got)
	}
}

// TestPromptInitTarget covers the interactive picker shown when
// `pincher init` can't auto-detect the host (#1862). Feeds canned
// stdin so every menu branch is exercised without a TTY.
func TestPromptInitTarget(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, in, want string
		ok             bool
	}{
		{"shortlist by number", "1\n", "claude", true},
		{"shortlist by number 2", "2\n", "codex", true},
		{"literal target name", "cursor\n", "cursor", true},
		{"detect by number", "6\n", "detect", true},
		{"detect by name", "detect\n", "detect", true},
		{"other then full-list number", "7\n2\n", pinit.AllTargets[1].Name, true},
		{"EOF — no input", "", "", false},
		{"out-of-range number", "99\n", "", false},
		{"garbage", "not-a-target\n", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			got, ok := promptInitTarget(&out, strings.NewReader(c.in))
			if got != c.want || ok != c.ok {
				t.Errorf("promptInitTarget(%q) = (%q,%v), want (%q,%v)", c.in, got, ok, c.want, c.ok)
			}
		})
	}
}

// CLI binary tests. The merge primitives, target writers, and detect
// logic are tested directly in internal/init; this file covers the
// orchestration: flag parsing, --target dispatch, dry-run vs write,
// stdout copy. The tests build a coverage-instrumented binary and
// exec it so the runInitCLI path picks up real coverage.

func TestInitCLI_Binary_DryRun(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping CLI binary build in -short mode")
	}

	bin := buildPincherBinary(t)

	workdir := t.TempDir()
	cmd := exec.Command(bin, "init", "--target", "claude", "--dry-run")
	cmd.Dir = workdir
	cmd.Env = pincherCoverEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("pincher init --dry-run: %v\n%s", err, out)
	}
	got := string(out)
	if !strings.Contains(got, "would wrote") && !strings.Contains(got, "would write") {
		t.Errorf("expected dry-run preface; got:\n%s", got)
	}
	if !strings.Contains(got, pinit.MarkerStart) {
		t.Errorf("dry-run output should include start marker; got:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(workdir, "CLAUDE.md")); err == nil {
		t.Error("dry-run should not create CLAUDE.md, but it exists")
	}
}

// #631: `pincher init` prints a per-language extraction-tier profile
// after the wiring step. --quiet suppresses for CI/scripted installs.
func TestInitCLI_Binary_ProfilePrintedByDefault(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping CLI binary build in -short mode")
	}
	bin := buildPincherBinary(t)
	workdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workdir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "init", "--target", "claude", "--data-dir", t.TempDir(), "--no-hook")
	cmd.Dir = workdir
	cmd.Env = pincherCoverEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	got := string(out)
	for _, want := range []string{"Project profile:", "Headline tier:", "Go"} {
		if !strings.Contains(got, want) {
			t.Errorf("init output should include %q; got:\n%s", want, got)
		}
	}
}

func TestInitCLI_Binary_QuietSuppressesProfile(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping CLI binary build in -short mode")
	}
	bin := buildPincherBinary(t)
	workdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workdir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "init", "--target", "claude", "--quiet", "--data-dir", t.TempDir(), "--no-hook")
	cmd.Dir = workdir
	cmd.Env = pincherCoverEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("init --quiet: %v\n%s", err, out)
	}
	got := string(out)
	if strings.Contains(got, "Project profile:") {
		t.Errorf("--quiet should suppress profile; got:\n%s", got)
	}
	// Wiring still ran: marker block written.
	target := filepath.Join(workdir, "CLAUDE.md")
	if _, err := os.Stat(target); err != nil {
		t.Errorf("--quiet should still write the marker block: %v", err)
	}
}

func TestInitCLI_Binary_WriteThenIdempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping CLI binary build in -short mode")
	}

	bin := buildPincherBinary(t)

	workdir := t.TempDir()
	cmd := exec.Command(bin, "init", "--target", "claude", "--data-dir", t.TempDir())
	cmd.Dir = workdir
	cmd.Env = pincherCoverEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("first init: %v\n%s", err, out)
	}

	target := filepath.Join(workdir, "CLAUDE.md")
	first, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read CLAUDE.md: %v", err)
	}
	if !strings.Contains(string(first), pinit.MarkerStart) {
		t.Fatalf("first init didn't write the marker block:\n%s", first)
	}

	cmd2 := exec.Command(bin, "init", "--target", "claude", "--data-dir", t.TempDir())
	cmd2.Dir = workdir
	cmd2.Env = pincherCoverEnv()
	if out, err := cmd2.CombinedOutput(); err != nil {
		t.Fatalf("second init: %v\n%s", err, out)
	}
	second, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read CLAUDE.md after re-init: %v", err)
	}
	if string(first) != string(second) {
		t.Errorf("re-running init should be idempotent\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}

	startCount := strings.Count(string(second), pinit.MarkerStart)
	endCount := strings.Count(string(second), pinit.MarkerEnd)
	if startCount != 1 || endCount != 1 {
		t.Errorf("expected exactly one marker pair, got start=%d end=%d", startCount, endCount)
	}
}

func TestInitCLI_Binary_PreservesExistingContent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping CLI binary build in -short mode")
	}

	bin := buildPincherBinary(t)

	workdir := t.TempDir()
	target := filepath.Join(workdir, "CLAUDE.md")
	pre := "# CLAUDE.md\n\nProject-specific guidance:\n- Use snake_case for filenames.\n- Tests live in tests/.\n"
	if err := os.WriteFile(target, []byte(pre), 0o644); err != nil {
		t.Fatalf("seed CLAUDE.md: %v", err)
	}

	cmd := exec.Command(bin, "init", "--target", "claude", "--data-dir", t.TempDir())
	cmd.Dir = workdir
	cmd.Env = pincherCoverEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read post-init: %v", err)
	}
	gotStr := string(got)
	if !strings.Contains(gotStr, "Project-specific guidance:") {
		t.Error("existing content was lost")
	}
	if !strings.Contains(gotStr, "snake_case") {
		t.Error("existing bullet was lost")
	}
	if !strings.Contains(gotStr, pinit.MarkerStart) {
		t.Error("pincher block missing")
	}
}

func TestInitCLI_Binary_TargetCursor(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping CLI binary build in -short mode")
	}

	bin := buildPincherBinary(t)
	workdir := t.TempDir()
	cmd := exec.Command(bin, "init", "--target", "cursor", "--data-dir", t.TempDir())
	cmd.Dir = workdir
	cmd.Env = pincherCoverEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pincher init --target=cursor: %v\n%s", err, out)
	}
	want := filepath.Join(workdir, ".cursor", "rules", "pincher.mdc")
	got, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("expected %s to exist: %v", want, err)
	}
	if !strings.HasPrefix(string(got), "---\n") {
		t.Errorf("cursor target should start with frontmatter delimiter; got: %s", got[:50])
	}
}

func TestInitCLI_Binary_TargetAll(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping CLI binary build in -short mode")
	}

	bin := buildPincherBinary(t)
	workdir := t.TempDir()
	homeDir := t.TempDir()
	cmd := exec.Command(bin, "init", "--target", "all", "--data-dir", t.TempDir())
	cmd.Dir = workdir
	env := pincherCoverEnv()
	env = append(env,
		"HOME="+homeDir,
		"USERPROFILE="+homeDir,
		"CODEX_HOME="+filepath.Join(homeDir, ".codex"),
	)
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pincher init --target=all: %v\n%s", err, out)
	}
	for _, sub := range []string{
		filepath.Join(workdir, "CLAUDE.md"),
		filepath.Join(workdir, ".cursor", "rules", "pincher.mdc"),
		filepath.Join(workdir, ".cursorrules"),
		filepath.Join(workdir, ".windsurf", "rules", "pincher.md"),
		filepath.Join(workdir, "CONVENTIONS.md"),
		filepath.Join(homeDir, ".continue", "config.json"),
	} {
		if _, err := os.Stat(sub); err != nil {
			t.Errorf("expected %s to exist after --target=all: %v", sub, err)
		}
	}
}

func TestInitCLI_Binary_TargetDetect(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping CLI binary build in -short mode")
	}

	bin := buildPincherBinary(t)
	workdir := t.TempDir()
	homeDir := t.TempDir()
	// #1769: modern Windsurf detection keys on the .windsurf/ rules
	// directory — create it so `detect` picks windsurf up.
	if err := os.MkdirAll(filepath.Join(workdir, ".windsurf", "rules"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "init", "--target", "detect", "--data-dir", t.TempDir())
	cmd.Dir = workdir
	env := pincherCoverEnv()
	env = append(env,
		"HOME="+homeDir,
		"USERPROFILE="+homeDir,
		"CODEX_HOME="+filepath.Join(homeDir, ".codex"),
	)
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pincher init --target=detect: %v\n%s", err, out)
	}
	got, err := os.ReadFile(filepath.Join(workdir, ".windsurf", "rules", "pincher.md"))
	if err != nil {
		t.Fatalf(".windsurf/rules/pincher.md should exist: %v", err)
	}
	if !strings.Contains(string(got), pinit.MarkerStart) {
		t.Error("expected pincher block in .windsurf/rules/pincher.md")
	}
	if _, err := os.Stat(filepath.Join(workdir, "CLAUDE.md")); err == nil {
		t.Error("CLAUDE.md should not be written when only windsurf was detected")
	}
}

func TestInitCLI_Binary_UnknownTargetExits(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping CLI binary build in -short mode")
	}

	bin := buildPincherBinary(t)
	cmd := exec.Command(bin, "init", "--target", "vim", "--dry-run")
	cmd.Dir = t.TempDir()
	cmd.Env = pincherCoverEnv()
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit on unknown --target; got output: %s", out)
	}
	if !strings.Contains(string(out), "unknown --target") {
		t.Errorf("expected 'unknown --target' message; got: %s", out)
	}
}
