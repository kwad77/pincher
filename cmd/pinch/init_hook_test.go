// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// #627: pincher init --target=claude writes a PreToolUse hook to
// .claude/settings.json so that `pincher hook-check` fires on Read /
// Grep tool calls. Idempotent re-runs leave the file unchanged.
// Existing settings keys are preserved.

func TestMergePincherHook_FromEmpty_CreatesFreshBlock(t *testing.T) {
	updated, action, err := mergePincherHook(nil)
	if err != nil {
		t.Fatalf("mergePincherHook: %v", err)
	}
	if action != "created" {
		t.Errorf("action = %q, want created", action)
	}
	hooks, _ := updated["hooks"].(map[string]any)
	preToolUse, _ := hooks["PreToolUse"].([]any)
	if len(preToolUse) != 1 {
		t.Fatalf("PreToolUse len = %d, want 1", len(preToolUse))
	}
	entry, _ := preToolUse[0].(map[string]any)
	if entry["matcher"] != "Read|Grep" {
		t.Errorf("matcher = %v, want Read|Grep", entry["matcher"])
	}
	hookList, _ := entry["hooks"].([]any)
	first, _ := hookList[0].(map[string]any)
	if first["command"] != "pincher hook-check" {
		t.Errorf("command = %v, want pincher hook-check", first["command"])
	}
}

func TestMergePincherHook_PreservesExistingKeys(t *testing.T) {
	in := map[string]any{
		"theme":     "dark",
		"telemetry": false,
		"hooks": map[string]any{
			"OtherEvent": []any{map[string]any{"matcher": "Bash"}},
		},
	}
	updated, action, err := mergePincherHook(in)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	// Existing hooks block but no PreToolUse list yet → label is
	// "created" (we created the PreToolUse subkey from empty), not
	// "added" (which means appended to a non-empty PreToolUse list).
	if action != "created" {
		t.Errorf("action = %q, want created (PreToolUse subkey created from empty)", action)
	}
	if updated["theme"] != "dark" {
		t.Errorf("theme key clobbered: %v", updated["theme"])
	}
	if updated["telemetry"] != false {
		t.Errorf("telemetry key clobbered: %v", updated["telemetry"])
	}
	hooks, _ := updated["hooks"].(map[string]any)
	if hooks["OtherEvent"] == nil {
		t.Error("OtherEvent hook clobbered")
	}
	if hooks["PreToolUse"] == nil {
		t.Error("PreToolUse hook missing")
	}
}

func TestMergePincherHook_Idempotent(t *testing.T) {
	// First merge installs the hook.
	first, _, err := mergePincherHook(nil)
	if err != nil {
		t.Fatalf("first merge: %v", err)
	}
	// Second merge should detect the existing entry and noop.
	_, action, err := mergePincherHook(first)
	if err != nil {
		t.Fatalf("second merge: %v", err)
	}
	if action != "noop" {
		t.Errorf("re-running merge should noop; got %q", action)
	}
}

func TestMergePincherHook_DetectsCustomCommand(t *testing.T) {
	// User may have `pincher hook-check --debug` or
	// `/usr/local/bin/pincher hook-check`. Idempotency tolerates these
	// on the PreToolUse leg: the entry is left untouched. The install
	// predates PreCompact (precompact-hook), so that registration is
	// still added — action is "added", not "noop".
	in := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{
					"matcher": "Read",
					"hooks": []any{
						map[string]any{"type": "command", "command": "/usr/local/bin/pincher hook-check --debug"},
					},
				},
			},
		},
	}
	updated, action, err := mergePincherHook(in)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if action != "added" {
		t.Errorf("legacy install should gain the PreCompact leg; got %q", action)
	}
	hooks, _ := updated["hooks"].(map[string]any)
	pre, _ := hooks["PreToolUse"].([]any)
	if len(pre) != 1 {
		t.Fatalf("custom PreToolUse entry must be left alone; len = %d, want 1", len(pre))
	}
	entry, _ := pre[0].(map[string]any)
	if entry["matcher"] != "Read" {
		t.Errorf("custom matcher clobbered: %v", entry["matcher"])
	}
	if hooks["PreCompact"] == nil {
		t.Error("PreCompact registration missing after legacy upgrade")
	}
}

