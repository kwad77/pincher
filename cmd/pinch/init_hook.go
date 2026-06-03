// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// installClaudeHook writes (or merges into) the project's
// .claude/settings.json PreToolUse hook so that `pincher hook-check`
// fires on Read/Grep tool calls (#627). One install — `pincher init
// --target=claude` — wires both the MCP server registration AND the
// hook interception. Without this, agents running with the policy
// in CLAUDE.md still default to Read/Grep on hot paths; the runtime
// hook is what closes the gap.
//
// Idempotent: if a `pincher hook-check` PreToolUse entry is already
// present, the file is left untouched. Otherwise the hook entry is
// merged into the existing structure without clobbering other keys.
func installClaudeHook(out io.Writer, projectDir string, dryRun bool) error {
	settingsPath := filepath.Join(projectDir, ".claude", "settings.json")

	existing := map[string]any{}
	if raw, err := os.ReadFile(settingsPath); err == nil {
		if err := json.Unmarshal(raw, &existing); err != nil {
			return fmt.Errorf(".claude/settings.json exists but is not valid JSON (%v) — fix or delete it before re-running init", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", settingsPath, err)
	}

	updated, action, err := mergePincherHook(existing)
	if err != nil {
		return err
	}
	if action == "noop" {
		fmt.Fprintf(out, "pincher init [claude]: PreToolUse hook already present in %s — no change\n", settingsPath)
		return nil
	}

	if dryRun {
		preview, _ := json.MarshalIndent(updated, "", "  ")
		fmt.Fprintf(out, "pincher init [claude]: would %s PreToolUse hook in %s\n", action, settingsPath)
		fmt.Fprintln(out, "--- new file content ---")
		fmt.Fprintln(out, string(preview))
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(settingsPath), err)
	}
	body, err := json.MarshalIndent(updated, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if err := os.WriteFile(settingsPath, append(body, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", settingsPath, err)
	}
	fmt.Fprintf(out, "pincher init [claude]: %s PreToolUse hook in %s\n", action, settingsPath)
	return nil
}

// mergePincherHook returns the updated settings JSON-shape and an
// action label ("created" for a fresh hooks block, "added" for
// inserting the entry into an existing PreToolUse list, or "noop"
// when the entry is already present). Pure function — no I/O.
func mergePincherHook(settings map[string]any) (map[string]any, string, error) {
	if settings == nil {
		settings = map[string]any{}
	}
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	preToolUse, _ := hooks["PreToolUse"].([]any)

	pincherEntry := map[string]any{
		"matcher": "Read|Grep",
		"hooks": []any{
			map[string]any{
				"type":    "command",
				"command": "pincher hook-check",
			},
		},
	}

	// Idempotency: any existing PreToolUse entry with matcher=Read|Grep
	// AND a hook command containing "pincher hook-check" is treated as
	// the pincher hook — leave it alone, even if the user has tweaked
	// the matcher / shell args.
	for _, raw := range preToolUse {
		entry, _ := raw.(map[string]any)
		if entry == nil {
			continue
		}
		entryHooks, _ := entry["hooks"].([]any)
		for _, h := range entryHooks {
			cmd, _ := h.(map[string]any)
			if cmd == nil {
				continue
			}
			if c, _ := cmd["command"].(string); contains(c, "pincher hook-check") {
				return settings, "noop", nil
			}
		}
	}

	action := "added"
	if len(preToolUse) == 0 {
		action = "created"
	}
	preToolUse = append(preToolUse, pincherEntry)
	hooks["PreToolUse"] = preToolUse
	settings["hooks"] = hooks
	return settings, action, nil
}

// contains is a small substring check used by mergePincherHook so
// that idempotency tolerates variations like `pincher hook-check
// --debug` or `/usr/local/bin/pincher hook-check`.
func contains(haystack, needle string) bool {
	if needle == "" {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
