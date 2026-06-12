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
// .claude/settings.json hooks so that `pincher hook-check` fires on
// Read/Grep/Glob/Task tool calls (PreToolUse, #627; Task carries the
// advise_route recruitment advisory, router-loop §A2) and on context
// compaction (PreCompact, precompact-hook — the ledger-aware
// compaction advisory). One install — `pincher init --target=claude` —
// wires both the MCP server registration AND the hook interception.
// Without this, agents running with the policy in CLAUDE.md still
// default to Read/Grep on hot paths; the runtime hook is what closes
// the gap.
//
// Idempotent: if `pincher hook-check` entries are already present for
// both events, the file is left untouched. Otherwise the missing
// entries are merged into the existing structure without clobbering
// other keys — re-running init on a pre-PreCompact install additively
// registers the new event.
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

	updated, action, err := mergePincherHook(existing, hookCheckCommand())
	if err != nil {
		return err
	}
	if action == "noop" {
		fmt.Fprintf(out, "pincher init [claude]: PreToolUse + PreCompact hooks already present in %s — no change\n", settingsPath)
		return nil
	}

	if dryRun {
		preview, _ := json.MarshalIndent(updated, "", "  ")
		fmt.Fprintf(out, "pincher init [claude]: would %s PreToolUse + PreCompact hooks in %s\n", hookPresentTense(action), settingsPath)
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
	fmt.Fprintf(out, "pincher init [claude]: %s PreToolUse + PreCompact hooks in %s\n", action, settingsPath)
	return nil
}

// hookCheckCommand builds the command line baked into the installed
// hook entry. When the install itself runs with PINCHER_DATA_DIR set,
// the hook must carry the same data dir explicitly: the hook's
// execution environment is not guaranteed to include the user's shell
// env, so a bare `pincher hook-check` would resolve the platform
// default dir, find no indexed projects there, and silently pass
// everything through — the hook looks installed but never fires.
func hookCheckCommand() string {
	if dir := os.Getenv("PINCHER_DATA_DIR"); dir != "" {
		return fmt.Sprintf("pincher hook-check --data-dir %q", dir)
	}
	return "pincher hook-check"
}

// mergePincherHook returns the updated settings JSON-shape and an
// action label ("created" for a fresh PreToolUse block, "added" for
// inserting into an existing PreToolUse list OR for additively
// registering the PreCompact event on an install that predates it, or
// "noop" when both entries are already present). Pure function — no
// I/O; the hook command line is injected by the caller (see
// hookCheckCommand — it bakes --data-dir in when PINCHER_DATA_DIR is
// set at install time).
//
// Two entries are managed (precompact-hook):
//   - PreToolUse matcher=Read|Grep|Glob|Task → `pincher hook-check`
//     (#627; Task added for the advise_route advisory, router-loop §A2)
//   - PreCompact (no matcher — fires on manual AND auto compaction)
//     → the same `pincher hook-check` command; event routing happens
//     inside the CLI via hook_event_name
//
// Each leg is independently idempotent: any existing entry under the
// event whose hook command contains "pincher hook-check" is treated as
// ours and left alone, even if the user tweaked matcher / shell args.
func mergePincherHook(settings map[string]any, command string) (map[string]any, string, error) {
	if settings == nil {
		settings = map[string]any{}
	}
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}

	changed := false
	legAdded := false
	matcherUpgraded := false
	action := "added"

	// Leg 1: PreToolUse (Read|Grep|Glob redirect advisories, #627;
	// Task carries the one-time advise_route recruitment advisory,
	// router-loop §A2 — without it on the matcher the hook never
	// observes subagent spawns and the per-session Task counter
	// stays empty).
	preToolUse, _ := hooks["PreToolUse"].([]any)
	if !hasPincherHookEntry(preToolUse) {
		if len(preToolUse) == 0 {
			action = "created"
		}
		preToolUse = append(preToolUse, map[string]any{
			"matcher": pincherHookMatcher,
			"hooks": []any{
				map[string]any{
					"type":    "command",
					"command": command,
				},
			},
		})
		hooks["PreToolUse"] = preToolUse
		changed = true
		legAdded = true
	} else if upgradePincherHookMatcher(preToolUse) {
		// Additive matcher migration: a pincher-owned entry still
		// carrying an exact PREVIOUS managed matcher value is upgraded
		// in place so existing installs start observing Task spawns
		// (advise_route, router-loop §A2) on the next re-run of init.
		// Entries whose matcher the user tweaked are left alone — only
		// the exact prior managed values are recognized as ours.
		hooks["PreToolUse"] = preToolUse
		matcherUpgraded = true
		changed = true
	}

	// Leg 2: PreCompact (ledger-aware compaction advisories,
	// precompact-hook). No matcher — PreCompact matchers select the
	// compaction trigger ("manual"/"auto") and the advisory applies to
	// both. Carries the same injected command line, so a
	// PINCHER_DATA_DIR install routes both legs at the right store.
	preCompact, _ := hooks["PreCompact"].([]any)
	if !hasPincherHookEntry(preCompact) {
		preCompact = append(preCompact, map[string]any{
			"hooks": []any{
				map[string]any{
					"type":    "command",
					"command": command,
				},
			},
		})
		hooks["PreCompact"] = preCompact
		changed = true
		legAdded = true
	}

	if !changed {
		return settings, "noop", nil
	}
	// A pure matcher migration (no leg added) reports "updated"; any
	// added leg keeps the established created/added labels.
	if matcherUpgraded && !legAdded {
		action = "updated"
	}
	settings["hooks"] = hooks
	return settings, action, nil
}

