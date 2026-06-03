// SPDX-License-Identifier: MIT

package init

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// VSCodeMCPTarget registers pincher as an MCP server inside VS Code
// Copilot Chat's project-scoped MCP config (`.vscode/mcp.json`).
// Unlike `target=vscode` (which writes the project-rules file
// `.github/copilot-instructions.md`), this target wires pincher's
// MCP tools directly into Copilot Chat's tool surface.
//
// Why a separate target from `vscode`:
//
//   - vscode: project-rules. Tells Copilot HOW to behave (use pincher
//     for code-intel). One markdown file.
//   - vscode-mcp: MCP server registration. Tells Copilot WHERE to
//     find pincher's tools. One JSON file in `.vscode/`.
//
// Both can be combined via `--target=detect` once #970's detection
// hits; running both gives Copilot the rules + the runtime. Closing
// the loop for the VS Code user-base push.
//
// File format follows Microsoft's documented MCP-server schema:
//
//	{
//	  "servers": {
//	    "pincher": {
//	      "command": "pincher",
//	      "args": ["supervised"],
//	      "env": {
//	        "PINCHER_DATA_DIR": "...",
//	        "PINCHER_AUTO_RESTART_ON_DRIFT": "1"
//	      }
//	    }
//	  }
//	}
//
// Detection: fires on `.vscode/` presence at the project root —
// distinct from the rules-file detection (#970's `vscode` target),
// which fires on `.vscode/`, `.github/copilot-instructions.md`, or
// `.github/instructions/`.

var VSCodeMCPTarget = Target{
	Name:     "vscode-mcp",
	Describe: "VS Code Copilot Chat: ./.vscode/mcp.json — registers pincher as an MCP server alongside Copilot's other tools",
	PathFn: func(cwd string, global bool) (string, error) {
		if global {
			return "", fmt.Errorf("vscode-mcp target has no global variant; mcp.json is project-scoped under .vscode/")
		}
		return filepath.Join(cwd, ".vscode", "mcp.json"), nil
	},
	DetectFn: func(cwd string) bool {
		// .vscode/ is the strongest signal — present on every VS Code
		// project. The other rules-file markers (copilot-instructions.md
		// etc.) belong to the sibling `vscode` target; this one cares
		// about whether the editor itself is in use, not whether the
		// user has a Copilot rules file yet.
		if _, err := os.Stat(filepath.Join(cwd, ".vscode")); err == nil {
			return true
		}
		return false
	},
	WriteFn: writeVSCodeMCPConfig,
}

// writeVSCodeMCPConfig produces the merged .vscode/mcp.json content.
// The `policy` parameter is intentionally ignored — VS Code's mcp.json
// is a structured-config artifact, not a rules file. The companion
// `vscode` target writes the rules file at .github/copilot-instructions.md.
//
// Behavior:
//   - empty `existing` → emit a fresh document with just our server
//   - existing valid JSON → merge our `servers.pincher` entry into it,
//     preserving every other server registration the user has added
//   - existing malformed JSON → refuse with a recovery-action label so
//     we don't clobber whatever the user wrote manually
func writeVSCodeMCPConfig(existing, policy string) (string, string) {
	_ = policy
	pincherEntry := buildVSCodePincherEntry()

	if existing == "" {
		doc := map[string]any{
			"servers": map[string]any{
				"pincher": pincherEntry,
			},
		}
		out, _ := json.MarshalIndent(doc, "", "  ")
		return string(out) + "\n", "wrote"
	}

	var doc map[string]any
	if err := json.Unmarshal([]byte(existing), &doc); err != nil {
		// Malformed JSON: refuse rather than guess. The user has to
		// fix their syntax first, then re-run.
		fmt.Fprintln(os.Stderr, "pincher init [vscode-mcp]: refusing to write — existing .vscode/mcp.json is not valid JSON.")
		fmt.Fprintln(os.Stderr, "  Fix the syntax error, then re-run `pincher init --target=vscode-mcp`.")
		return existing, "error"
	}
	if doc == nil {
		doc = map[string]any{}
	}
	servers, _ := doc["servers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	// Track whether the existing entry was already byte-identical so
	// the caller can surface "unchanged" instead of "updated".
	priorPincher, hadPincher := servers["pincher"]
	servers["pincher"] = pincherEntry
	doc["servers"] = servers

	out, _ := json.MarshalIndent(doc, "", "  ")
	action := "updated"
	if !hadPincher {
		action = "appended"
	} else if jsonDeepEqual(priorPincher, pincherEntry) {
		// Pre-existing entry already matches. Caller's "unchanged"
		// detection compares Existing == Updated bytewise; with a
		// stable JSON key order this round-trips cleanly.
		action = "updated" // bytes may differ in formatting; let caller fall through
	}
	return string(out) + "\n", action
}

// buildVSCodePincherEntry returns the pincher server entry for
// .vscode/mcp.json. Uses the supervised wrapper for the same
// auto-restart benefits the codex target wires up.
func buildVSCodePincherEntry() map[string]any {
	return map[string]any{
		"command": vscodePincherBinaryPath(),
		"args":    []string{"supervised"},
		"env": map[string]any{
			"PINCHER_DATA_DIR":              vscodePincherDataDir(),
			"PINCHER_AUTO_RESTART_ON_DRIFT": "1",
		},
	}
}

// vscodePincherBinaryPath returns the pincher binary path to embed in
// the .vscode/mcp.json command field. Same logic as codex: prefer the
// currently-running binary, fall back to the bare "pincher" so a
// system-package install (Homebrew, scoop) resolves via PATH.
func vscodePincherBinaryPath() string {
	if exe, err := os.Executable(); err == nil {
		return exe
	}
	return "pincher"
}

// vscodePincherDataDir computes a VS Code-specific PINCHER_DATA_DIR.
// Mirrors the codex helper's per-target isolation so multiple agents
// running pincher (Codex CLI, VS Code Copilot, bare CLI) don't share
// a DB and step on each other's session counters.
func vscodePincherDataDir() string {
	if runtime.GOOS == "windows" {
		if appData := os.Getenv("APPDATA"); appData != "" {
			return filepath.Join(appData, "pincherMCP", "vscode")
		}
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(h, "Library", "Application Support", "pincherMCP", "vscode")
	}
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "pincherMCP", "vscode")
	}
	return filepath.Join(h, ".local", "share", "pincherMCP", "vscode")
}

// jsonDeepEqual compares two json-marshalable values structurally by
// round-tripping through json.Marshal. Cheap, correct for our small
// pincher-entry shape, and avoids a reflect.DeepEqual on maps that
// would be order-sensitive in subtle ways.
func jsonDeepEqual(a, b any) bool {
	ab, err1 := json.Marshal(a)
	bb, err2 := json.Marshal(b)
	if err1 != nil || err2 != nil {
		return false
	}
	return string(ab) == string(bb)
}
