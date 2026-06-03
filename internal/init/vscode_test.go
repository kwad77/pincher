// SPDX-License-Identifier: MIT

package init

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #658 wave-1.5: VS Code / GitHub Copilot init target. Writes the
// documented `.github/copilot-instructions.md` at the repo root, the
// file VS Code Copilot Chat, JetBrains Copilot, and GitHub.com all
// consume. The shared MergePolicyBlock writer handles marker-block
// idempotency without modification.

func TestVSCode_FreshWriteCreatesCopilotInstructions(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	path, err := VSCodeTarget.PathFn(cwd, false)
	if err != nil {
		t.Fatalf("PathFn: %v", err)
	}
	want := filepath.Join(cwd, ".github", "copilot-instructions.md")
	if path != want {
		t.Errorf("PathFn = %q, want %q", path, want)
	}

	out, action := VSCodeTarget.WriteFn("", samplePolicy)
	if action != "wrote" {
		t.Errorf("action = %q, want wrote", action)
	}
	if !strings.Contains(out, MarkerStart) || !strings.Contains(out, MarkerEnd) {
		t.Error("expected pincher marker block in fresh write")
	}
}

func TestVSCode_IdempotentRewrite(t *testing.T) {
	t.Parallel()
	first, _ := VSCodeTarget.WriteFn("", samplePolicy)
	second, action := VSCodeTarget.WriteFn(first, samplePolicy)
	if action != "updated" {
		t.Errorf("second action = %q, want updated", action)
	}
	if first != second {
		t.Errorf("non-idempotent rewrite — bytes diverge between runs")
	}
}

func TestVSCode_PreservesUnmanagedContent(t *testing.T) {
	t.Parallel()
	preamble := "# Repo Copilot rules\n\nFavor minimal diffs.\n\n"
	managed, _ := VSCodeTarget.WriteFn("", samplePolicy)
	existing := preamble + managed + "\n\n## Custom\n- No magic numbers.\n"

	updated, _ := VSCodeTarget.WriteFn(existing, samplePolicy)
	if !strings.Contains(updated, "Favor minimal diffs.") {
		t.Error("preamble lost on re-run")
	}
	if !strings.Contains(updated, "No magic numbers.") {
		t.Error("postscript lost on re-run")
	}
}

// Detection fires on any of three markers: the rules file itself, a
// .vscode/ dir, or a .github/instructions/ dir. Each in isolation.
func TestVSCode_DetectFn_MultipleMarkers(t *testing.T) {
	t.Parallel()

	t.Run("empty dir does not detect", func(t *testing.T) {
		t.Parallel()
		if VSCodeTarget.DetectFn(t.TempDir()) {
			t.Error("empty dir should not detect")
		}
	})

	t.Run("copilot-instructions.md alone detects", func(t *testing.T) {
		t.Parallel()
		cwd := t.TempDir()
		if err := os.MkdirAll(filepath.Join(cwd, ".github"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cwd, ".github", "copilot-instructions.md"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if !VSCodeTarget.DetectFn(cwd) {
			t.Error("expected detect on copilot-instructions.md")
		}
	})

	t.Run(".vscode/ alone detects", func(t *testing.T) {
		t.Parallel()
		cwd := t.TempDir()
		if err := os.Mkdir(filepath.Join(cwd, ".vscode"), 0o755); err != nil {
			t.Fatal(err)
		}
		if !VSCodeTarget.DetectFn(cwd) {
			t.Error("expected detect on .vscode/ dir")
		}
	})

	t.Run(".github/instructions/ alone detects", func(t *testing.T) {
		t.Parallel()
		cwd := t.TempDir()
		if err := os.MkdirAll(filepath.Join(cwd, ".github", "instructions"), 0o755); err != nil {
			t.Fatal(err)
		}
		if !VSCodeTarget.DetectFn(cwd) {
			t.Error("expected detect on .github/instructions/ dir")
		}
	})
}

func TestVSCode_GlobalRejected(t *testing.T) {
	t.Parallel()
	if _, err := VSCodeTarget.PathFn(t.TempDir(), true); err == nil {
		t.Error("vscode --global should error (copilot-instructions.md is per-repo by design)")
	}
}