// precompact-hook: init registers the PreCompact event alongside
// PreToolUse so `pincher hook-check` receives compaction events and
// can emit the ledger-aware advisory.

func TestMergePincherHook_RegistersPreCompact(t *testing.T) {
	updated, action, err := mergePincherHook(nil)
	if err != nil {
		t.Fatalf("mergePincherHook: %v", err)
	}
	if action != "created" {
		t.Errorf("action = %q, want created", action)
	}
	hooks, _ := updated["hooks"].(map[string]any)
	preCompact, _ := hooks["PreCompact"].([]any)
	if len(preCompact) != 1 {
		t.Fatalf("PreCompact len = %d, want 1", len(preCompact))
	}
	entry, _ := preCompact[0].(map[string]any)
	if _, hasMatcher := entry["matcher"]; hasMatcher {
		t.Errorf("PreCompact entry should carry no matcher (fires on manual AND auto); got %v", entry["matcher"])
	}
	hookList, _ := entry["hooks"].([]any)
	if len(hookList) != 1 {
		t.Fatalf("PreCompact hooks len = %d, want 1", len(hookList))
	}
	first, _ := hookList[0].(map[string]any)
	if first["command"] != "pincher hook-check" {
		t.Errorf("command = %v, want pincher hook-check", first["command"])
	}
}

func TestMergePincherHook_PreCompactIdempotent(t *testing.T) {
	// A user-tweaked PreCompact entry still counts as ours.
	in := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{
					"matcher": "Read|Grep",
					"hooks": []any{
						map[string]any{"type": "command", "command": "pincher hook-check"},
					},
				},
			},
			"PreCompact": []any{
				map[string]any{
					"matcher": "auto",
					"hooks": []any{
						map[string]any{"type": "command", "command": "/opt/pincher hook-check --debug"},
					},
				},
			},
		},
	}
	_, action, err := mergePincherHook(in)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if action != "noop" {
		t.Errorf("both legs present should noop; got %q", action)
	}
}

func TestMergePincherHook_PreservesForeignPreCompactEntries(t *testing.T) {
	// Someone else's PreCompact hook must be preserved, ours appended.
	in := map[string]any{
		"hooks": map[string]any{
			"PreCompact": []any{
				map[string]any{
					"hooks": []any{
						map[string]any{"type": "command", "command": "my-transcript-backup.sh"},
					},
				},
			},
		},
	}
	updated, _, err := mergePincherHook(in)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	hooks, _ := updated["hooks"].(map[string]any)
	preCompact, _ := hooks["PreCompact"].([]any)
	if len(preCompact) != 2 {
		t.Fatalf("PreCompact len = %d, want 2 (preserved foreign + appended pincher)", len(preCompact))
	}
}