// pincherHookMatcher is the managed PreToolUse matcher value. History:
// "Read|Grep" (#627) → "Read|Grep|Glob" (#2006 era) → current (Task
// added for the advise_route recruitment advisory, router-loop §A2).
const pincherHookMatcher = "Read|Grep|Glob|Task"

// previousPincherHookMatchers are managed matcher values from earlier
// releases. An owned entry still carrying one of these EXACT values is
// safely ours-and-untweaked, so init may upgrade it in place; any
// other value is treated as a user tweak and left alone.
var previousPincherHookMatchers = map[string]bool{
	"Read|Grep":      true,
	"Read|Grep|Glob": true,
}

// upgradePincherHookMatcher rewrites the matcher of pincher-owned
// PreToolUse entries that still carry an exact previous managed value.
// Returns true when anything changed. Mutates the entry maps in place
// (they alias the caller's settings tree).
func upgradePincherHookMatcher(entries []any) bool {
	changed := false
	for _, raw := range entries {
		entry, _ := raw.(map[string]any)
		if entry == nil || !hasPincherHookEntry([]any{entry}) {
			continue
		}
		if m, _ := entry["matcher"].(string); previousPincherHookMatchers[m] {
			entry["matcher"] = pincherHookMatcher
			changed = true
		}
	}
	return changed
}

// hasPincherHookEntry reports whether any entry in a hook-event list
// carries a command containing "pincher hook-check" — the idempotency
// probe shared by both managed legs.
func hasPincherHookEntry(entries []any) bool {
	for _, raw := range entries {
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
				return true
			}
		}
	}
	return false
}

