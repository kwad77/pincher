// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// #2020: an MCP-only client on the default PINCHER_TOOLSET=core surface
// previously could not read OR write ADRs — `adr` was absent from
// coreToolset, and the documented `batch` escape hatch dispatches the
// read-only sub-tool set only (batchAllowedSubTools: no writers). The
// loop methodology treats the ADR store as first-class cross-session
// memory, so `adr` now rides the core advertisement. This test pins the
// fix end-to-end over a REAL MCP session (in-memory transports, full
// initialize handshake): the default advertisement carries `adr`, and a
// set → get → list round-trip works without env opt-outs or HTTP.
func TestADR_CoreMode_MCPRoundTrip(t *testing.T) {
	t.Setenv("PINCHER_TOOLSET", "core") // opt-in core surface (#2054 made full the default)
	t.Setenv("PINCHER_SCHEMA_STYLE", "")
	t.Setenv("PINCHER_ROUTER", "off")
	srv, store, _ := newTestServer(t)
	srv.sessionID = "adr-core-2020"
	if err := store.UpsertProject(db.Project{ID: "adr-core-2020", Path: "/tmp/adr-core-2020", Name: "adr-core-2020"}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}

	cs, cleanup := connectInMemoryClient(t, srv, nil)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// tools/list: adr must be advertised on the default surface.
	listRes, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	sawADR := false
	for _, tool := range listRes.Tools {
		if tool.Name == "adr" {
			sawADR = true
		}
	}
	if !sawADR {
		t.Fatalf("default (core) tools/list does not advertise adr — got %d tools", len(listRes.Tools))
	}
	if want := len(coreToolset); len(listRes.Tools) != want {
		t.Errorf("default tools/list advertises %d tools, want %d (coreToolset)", len(listRes.Tools), want)
	}

	// tools/call round-trip: set → get → list, all through the session.
	call := func(args map[string]any) *mcp.CallToolResult {
		t.Helper()
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "adr", Arguments: args})
		if err != nil {
			t.Fatalf("tools/call adr %v: %v", args, err)
		}
		return res
	}

	setRes := call(map[string]any{"action": "set", "key": "STACK", "value": "Go+SQLite", "project": "adr-core-2020"})
	if setRes.IsError {
		t.Fatalf("adr set errored: %s", textOf(t, setRes))
	}

	getRes := call(map[string]any{"action": "get", "key": "STACK", "project": "adr-core-2020"})
	if getRes.IsError {
		t.Fatalf("adr get errored: %s", textOf(t, getRes))
	}
	if got := decode(t, getRes); got["value"] != "Go+SQLite" {
		t.Errorf("adr get round-trip: value = %v, want %q", got["value"], "Go+SQLite")
	}

	listADRRes := call(map[string]any{"action": "list", "project": "adr-core-2020"})
	if listADRRes.IsError {
		t.Fatalf("adr list errored: %s", textOf(t, listADRRes))
	}
	body := decode(t, listADRRes)
	entries, _ := body["entries"].(map[string]any)
	if entries["STACK"] != "Go+SQLite" {
		t.Errorf("adr list round-trip: entries = %v, want STACK=Go+SQLite", body["entries"])
	}
}

// TestCoreToolset_CountAndADRMembership pins the #2020 surface shape as
// plain numbers so a future membership edit is a conscious decision:
// 11 tools on the router-absent default advertisement, 13 with the
// detected-state router pair, and adr is a member.
func TestCoreToolset_CountAndADRMembership(t *testing.T) {
	t.Parallel()
	if !coreToolset["adr"] {
		t.Error("adr is not in coreToolset — #2020 regressed: MCP-only core-mode clients lose their only ADR path (batch dispatches read-only sub-tools only)")
	}
	if got := len(coreToolset); got != 11 {
		t.Errorf("coreToolset has %d tools, want 11 (#2020) — update the count consciously alongside the schema-weight golden", got)
	}
	if got := len(coreToolset) + len(routerConditionalTools); got != 13 {
		t.Errorf("router-present default surface has %d tools, want 13 (#2020)", got)
	}
}
