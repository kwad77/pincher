// SPDX-License-Identifier: MIT

package init

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// codexMarker* bracket the pincher-managed block in Codex's TOML
// config. TOML uses `#` for line comments, so HTML markers (used by
// the markdown-targeted writers like CLAUDE.md / Cursor MDC) won't
// parse — these are TOML-friendly comment lines that the parser
// treats as no-op comments around the real `[mcp_servers.pincher]`
// table.
const (
	codexMarkerStart = "# >>> pincher:start (managed by `pincher init --target=codex`) >>>"
	codexMarkerEnd   = "# <<< pincher:end <<<"
)

// CodexTarget registers pincher as an MCP server in OpenAI Codex's
// `~/.codex/config.toml`. Unlike rules-file targets (claude, cursor,
// windsurf, aider, continue), this one writes a TOML block — Codex
// finds pincher via its MCP config, not via a free-text policy file.
//
// The block uses `pincher supervised` as the command (so disconnects
// auto-recover via the supervisor — see #367/#368) and a Codex-
// specific PINCHER_DATA_DIR (so each agent CLI has its own DB and
// concurrent CLIs can't drift on each other — see Path A in the
// session design notes).
var CodexTarget = Target{
	Name:         "codex",
	Describe:     "OpenAI Codex CLI: ~/.codex/config.toml — adds [mcp_servers.pincher] with `pincher supervised` and per-target PINCHER_DATA_DIR",
	AlwaysGlobal: true, // Codex's MCP config is always global (~/.codex)
	PathFn:       ResolveCodexConfigPath,
	DetectFn:     detectCodex,
	WriteFn:      writeCodexMCPConfig,
}

// ResolveCodexConfigPath returns the absolute path to Codex's
// `config.toml`. Honors $CODEX_HOME (the documented override) before
// falling back to the platform default of `~/.codex`.
//
// cwd and global are accepted for signature uniformity with other
// targets but ignored — Codex's config is always global.
func ResolveCodexConfigPath(cwd string, global bool) (string, error) {
	_ = cwd
	_ = global
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve user home dir for codex config: %w", err)
		}
		home = filepath.Join(h, ".codex")
	}
	return filepath.Join(home, "config.toml"), nil
}

// detectCodex reports whether Codex is installed by checking for the
// presence of its config dir (`~/.codex` or `$CODEX_HOME`). The
// directory is created by Codex on first run, so its existence is a
// reliable proxy for "user has Codex installed and has run it at
// least once."
func detectCodex(cwd string) bool {
	path, err := ResolveCodexConfigPath(cwd, true)
	if err != nil {
		return false
	}
	if _, err := os.Stat(filepath.Dir(path)); err == nil {
		return true
	}
	return false
}

// CodexActionSkipped is the action string returned when an existing
// un-managed `[mcp_servers.pincher]` entry was found and we refused to
// touch it. Caller is expected to surface this to the user; the file
// is unchanged.
const CodexActionSkipped = "skipped (existing un-managed [mcp_servers.pincher])"

