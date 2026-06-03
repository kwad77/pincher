// SPDX-License-Identifier: MIT

package init

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #658: Warp init target wave 1. ./WARP.md at project root (or
// ~/.warp/WARP.md with --global), same shape as Claude Code's
// CLAUDE.md.

func TestWarp_FreshWriteCreatesWARPMd(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	path, err := WarpTarget.PathFn(cwd, false)
	if err != nil {
		t.Fatalf("PathFn: %v", err)
	}
	if got, want := path, filepath.Join(cwd, "WARP.md"); got != want {
		t.Errorf("PathFn = %q, want %q", got, want)
	}

	out, action := WarpTarget.WriteFn("", samplePolicy)
	if action != "wrote" {
		t.Errorf("action = %q, want wrote", action)
	}
	if !strings.Contains(out, MarkerStart) || !strings.Contains(out, MarkerEnd) {
		t.Error("expected pincher marker block in fresh write")
	}
}

// Idempotent re-run replaces in place; second write must equal first.
func TestWarp_IdempotentRewrite(t *testing.T) {
	t.Parallel()
	first, _ := WarpTarget.WriteFn("", samplePolicy)
	second, action := WarpTarget.WriteFn(first, samplePolicy)
	if action != "updated" {
		t.Errorf("second action = %q, want updated", action)
	}
	if first != second {
		t.Errorf("non-idempotent rewrite — bytes diverge between runs")
	}
}

// User-edited content outside the marker block survives re-run.
func TestWarp_PreservesUnmanagedContent(t *testing.T) {
	t.Parallel()
	preamble := "# My WARP rules\n\nPrefer typed errors.\n\n"
	managed, _ := WarpTarget.WriteFn("", samplePolicy)
	existing := preamble + managed + "\n\n## Custom\n- Avoid panics in hot paths.\n"

	updated, _ := WarpTarget.WriteFn(existing, samplePolicy)
	if !strings.Contains(updated, "Prefer typed errors.") {
		t.Error("preamble lost on re-run")
	}
	if !strings.Contains(updated, "Avoid panics in hot paths.") {
		t.Error("postscript lost on re-run")
	}
}

func TestWarp_DetectFn_MatchesWARPMdOrWarpDir(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	if WarpTarget.DetectFn(cwd) {
		t.Errorf("empty dir should not detect Warp")
	}

	// WARP.md file case.
	if err := os.WriteFile(filepath.Join(cwd, "WARP.md"), []byte("# rules"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !WarpTarget.DetectFn(cwd) {
		t.Error("expected detect to fire on WARP.md presence")
	}

	// .warp/ directory case (separate temp dir).
	cwd2 := t.TempDir()
	if err := os.Mkdir(filepath.Join(cwd2, ".warp"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !WarpTarget.DetectFn(cwd2) {
		t.Error("expected detect to fire on .warp/ directory presence")
	}
}

func TestWarp_GlobalPathResolvesUnderHome(t *testing.T) {
	// Cannot t.Parallel() — withHome uses t.Setenv.
	home := t.TempDir()
	withHome(t, home)

	path, err := WarpTarget.PathFn(t.TempDir(), true)
	if err != nil {
		t.Fatalf("PathFn global: %v", err)
	}
	want := filepath.Join(home, ".warp", "WARP.md")
	if path != want {
		t.Errorf("global PathFn = %q, want %q", path, want)
	}
}