func TestInstallClaudeHook_WritesPreCompactRegistration(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	if err := installClaudeHook(&buf, dir, false); err != nil {
		t.Fatalf("installClaudeHook: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("read written settings: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	hooks, _ := got["hooks"].(map[string]any)
	if hooks["PreToolUse"] == nil {
		t.Error("PreToolUse registration missing")
	}
	if hooks["PreCompact"] == nil {
		t.Error("PreCompact registration missing")
	}
}

func TestInstallClaudeHook_LegacyInstallGainsPreCompact(t *testing.T) {
	// Re-running init on a pre-PreCompact install upgrades it
	// additively: PreToolUse untouched, PreCompact appended.
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	legacy := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{
					"matcher": "Read|Grep",
					"hooks": []any{
						map[string]any{"type": "command", "command": "pincher hook-check"},
					},
				},
			},
		},
	}
	body, _ := json.MarshalIndent(legacy, "", "  ")
	if err := os.WriteFile(settingsPath, body, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var buf bytes.Buffer
	if err := installClaudeHook(&buf, dir, false); err != nil {
		t.Fatalf("installClaudeHook: %v", err)
	}
	updated, _ := os.ReadFile(settingsPath)
	var got map[string]any
	if err := json.Unmarshal(updated, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	hooks, _ := got["hooks"].(map[string]any)
	pre, _ := hooks["PreToolUse"].([]any)
	if len(pre) != 1 {
		t.Errorf("PreToolUse must be untouched; len = %d, want 1", len(pre))
	}
	if hooks["PreCompact"] == nil {
		t.Error("PreCompact registration not added to legacy install")
	}
	if !strings.Contains(buf.String(), "added") {
		t.Errorf("output should report the additive upgrade; got %q", buf.String())
	}
}

func TestMergePincherHook_ExistingPreToolUseGetsAppendTo(t *testing.T) {
	// User has another PreToolUse entry for a different matcher; the
	// pincher hook should be appended, not replace it.
	in := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{
					"matcher": "Bash",
					"hooks": []any{
						map[string]any{"type": "command", "command": "shellcheck"},
					},
				},
			},
		},
	}
	updated, action, err := mergePincherHook(in)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if action != "added" {
		t.Errorf("action = %q, want added", action)
	}
	hooks, _ := updated["hooks"].(map[string]any)
	pre, _ := hooks["PreToolUse"].([]any)
	if len(pre) != 2 {
		t.Errorf("PreToolUse len = %d, want 2 (preserved Bash + appended pincher)", len(pre))
	}
}

func TestInstallClaudeHook_FreshFileCreated(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	if err := installClaudeHook(&buf, dir, false); err != nil {
		t.Fatalf("installClaudeHook: %v", err)
	}
	settingsPath := filepath.Join(dir, ".claude", "settings.json")
	body, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read written settings: %v", err)
	}
	if !strings.Contains(string(body), "pincher hook-check") {
		t.Errorf("written file should contain hook command; got %s", body)
	}
	if !strings.Contains(string(body), `"matcher": "Read|Grep"`) {
		t.Errorf("written file should contain the matcher; got %s", body)
	}
	if !strings.Contains(buf.String(), "created") {
		t.Errorf("output should mention 'created'; got %q", buf.String())
	}
}

