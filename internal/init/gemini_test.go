// SPDX-License-Identifier: MIT

package init

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #658: Gemini init target wave 1. ./GEMINI.md at project root (or
// ~/.gemini/GEMINI.md with --global), same shape as Claude Code's
// CLAUDE.md.

func TestGemini_FreshWriteCreatesGEMINIMd(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	path, err := GeminiTarget.PathFn(cwd, false)
	if err != nil {
		t.Fatalf("PathFn: %v", err)
	}
	if got, want := path, filepath.Join(cwd, "GEMINI.md"); got != want {
		t.Errorf("PathFn = %q, want %q", got, want)
	}

	out, action := GeminiTarget.WriteFn("", samplePolicy)
	if action != "wrote" {
		t.Errorf("action = %q, want wrote", action)
	}
	if !strings.Contains(out, MarkerStart) || !strings.Contains(out, MarkerEnd) {
		t.Error("expected pincher marker block in fresh write")
	}
}

// Idempotent re-run replaces in place; second write must equal first.
func TestGemini_IdempotentRewrite(t *testing.T) {
	t.Parallel()
	first, _ := GeminiTarget.WriteFn("", samplePolicy)
	second, action := GeminiTarget.WriteFn(first, samplePolicy)
	if action != "updated" {
		t.Errorf("second action = %q, want updated", action)
	}
	if first != second {
		t.Errorf("non-idempotent rewrite — bytes diverge between runs")
	}
}

// User-edited content outside the marker block survives re-run.
func TestGemini_PreservesUnmanagedContent(t *testing.T) {
	t.Parallel()
	preamble := "# My GEMINI rules\n\nUse clean architecture.\n\n"
	managed, _ := GeminiTarget.WriteFn("", samplePolicy)
	existing := preamble + managed + "\n\n## Custom\n- Mind allocations.\n"

	updated, _ := GeminiTarget.WriteFn(existing, samplePolicy)
	if !strings.Contains(updated, "Use clean architecture.") {
		t.Error("preamble lost on re-run")
	}
	if !strings.Contains(updated, "Mind allocations.") {
		t.Error("postscript lost on re-run")
	}
}

func TestGemini_DetectFn_MatchesGEMINIMdOrGeminiDir(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	if GeminiTarget.DetectFn(cwd) {
		t.Errorf("empty dir should not detect Gemini")
	}

	// GEMINI.md file case.
	if err := os.WriteFile(filepath.Join(cwd, "GEMINI.md"), []byte("# rules"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !GeminiTarget.DetectFn(cwd) {
		t.Error("expected detect to fire on GEMINI.md presence")
	}

	// .gemini/ directory case (separate temp dir).
	cwd2 := t.TempDir()
	if err := os.Mkdir(filepath.Join(cwd2, ".gemini"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !GeminiTarget.DetectFn(cwd2) {
		t.Error("expected detect to fire on .gemini/ directory presence")
	}
}

func TestGemini_GlobalPathResolvesUnderHome(t *testing.T) {
	// Cannot t.Parallel() — withHome uses t.Setenv.
	home := t.TempDir()
	withHome(t, home)

	path, err := GeminiTarget.PathFn(t.TempDir(), true)
	if err != nil {
		t.Fatalf("PathFn global: %v", err)
	}
	want := filepath.Join(home, ".gemini", "GEMINI.md")
	if path != want {
		t.Errorf("global PathFn = %q, want %q", path, want)
	}
}
