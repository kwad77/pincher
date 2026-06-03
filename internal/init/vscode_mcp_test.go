// SPDX-License-Identifier: MIT

package init

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// vscode-mcp registers pincher as an MCP server inside VS Code
// Copilot Chat. Distinct from `vscode` (which writes the project-rules
// file at .github/copilot-instructions.md): this one writes
// .vscode/mcp.json so Copilot's tool surface picks up pincher's
// tools directly.

func TestVSCodeMCP_FreshWriteCreatesMcpJson(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	path, err := VSCodeMCPTarget.PathFn(cwd, false)
	if err != nil {
		t.Fatalf("PathFn: %v", err)
	}
	if got, want := path, filepath.Join(cwd, ".vscode", "mcp.json"); got != want {
		t.Errorf("PathFn = %q, want %q", got, want)
	}

	out, action := VSCodeMCPTarget.WriteFn("", samplePolicy)
	if action != "wrote" {
		t.Errorf("action = %q, want wrote", action)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v\n--- output ---\n%s", err, out)
	}
	servers, _ := doc["servers"].(map[string]any)
	if servers == nil {
		t.Fatal("expected `servers` map in output")
	}
	pincher, _ := servers["pincher"].(map[string]any)
	if pincher == nil {
		t.Fatal("expected `servers.pincher` entry")
	}
	args, _ := pincher["args"].([]any)
	if len(args) == 0 || args[0] != "supervised" {
		t.Errorf("expected args=[\"supervised\"]; got %v", args)
	}
	envMap, _ := pincher["env"].(map[string]any)
	if envMap == nil {
		t.Fatal("expected `servers.pincher.env`")
	}
	if envMap["PINCHER_AUTO_RESTART_ON_DRIFT"] != "1" {
		t.Errorf("expected PINCHER_AUTO_RESTART_ON_DRIFT=\"1\"; got %v", envMap["PINCHER_AUTO_RESTART_ON_DRIFT"])
	}
}

// User-edited mcp.json that already has other MCP servers must
// survive — only `servers.pincher` should be touched.
func TestVSCodeMCP_PreservesUnrelatedServers(t *testing.T) {
	t.Parallel()
	prior := `{
  "servers": {
    "other-server": {
      "command": "other",
      "args": ["serve"]
    }
  }
}`
	out, action := VSCodeMCPTarget.WriteFn(prior, samplePolicy)
	if action != "appended" {
		t.Errorf("action = %q, want appended", action)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v\n--- output ---\n%s", err, out)
	}
	servers, _ := doc["servers"].(map[string]any)
	if servers["other-server"] == nil {
		t.Error("unrelated other-server entry should be preserved")
	}
	if servers["pincher"] == nil {
		t.Error("pincher entry should be added")
	}
}

// Idempotent re-run: rewriting an existing pincher entry yields
// stable bytes (modulo nothing the test can predict re: command path).
// We check the action is `updated` and `servers.pincher` exists.
func TestVSCodeMCP_IdempotentRewrite(t *testing.T) {
	t.Parallel()
	first, _ := VSCodeMCPTarget.WriteFn("", samplePolicy)
	second, action := VSCodeMCPTarget.WriteFn(first, samplePolicy)
	if action != "updated" {
		t.Errorf("second action = %q, want updated", action)
	}
	if first != second {
		t.Errorf("non-idempotent rewrite — bytes diverge between runs")
	}
}

// Malformed JSON input: refuse to write, return action=error.
func TestVSCodeMCP_MalformedJSONReturnsError(t *testing.T) {
	t.Parallel()
	out, action := VSCodeMCPTarget.WriteFn("not valid json {", samplePolicy)
	if action != "error" {
		t.Errorf("action = %q, want error on malformed input", action)
	}
	if out != "not valid json {" {
		t.Errorf("malformed input should be returned unchanged; got %q", out)
	}
}

// Detection fires on .vscode/ presence at the project root.
func TestVSCodeMCP_DetectFn(t *testing.T) {
	t.Parallel()
	t.Run("empty dir does not detect", func(t *testing.T) {
		t.Parallel()
		if VSCodeMCPTarget.DetectFn(t.TempDir()) {
			t.Error("empty dir should not detect")
		}
	})
	t.Run(".vscode/ detects", func(t *testing.T) {
		t.Parallel()
		cwd := t.TempDir()
		if err := os.Mkdir(filepath.Join(cwd, ".vscode"), 0o755); err != nil {
			t.Fatal(err)
		}
		if !VSCodeMCPTarget.DetectFn(cwd) {
			t.Error("expected detect on .vscode/ dir")
		}
	})
}

func TestVSCodeMCP_GlobalRejected(t *testing.T) {
	t.Parallel()
	if _, err := VSCodeMCPTarget.PathFn(t.TempDir(), true); err == nil {
		t.Error("vscode-mcp --global should error (mcp.json is project-scoped)")
	}
}

// Smoke test that the registry-shape gate accepts the new target.
func TestVSCodeMCP_InRegistry(t *testing.T) {
	t.Parallel()
	if _, ok := FindTarget("vscode-mcp"); !ok {
		t.Fatal("vscode-mcp not found in registry")
	}
	names := TargetNames()
	if !strings.Contains(strings.Join(names, ","), "vscode-mcp") {
		t.Fatal("vscode-mcp missing from TargetNames")
	}
}