func TestInstallClaudeHook_ExistingSettingsPreserved(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	preExisting := map[string]any{
		"theme": "high-contrast",
		"hooks": map[string]any{
			"OtherEvent": []any{map[string]any{"matcher": "Edit"}},
		},
	}
	body, _ := json.MarshalIndent(preExisting, "", "  ")
	if err := os.WriteFile(settingsPath, body, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var buf bytes.Buffer
	if err := installClaudeHook(&buf, dir, false); err != nil {
		t.Fatalf("installClaudeHook: %v", err)
	}
	updated, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(updated, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["theme"] != "high-contrast" {
		t.Errorf("pre-existing theme key clobbered: %v", got["theme"])
	}
	hooks, _ := got["hooks"].(map[string]any)
	if hooks["OtherEvent"] == nil {
		t.Error("OtherEvent hook lost")
	}
	if hooks["PreToolUse"] == nil {
		t.Error("PreToolUse hook not installed")
	}
}

func TestInstallClaudeHook_IdempotentReRun(t *testing.T) {
	dir := t.TempDir()
	if err := installClaudeHook(io.Discard, dir, false); err != nil {
		t.Fatalf("first install: %v", err)
	}
	first, _ := os.ReadFile(filepath.Join(dir, ".claude", "settings.json"))

	var buf bytes.Buffer
	if err := installClaudeHook(&buf, dir, false); err != nil {
		t.Fatalf("second install: %v", err)
	}
	if !strings.Contains(buf.String(), "no change") {
		t.Errorf("second install should report no change; got %q", buf.String())
	}
	second, _ := os.ReadFile(filepath.Join(dir, ".claude", "settings.json"))
	if string(first) != string(second) {
		t.Error("idempotent re-run modified the file")
	}
}

func TestInstallClaudeHook_DryRunMakesNoChanges(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	if err := installClaudeHook(&buf, dir, true); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Errorf("dry run should not create file; stat err = %v", err)
	}
	if !strings.Contains(buf.String(), "would") {
		t.Errorf("dry run output should say 'would'; got %q", buf.String())
	}
}

func TestMergeGooseHook_FromEmpty_CreatesOpenPluginHook(t *testing.T) {
	updated, action, err := mergeGooseHook(nil)
	if err != nil {
		t.Fatalf("mergeGooseHook: %v", err)
	}
	if action != "created" {
		t.Errorf("action = %q, want created", action)
	}
	hooks, _ := updated["hooks"].(map[string]any)
	preToolUse, _ := hooks["PreToolUse"].([]any)
	if len(preToolUse) != 1 {
		t.Fatalf("PreToolUse len = %d, want 1", len(preToolUse))
	}
	entry, _ := preToolUse[0].(map[string]any)
	if entry["matcher"] != "developer__shell|developer__text_editor" {
		t.Errorf("matcher = %v, want developer__shell|developer__text_editor", entry["matcher"])
	}
	hookList, _ := entry["hooks"].([]any)
	first, _ := hookList[0].(map[string]any)
	if first["command"] != "${PLUGIN_ROOT}/scripts/pincher-hook-check.sh" {
		t.Errorf("command = %v, want plugin-root script", first["command"])
	}
}

func TestInstallGooseHook_FreshPluginCreated(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	if err := installGooseHook(&buf, dir, false); err != nil {
		t.Fatalf("installGooseHook: %v", err)
	}
	root := filepath.Join(dir, ".agents", "plugins", "pincher")
	for _, rel := range []string{"plugin.json", filepath.Join("hooks", "hooks.json"), filepath.Join("scripts", "pincher-hook-check.sh"), "README.md"} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Fatalf("expected %s: %v", rel, err)
		}
	}
	hooks, err := os.ReadFile(filepath.Join(root, "hooks", "hooks.json"))
	if err != nil {
		t.Fatalf("read hooks: %v", err)
	}
	if !strings.Contains(string(hooks), "developer__shell|developer__text_editor") {
		t.Errorf("hooks.json missing Goose developer matcher: %s", hooks)
	}
	script := filepath.Join(root, "scripts", "pincher-hook-check.sh")
	info, err := os.Stat(script)
	if err != nil {
		t.Fatalf("stat script: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode()&0o100 == 0 {
		t.Errorf("script should be owner-executable; mode=%v", info.Mode())
	}
	if !strings.Contains(buf.String(), "created") {
		t.Errorf("output should mention created; got %q", buf.String())
	}
}

func TestInstallGooseHook_IdempotentReRun(t *testing.T) {
	dir := t.TempDir()
	if err := installGooseHook(io.Discard, dir, false); err != nil {
		t.Fatalf("first install: %v", err)
	}
	first, _ := os.ReadFile(filepath.Join(dir, ".agents", "plugins", "pincher", "hooks", "hooks.json"))

	var buf bytes.Buffer
	if err := installGooseHook(&buf, dir, false); err != nil {
		t.Fatalf("second install: %v", err)
	}
	if !strings.Contains(buf.String(), "no change") {
		t.Errorf("second install should report no change; got %q", buf.String())
	}
	second, _ := os.ReadFile(filepath.Join(dir, ".agents", "plugins", "pincher", "hooks", "hooks.json"))
	if string(first) != string(second) {
		t.Error("idempotent re-run modified hooks.json")
	}
}