func installGooseHook(out io.Writer, projectDir string, dryRun bool) error {
	pluginRoot := filepath.Join(projectDir, ".agents", "plugins", "pincher")
	hooksPath := filepath.Join(pluginRoot, "hooks", "hooks.json")

	existing := map[string]any{}
	if raw, err := os.ReadFile(hooksPath); err == nil {
		if err := json.Unmarshal(raw, &existing); err != nil {
			return fmt.Errorf("goose hooks.json exists but is not valid JSON (%v) — fix or delete it before re-running init", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", hooksPath, err)
	}

	updated, action, err := mergeGooseHook(existing)
	if err != nil {
		return err
	}
	if action == "noop" {
		fmt.Fprintf(out, "pincher init [goose]: PreToolUse hook already present in %s — no change\n", hooksPath)
		return nil
	}

	if dryRun {
		preview, _ := json.MarshalIndent(updated, "", "  ")
		fmt.Fprintf(out, "pincher init [goose]: would %s Open Plugins hook extension in %s\n", hookPresentTense(action), pluginRoot)
		fmt.Fprintln(out, "--- hooks/hooks.json ---")
		fmt.Fprintln(out, string(preview))
		return nil
	}

	if err := os.MkdirAll(filepath.Join(pluginRoot, "hooks"), 0o755); err != nil {
		return fmt.Errorf("mkdir hooks: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(pluginRoot, "scripts"), 0o755); err != nil {
		return fmt.Errorf("mkdir scripts: %w", err)
	}
	if err := writeJSONFile(filepath.Join(pluginRoot, "plugin.json"), goosePluginManifest()); err != nil {
		return err
	}
	if err := writeJSONFile(hooksPath, updated); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, "scripts", "pincher-hook-check.sh"), []byte(gooseHookScript), 0o755); err != nil {
		return fmt.Errorf("write goose hook script: %w", err)
	}
	readmePath := filepath.Join(pluginRoot, "README.md")
	if _, err := os.Stat(readmePath); os.IsNotExist(err) {
		if err := os.WriteFile(readmePath, []byte(goosePluginREADME), 0o644); err != nil {
			return fmt.Errorf("write goose README: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("stat goose README: %w", err)
	}
	fmt.Fprintf(out, "pincher init [goose]: %s Open Plugins hook extension in %s\n", action, pluginRoot)
	return nil
}

func mergeGooseHook(settings map[string]any) (map[string]any, string, error) {
	if settings == nil {
		settings = map[string]any{}
	}
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	preToolUse, _ := hooks["PreToolUse"].([]any)

	gooseEntry := map[string]any{
		"matcher": "developer__shell|developer__text_editor",
		"hooks": []any{
			map[string]any{
				"type":    "command",
				"command": "${PLUGIN_ROOT}/scripts/pincher-hook-check.sh",
			},
		},
	}

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
			if c, _ := cmd["command"].(string); contains(c, "pincher-hook-check.sh") || contains(c, "pincher hook-check") {
				return settings, "noop", nil
			}
		}
	}

	action := "added"
	if len(preToolUse) == 0 {
		action = "created"
	}
	preToolUse = append(preToolUse, gooseEntry)
	hooks["PreToolUse"] = preToolUse
	settings["hooks"] = hooks
	return settings, action, nil
}

func writeJSONFile(path string, v any) error {
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func hookPresentTense(action string) string {
	switch action {
	case "created":
		return "create"
	case "added":
		return "add"
	case "updated":
		return "update"
	}
	return action
}

func goosePluginManifest() map[string]any {
	return map[string]any{
		"name":        "pincher",
		"version":     "0.1.0",
		"description": "Goose Open Plugins hooks that route developer tool calls through Pincher hook-check guardrails.",
	}
}

const gooseHookScript = `#!/usr/bin/env bash
# Goose Open Plugins hook bridge for Pincher.
set -euo pipefail

payload="$(cat)"
plugin_root="${PLUGIN_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"

{
  printf -- '---- PreToolUse @ %s ----\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  printf '%s\n' "$payload"
} >> "${plugin_root}/last-event.log" 2>/dev/null || true

printf '%s' "$payload" | pincher hook-check
`

const goosePluginREADME = `# Pincher Goose extension

Project-scoped Goose Open Plugins extension installed by ` + "`pincher init --target=goose`" + `.

It registers a Goose ` + "`PreToolUse`" + ` hook for ` + "`developer__shell|developer__text_editor`" + ` and forwards the raw Goose hook payload to ` + "`pincher hook-check`" + `.

Run from a repository root:

` + "```bash" + `
pincher init --target=goose
goose session
` + "```" + `

Goose discovers project plugins under ` + "`.agents/plugins/<name>/`" + `. The hook writes local debug payloads to ` + "`last-event.log`" + ` beside this file.
`

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
