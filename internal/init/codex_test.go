// SPDX-License-Identifier: MIT

package init

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexTarget_RegisteredInAllTargets(t *testing.T) {
	for _, name := range TargetNames() {
		if name == "codex" {
			return
		}
	}
	t.Fatal("codex target not found in TargetNames()")
}

func TestResolveCodexConfigPath_DefaultHome(t *testing.T) {
	t.Setenv("CODEX_HOME", "")
	path, err := ResolveCodexConfigPath("", true)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !strings.HasSuffix(filepath.ToSlash(path), ".codex/config.toml") {
		t.Errorf("expected path to end with .codex/config.toml, got %s", path)
	}
}

func TestResolveCodexConfigPath_HonorsCODEX_HOME(t *testing.T) {
	t.Setenv("CODEX_HOME", "/custom/codex/dir")
	path, err := ResolveCodexConfigPath("", true)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want := filepath.Join("/custom/codex/dir", "config.toml")
	if path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
}

func TestWriteCodexMCPConfig_EmptyExisting_Writes(t *testing.T) {
	out, action := writeCodexMCPConfig("", "ignored policy text")
	if action != "wrote" {
		t.Errorf("action = %q, want %q", action, "wrote")
	}
	for _, want := range []string{
		codexMarkerStart,
		codexMarkerEnd,
		"[mcp_servers.pincher]",
		`args = ["supervised"]`,
		"[mcp_servers.pincher.env]",
		"PINCHER_DATA_DIR",
		`PINCHER_AUTO_RESTART_ON_DRIFT = "1"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestWriteCodexMCPConfig_AppendsToExistingWithoutMarkers(t *testing.T) {
	existing := "[mcp_servers.other]\ncommand = \"foo\"\nargs = []\n"
	out, action := writeCodexMCPConfig(existing, "")
	if action != "appended" {
		t.Errorf("action = %q, want %q", action, "appended")
	}
	if !strings.Contains(out, "mcp_servers.other") {
		t.Error("existing other-server entry was lost")
	}
	if !strings.Contains(out, codexMarkerStart) || !strings.Contains(out, "[mcp_servers.pincher]") {
		t.Error("pincher block not appended")
	}
}

func TestWriteCodexMCPConfig_RefusesUnmanagedExistingPincher(t *testing.T) {
	// User has hand-written a pincher entry without our markers —
	// possibly with custom subtables (e.g. tool approval modes).
	// Refuse rather than produce duplicate TOML.
	existing := "[mcp_servers.pincher]\ncommand = \"/custom/path\"\nargs = []\n\n" +
		"[mcp_servers.pincher.tools.search]\napproval_mode = \"approve\"\n"
	out, action := writeCodexMCPConfig(existing, "")
	if action != CodexActionSkipped {
		t.Errorf("action = %q, want %q", action, CodexActionSkipped)
	}
	if out != existing {
		t.Error("existing content was modified despite refusal")
	}
}

func TestWriteCodexMCPConfig_UpdatesInPlace(t *testing.T) {
	// Pre-existing config with an old pincher block (e.g. older binary
	// path). Re-running init should replace the marker block, leaving
	// surrounding content untouched.
	existing := "[mcp_servers.foo]\ncommand = \"foo\"\n\n" +
		codexMarkerStart + "\n" +
		"[mcp_servers.pincher]\n" +
		"command = \"/old/path/pincher\"\n" +
		"args = [\"oldarg\"]\n" +
		codexMarkerEnd + "\n\n" +
		"[mcp_servers.bar]\ncommand = \"bar\"\n"

	out, action := writeCodexMCPConfig(existing, "")
	if action != "updated" {
		t.Errorf("action = %q, want %q", action, "updated")
	}
	// Surrounding content preserved.
	if !strings.Contains(out, "mcp_servers.foo") {
		t.Error("foo entry before block was lost")
	}
	if !strings.Contains(out, "mcp_servers.bar") {
		t.Error("bar entry after block was lost")
	}
	// Old block content gone, new content present.
	if strings.Contains(out, "/old/path/pincher") {
		t.Error("old command was not replaced")
	}
	if !strings.Contains(out, `args = ["supervised"]`) {
		t.Error("new args missing")
	}
}

func TestWriteCodexMCPConfig_IdempotentOnReRun(t *testing.T) {
	first, _ := writeCodexMCPConfig("", "")
	second, action := writeCodexMCPConfig(first, "")
	if action != "updated" {
		t.Errorf("second run action = %q, want %q", action, "updated")
	}
	if first != second {
		t.Errorf("re-run produced different output:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestCodexPincherDataDir_NotEmptyOnSupportedPlatforms(t *testing.T) {
	got := codexPincherDataDir()
	if got == "" {
		t.Skip("home dir unavailable in test environment — acceptable empty fallback")
	}
	if !strings.Contains(got, "codex") {
		t.Errorf("data dir %q should contain 'codex' segment", got)
	}
}
