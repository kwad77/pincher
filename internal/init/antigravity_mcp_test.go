// SPDX-License-Identifier: MIT

package init

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #1770: the antigravity-mcp target registers pincher as an MCP server
// in Antigravity's global mcp_config.json so the agent can call
// pincher's tools (the rules-only `antigravity` target only describes
// pincher; it doesn't make it callable).

func TestAntigravityMCPTarget_PathFn(t *testing.T) {
	dir := t.TempDir()
	withHome(t, dir)
	got, err := AntigravityMCPTarget.PathFn("/any/cwd", false)
	if err != nil {
		t.Fatalf("PathFn: %v", err)
	}
	want := filepath.Join(dir, ".gemini", "antigravity", "mcp_config.json")
	if got != want {
		t.Errorf("PathFn = %q, want %q", got, want)
	}
}

// AlwaysGlobal must be set — Antigravity's MCP config is per-user, not
// per-workspace, so the MCP `init` tool skips this target (the same
// treatment codex gets).
func TestAntigravityMCPTarget_AlwaysGlobal(t *testing.T) {
	t.Parallel()
	if !AntigravityMCPTarget.AlwaysGlobal {
		t.Error("antigravity-mcp must be AlwaysGlobal — its mcp_config.json is global")
	}
}

func TestAntigravityMCPTarget_DetectFn(t *testing.T) {
	// Positive: ~/.gemini/antigravity/ present.
	hit := t.TempDir()
	if err := os.MkdirAll(filepath.Join(hit, ".gemini", "antigravity"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	withHome(t, hit)
	if !detectAntigravityMCP("") {
		t.Error("DetectFn returned false despite ~/.gemini/antigravity/ being present")
	}
}

func TestAntigravityMCPTarget_DetectFn_NegativeNoDir(t *testing.T) {
	withHome(t, t.TempDir())
	if detectAntigravityMCP("") {
		t.Error("DetectFn returned true with no ~/.gemini/antigravity/ directory")
	}
}

// Fresh write: an mcpServers.pincher entry with command/args/env.
func TestAntigravityMCPConfig_FreshWrite(t *testing.T) {
	t.Parallel()
	out, action := writeAntigravityMCPConfig("", "")
	if action != "wrote" {
		t.Errorf("action = %q, want wrote", action)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	servers, _ := doc["mcpServers"].(map[string]any)
	if servers == nil {
		t.Fatal("output missing mcpServers object")
	}
	pincher, _ := servers["pincher"].(map[string]any)
	if pincher == nil {
		t.Fatal("output missing mcpServers.pincher")
	}
	if pincher["command"] == nil || pincher["command"] == "" {
		t.Error("pincher entry missing command")
	}
	args, _ := pincher["args"].([]any)
	if len(args) != 1 || args[0] != "supervised" {
		t.Errorf("pincher args = %v, want [supervised]", pincher["args"])
	}
	env, _ := pincher["env"].(map[string]any)
	if env == nil || env["PINCHER_AUTO_RESTART_ON_DRIFT"] != "1" {
		t.Errorf("pincher env missing PINCHER_AUTO_RESTART_ON_DRIFT=1; got %v", pincher["env"])
	}
}

// Merge: an existing config with another server must keep that server
// and gain pincher.
func TestAntigravityMCPConfig_MergesIntoExisting(t *testing.T) {
	t.Parallel()
	existing := `{
  "mcpServers": {
    "github": { "command": "gh-mcp", "args": [] }
  }
}`
	out, action := writeAntigravityMCPConfig(existing, "")
	if action != "appended" {
		t.Errorf("action = %q, want appended", action)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
	servers, _ := doc["mcpServers"].(map[string]any)
	if _, ok := servers["github"]; !ok {
		t.Error("merge dropped the user's pre-existing 'github' server")
	}
	if _, ok := servers["pincher"]; !ok {
		t.Error("merge did not add the 'pincher' server")
	}
}

// Re-running over a config that already has pincher reports "updated",
// not "appended", and stays valid + idempotent.
func TestAntigravityMCPConfig_Idempotent(t *testing.T) {
	t.Parallel()
	first, _ := writeAntigravityMCPConfig("", "")
	second, action := writeAntigravityMCPConfig(first, "")
	if action != "updated" {
		t.Errorf("second-write action = %q, want updated", action)
	}
	if first != second {
		t.Errorf("non-idempotent rewrite:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

// Malformed existing JSON: refuse rather than clobber.
func TestAntigravityMCPConfig_RefusesMalformed(t *testing.T) {
	t.Parallel()
	bad := `{ "mcpServers": { "github": `
	out, action := writeAntigravityMCPConfig(bad, "")
	if action != "error" {
		t.Errorf("action = %q, want error on malformed JSON", action)
	}
	if out != bad {
		t.Error("malformed-JSON refusal must return the existing content unchanged, not a rewrite")
	}
	if strings.Contains(out, "pincher") {
		t.Error("refused write must not have injected the pincher entry")
	}
}
