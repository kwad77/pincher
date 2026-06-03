// SPDX-License-Identifier: MIT

package server

import (
	"sort"
	"testing"
)

// #558 phase 3: cross-surface parity. Every user-facing CLI subcommand
// that exposes data (as opposed to a pure ops command — `web`,
// `supervised`, `update`, `project`, `health-check`) must have an
// equivalent MCP tool. The HTTP surface is auto-derived from
// s.handlers via /v1/<tool>, so MCP and HTTP parity is enforced
// elsewhere (TestOpenAPI_ParityWithRegisteredHandlers).
//
// Before this gate, `pincher doctor`/`rebuild-fts`/`self-test` were
// CLI-only — agents and dashboards had to shell out. Phase 2 added
// MCP handlers; this test stops them silently regressing.
//
// The ops-only carve-outs are listed explicitly so adding a new
// pure-operation CLI doesn't accidentally trip the gate, and adding a
// new data CLI without an MCP equivalent does.
func TestCLISurface_HasMCPParity(t *testing.T) {
	t.Parallel()
	// User-facing CLI subcommands that surface data → MCP tool name.
	// CLI uses kebab-case; MCP uses snake_case.
	expected := map[string]string{
		"index":       "index",
		"doctor":      "doctor",
		"rebuild-fts": "rebuild_fts",
		"self-test":   "self_test",
		"stats":       "stats",
		"init":        "init",
	}

	// Pure-ops CLIs that legitimately have no MCP equivalent. Listed
	// here so the test reviewer can audit the carve-out at a glance.
	// Adding a new entry requires a comment justifying it.
	opsOnly := map[string]string{
		"web":          "spawns the HTTP dashboard process — operational",
		"supervised":   "wraps the MCP server with auto-restart — operational",
		"update":       "self-update binary download — operational",
		"project":      "registers/lists projects on disk — would duplicate `list`",
		"health-check": "Docker/k8s liveness probe; returns exit code, no payload",
	}

	srv, _, _ := newTestServer(t)

	// Every expected CLI subcommand must have a registered MCP tool.
	var missing []string
	for cli, mcpName := range expected {
		if _, ok := srv.handlers[mcpName]; !ok {
			missing = append(missing, cli+" → "+mcpName)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("CLI subcommands missing MCP equivalent: %v\n"+
			"Every user-facing CLI command must be reachable via MCP "+
			"(and therefore HTTP via /v1/<tool>) so dashboards and agents "+
			"don't have to shell out. If the new CLI is intentionally "+
			"ops-only, add it to the opsOnly map with a justification.",
			missing)
	}

	// Surface the ops-only set explicitly in test output for the
	// auditor — keeps the carve-out visible across PRs.
	for cli := range opsOnly {
		if _, ok := srv.handlers[cli]; ok {
			t.Errorf("%q is in opsOnly but ALSO has an MCP handler — pick one", cli)
		}
	}
}