// writeCodexMCPConfig produces the merged config.toml content. The
// `policy` parameter is intentionally ignored — Codex doesn't have a
// rules-file convention this target can write to (AGENTS.md is
// project-scoped and not consistently adopted yet). If/when that
// convention solidifies we can add a sibling target.
//
// Behavior:
//   - empty `existing` → emit just the block (caller's writeAtomic
//     creates the file)
//   - existing with both markers → replace the block in place
//   - existing has `[mcp_servers.pincher]` but NOT our markers →
//     refuse with CodexActionSkipped + a stderr message. Otherwise
//     we'd produce duplicate-table TOML, which Codex rejects.
//     Operator has to either remove their old block or wrap it with
//     our markers to opt into managed updates.
//   - existing without any pincher entry → append the block at end
//   - existing with malformed markers (only one) → append fresh; we
//     don't attempt automated recovery (more often a user-edit issue
//     than a tool-corruption issue, mirroring MergePolicyBlock's stance)
func writeCodexMCPConfig(existing, policy string) (string, string) {
	_ = policy // see func doc
	block := buildCodexBlock()

	if existing == "" {
		return block + "\n", "wrote"
	}

	startIdx := strings.Index(existing, codexMarkerStart)
	endIdx := strings.Index(existing, codexMarkerEnd)
	if startIdx >= 0 && endIdx > startIdx {
		afterIdx := endIdx + len(codexMarkerEnd)
		before := strings.TrimRight(existing[:startIdx], "\n")
		after := strings.TrimLeft(existing[afterIdx:], "\n")

		var b strings.Builder
		b.WriteString(before)
		if before != "" {
			b.WriteString("\n\n")
		}
		b.WriteString(block)
		if after != "" {
			b.WriteString("\n\n")
			b.WriteString(after)
		} else {
			b.WriteString("\n")
		}
		return b.String(), "updated"
	}

	// Detect an un-managed pincher entry and refuse rather than
	// produce a duplicate-table TOML. Side-effect stderr message
	// gives the operator the exact lines to copy if they want to
	// opt into managed updates.
	if strings.Contains(existing, "[mcp_servers.pincher]") {
		fmt.Fprintln(os.Stderr, "pincher init [codex]: refusing to write — existing [mcp_servers.pincher] entry detected in config.toml without our managed-block markers.")
		fmt.Fprintln(os.Stderr, "  Adding a second entry would produce duplicate-table TOML and break Codex.")
		fmt.Fprintln(os.Stderr, "  Either:")
		fmt.Fprintln(os.Stderr, "    (a) remove the existing [mcp_servers.pincher] block manually, then re-run `pincher init --target=codex`,")
		fmt.Fprintln(os.Stderr, "    (b) wrap your existing block with these markers to opt into managed updates:")
		fmt.Fprintln(os.Stderr, "          "+codexMarkerStart)
		fmt.Fprintln(os.Stderr, "          [mcp_servers.pincher]")
		fmt.Fprintln(os.Stderr, "          ... (your existing keys) ...")
		fmt.Fprintln(os.Stderr, "          "+codexMarkerEnd)
		return existing, CodexActionSkipped
	}

	return strings.TrimRight(existing, "\n") + "\n\n" + block + "\n", "appended"
}

// buildCodexBlock assembles the marker-wrapped TOML block. Quoted
// strings use Go's %q which produces TOML-compatible quoting (TOML
// uses the same double-quote string-escape rules as JSON for the
// characters we emit — backslash, quote, control chars). Embedded
// backslashes in Windows paths get correctly escaped.
func buildCodexBlock() string {
	binaryPath := codexPincherBinaryPath()
	dataDir := codexPincherDataDir()

	var b strings.Builder
	b.WriteString(codexMarkerStart + "\n")
	b.WriteString("# Re-run `pincher init --target=codex` to refresh this block.\n")
	b.WriteString("# `command` uses the supervised wrapper so dropped MCP sessions\n")
	b.WriteString("# auto-respawn without manual /mcp; PINCHER_DATA_DIR isolates\n")
	b.WriteString("# Codex's pincher DB from other agent CLIs (Claude Code, etc.).\n")
	b.WriteString("[mcp_servers.pincher]\n")
	b.WriteString(fmt.Sprintf("command = %q\n", binaryPath))
	b.WriteString(`args = ["supervised"]` + "\n")
	b.WriteString("\n")
	b.WriteString("[mcp_servers.pincher.env]\n")
	b.WriteString(fmt.Sprintf("PINCHER_DATA_DIR = %q\n", dataDir))
	b.WriteString(`PINCHER_AUTO_RESTART_ON_DRIFT = "1"` + "\n")
	b.WriteString(codexMarkerEnd)
	return b.String()
}

// codexPincherBinaryPath returns the path to the pincher binary the
// Codex MCP entry should invoke. Best-effort: prefer os.Executable
// (the binary running `pincher init`), fall back to the literal
// "pincher" so PATH-resolved invocations still work for users who
// installed via Homebrew, scoop, or a system package manager.
func codexPincherBinaryPath() string {
	if exe, err := os.Executable(); err == nil {
		return exe
	}
	return "pincher"
}

// codexPincherDataDir computes a Codex-specific PINCHER_DATA_DIR.
// Mirrors `db.DataDir`'s platform logic but appends a "codex" segment
// so this directory is fully owned by Codex and won't collide with
// the default DB used by Claude Code or bare-CLI invocations.
//
// Empty return is acceptable: the block emits "" and Codex will fall
// back to pincher's default. We don't import internal/db to avoid the
// circular-package risk; the redundancy is small and stable.
func codexPincherDataDir() string {
	if runtime.GOOS == "windows" {
		if appData := os.Getenv("APPDATA"); appData != "" {
			return filepath.Join(appData, "pincherMCP", "codex")
		}
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(h, "Library", "Application Support", "pincherMCP", "codex")
	}
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "pincherMCP", "codex")
	}
	return filepath.Join(h, ".local", "share", "pincherMCP", "codex")
}
