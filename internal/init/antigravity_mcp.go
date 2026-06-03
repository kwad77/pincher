// SPDX-License-Identifier: MIT

package init

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// AntigravityMCPTarget registers pincher as an MCP server in Google
// Antigravity's MCP config (`~/.gemini/antigravity/mcp_config.json`).
//
// Companion to the rules-only `antigravity` target (#1765): that target
// tells the Antigravity agent ABOUT pincher; this one makes pincher's
// tools directly CALLABLE. Same `vscode` / `vscode-mcp` split — rules
// vs MCP-server registration.
//
// Antigravity's MCP config is global (per-user), not per-workspace, so
// this target is AlwaysGlobal — like `codex`. The MCP `init` tool skips
// AlwaysGlobal targets; write it via the
// `pincher init --target=antigravity-mcp` CLI.
//
// File format — the standard MCP `mcpServers` JSON shape (the file the
// IDE's "Manage MCP Servers → View raw config" opens):
//
//	{
//	  "mcpServers": {
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
// #1770.
var AntigravityMCPTarget = Target{
	Name:         "antigravity-mcp",
	Describe:     "Google Antigravity: ~/.gemini/antigravity/mcp_config.json — registers pincher as an MCP server the Antigravity agent can call",
	AlwaysGlobal: true, // Antigravity's MCP config is always global (~/.gemini/antigravity)
	PathFn:       ResolveAntigravityMCPConfigPath,
	DetectFn:     detectAntigravityMCP,
	WriteFn:      writeAntigravityMCPConfig,
}

// ResolveAntigravityMCPConfigPath returns the absolute path to
// Antigravity's MCP config: `~/.gemini/antigravity/mcp_config.json`.
// cwd / global are accepted for signature uniformity but ignored —
// Antigravity's MCP config is always global.
func ResolveAntigravityMCPConfigPath(cwd string, global bool) (string, error) {
	_ = cwd
	_ = global
	h, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home dir for antigravity mcp config: %w", err)
	}
	return filepath.Join(h, ".gemini", "antigravity", "mcp_config.json"), nil
}

// detectAntigravityMCP reports whether Antigravity is installed by
// checking for its global config directory `~/.gemini/antigravity`.
func detectAntigravityMCP(cwd string) bool {
	_ = cwd
	h, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(h, ".gemini", "antigravity"))
	return err == nil
}

// writeAntigravityMCPConfig merges pincher's `mcpServers.pincher` entry
// into the existing mcp_config.json, preserving every other server the
// user has registered. Mirrors writeVSCodeMCPConfig — Antigravity uses
// the `mcpServers` key (VS Code's mcp.json uses `servers`). The
// `policy` parameter is ignored: this is a structured-config artifact,
// not a rules file (the companion `antigravity` target writes the
// rules). Malformed existing JSON → refuse rather than clobber.
func writeAntigravityMCPConfig(existing, policy string) (string, string) {
	_ = policy
	pincherEntry := buildAntigravityMCPPincherEntry()

	if existing == "" {
		doc := map[string]any{
			"mcpServers": map[string]any{"pincher": pincherEntry},
		}
		out, _ := json.MarshalIndent(doc, "", "  ")
		return string(out) + "\n", "wrote"
	}

	var doc map[string]any
	if err := json.Unmarshal([]byte(existing), &doc); err != nil {
		fmt.Fprintln(os.Stderr, "pincher init [antigravity-mcp]: refusing to write — existing ~/.gemini/antigravity/mcp_config.json is not valid JSON.")
		fmt.Fprintln(os.Stderr, "  Fix the syntax error, then re-run `pincher init --target=antigravity-mcp`.")
		return existing, "error"
	}
	if doc == nil {
		doc = map[string]any{}
	}
	servers, _ := doc["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	_, hadPincher := servers["pincher"]
	servers["pincher"] = pincherEntry
	doc["mcpServers"] = servers

	out, _ := json.MarshalIndent(doc, "", "  ")
	if hadPincher {
		return string(out) + "\n", "updated"
	}
	return string(out) + "\n", "appended"
}

// buildAntigravityMCPPincherEntry returns the pincher server entry for
// mcp_config.json. Uses the supervised wrapper so disconnects auto-
// recover (#367/#368), and an Antigravity-specific PINCHER_DATA_DIR so
// concurrent agents (Codex CLI, VS Code Copilot, Antigravity, bare CLI)
// don't share a DB and drift on each other's session counters.
func buildAntigravityMCPPincherEntry() map[string]any {
	return map[string]any{
		"command": antigravityMCPPincherBinaryPath(),
		"args":    []string{"supervised"},
		"env": map[string]any{
			"PINCHER_DATA_DIR":              antigravityMCPPincherDataDir(),
			"PINCHER_AUTO_RESTART_ON_DRIFT": "1",
		},
	}
}

// antigravityMCPPincherBinaryPath prefers the currently-running binary
// (so a dev build registers itself), falling back to the bare
// "pincher" so a system-package install resolves via PATH.
func antigravityMCPPincherBinaryPath() string {
	if exe, err := os.Executable(); err == nil {
		return exe
	}
	return "pincher"
}

// antigravityMCPPincherDataDir computes an Antigravity-specific
// PINCHER_DATA_DIR — per-target isolation mirroring the vscode / codex
// helpers so multiple agents running pincher don't share one DB.
func antigravityMCPPincherDataDir() string {
	if runtime.GOOS == "windows" {
		if appData := os.Getenv("APPDATA"); appData != "" {
			return filepath.Join(appData, "pincherMCP", "antigravity")
		}
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(h, "Library", "Application Support", "pincherMCP", "antigravity")
	}
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "pincherMCP", "antigravity")
	}
	return filepath.Join(h, ".local", "share", "pincherMCP", "antigravity")
}
