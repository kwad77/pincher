// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile is a small test helper: writes content to path, creating parents.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// goBinInstall fabricates a temp home with a pincher binary under <home>/go/bin
// and forces isGoInstallDir onto the ~/go/bin default branch by clearing
// GOBIN / GOPATH. Returns (exePath, home).
func goBinInstall(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", "")
	exe := filepath.Join(home, "go", "bin", "pincher")
	writeFile(t, exe, "binary")
	return exe, home
}

// requireContains fails unless got contains every want substring.
func requireContains(t *testing.T, got string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("advisory missing %q\n  got: %s", w, got)
		}
	}
}

func TestMCPLaunchPathAdvisory_FiresWhenInteractiveOnly_1939(t *testing.T) {
	exe, home := goBinInstall(t)
	// PATH export lives only in the interactive rc — the exact trap.
	writeFile(t, filepath.Join(home, ".zshrc"), "export PATH=\"$HOME/go/bin:$PATH\"\n")

	got := mcpLaunchPathAdvisoryFor(exe, home)
	if got == "" {
		t.Fatal("expected advisory when go/bin is only on the interactive (.zshrc) PATH")
	}
	requireContains(t, got, "interactive shell rc", "~/.zshenv", "claude mcp add pincher", "#1939")
}

func TestMCPLaunchPathAdvisory_FiresWhenAbsentEverywhere_1939(t *testing.T) {
	exe, home := goBinInstall(t)
	// No init file references go/bin at all.

	got := mcpLaunchPathAdvisoryFor(exe, home)
	if got == "" {
		t.Fatal("expected advisory when go/bin is on no shell init PATH")
	}
	requireContains(t, got, "no login / non-interactive shell init file", "~/.zshenv", "#1939")
}

func TestMCPLaunchPathAdvisory_SilentWhenZshenvCovers_1939(t *testing.T) {
	exe, home := goBinInstall(t)
	// Covered by a non-interactive init file → the MCP host can launch it.
	writeFile(t, filepath.Join(home, ".zshenv"), "export PATH=\"$HOME/go/bin:$PATH\"\n")
	// Interactive rc may also have it; must not matter.
	writeFile(t, filepath.Join(home, ".zshrc"), "export PATH=\"$HOME/go/bin:$PATH\"\n")

	if got := mcpLaunchPathAdvisoryFor(exe, home); got != "" {
		t.Errorf("expected silence when ~/.zshenv exports go/bin; got: %s", got)
	}
}

func TestMCPLaunchPathAdvisory_SilentForNonGoInstall_1939(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", "")
	// A release / Homebrew-style install dir, not under any go-install path.
	exe := filepath.Join(home, "usr", "local", "bin", "pincher")
	writeFile(t, exe, "binary")

	if got := mcpLaunchPathAdvisoryFor(exe, home); got != "" {
		t.Errorf("expected silence for non-go-install location; got: %s", got)
	}
}

func TestMCPLaunchPathAdvisory_HonorsGOBIN_1939(t *testing.T) {
	home := t.TempDir()
	custom := filepath.Join(home, "custom-gobin")
	t.Setenv("GOBIN", custom)
	t.Setenv("GOPATH", "")
	exe := filepath.Join(custom, "pincher")
	writeFile(t, exe, "binary")
	// Interactive-only PATH entry naming the explicit dir (no $HOME tail match).
	writeFile(t, filepath.Join(home, ".bashrc"), "export PATH=\""+custom+":$PATH\"\n")

	got := mcpLaunchPathAdvisoryFor(exe, home)
	if got == "" {
		t.Fatal("expected advisory when $GOBIN dir is only on the interactive PATH")
	}
	requireContains(t, got, "interactive shell rc", custom, "#1939")
}

func TestMCPLaunchPathAdvisory_IgnoresCommentedExport_1939(t *testing.T) {
	exe, home := goBinInstall(t)
	// A commented-out export must not count as covering the dir.
	writeFile(t, filepath.Join(home, ".zshenv"), "# export PATH=\"$HOME/go/bin:$PATH\"\n")

	got := mcpLaunchPathAdvisoryFor(exe, home)
	if got == "" {
		t.Fatal("expected advisory — commented .zshenv export should not count as coverage")
	}
	requireContains(t, got, "no login / non-interactive shell init file", "#1939")
}
