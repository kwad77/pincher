// SPDX-License-Identifier: MIT

package server

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// updateGolden controls whether TestToolContract_GoldenFile rewrites
// testdata/tool-contract.json instead of asserting against it. Run with
// `go test ./internal/server/ -update-tool-contract` after a deliberate
// schema change. The diff IS the rationale — review it in the PR.
var updateGolden = flag.Bool("update-tool-contract", false,
	"rewrite testdata/tool-contract.json instead of asserting against it")

// TestToolContract_GoldenFile is the post-1.0 schema-stability gate. It
// snapshots every registered MCP tool's name, description, and InputSchema
// to a single committed file. Any rename, removal, or schema change to a
// public tool surfaces as a deliberate, reviewable diff at PR time.
//
// SemVer interpretation (per RELEASING.md):
//   - Adding a new tool / new field on an existing tool = MINOR bump.
//   - Removing or renaming a tool / field = MAJOR bump.
//
// A failing diff here means: either bump the appropriate version segment
// when the change ships, or revisit the change. A non-diffable rewrite
// (whitespace, comment shuffles inside the schema string) should produce
// no diff because the comparison happens on the parsed JSON tree.
//
// The golden pins the FULL/rich surface explicitly (env set below, not
// inherited): the contract documents the complete tool surface — every
// registered tool with its full description — independent of the
// shipped default, which flipped to core/lean in v1.6 (#2005). The
// default-mode advertisement has its own gate:
// TestToolContract_DefaultSurface below plus the toolset tests in
// schema_diet_test.go.
func TestToolContract_GoldenFile(t *testing.T) {
	t.Setenv("PINCHER_TOOLSET", "full")
	t.Setenv("PINCHER_SCHEMA_STYLE", "rich")
	// Router-loop B5: s.tools is detection-independent — models/route
	// are REGISTERED in both router states (only the advertisement is
	// conditional), so the contract golden documents them like every
	// other tool. Pinned to the absent state anyway so the golden can
	// never depend on the machine running the test.
	t.Setenv("PINCHER_ROUTER", "off")
	srv, _, _ := newTestServer(t)

	// Build a stable, parsed-and-re-encoded snapshot. We intentionally
	// re-marshal the InputSchema so whitespace differences in the source
	// file don't show up as diff churn. Only structural changes do.
	type toolEntry struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		InputSchema json.RawMessage `json:"input_schema"`
	}
	names := make([]string, 0, len(srv.tools))
	for name := range srv.tools {
		names = append(names, name)
	}
	sort.Strings(names)

	entries := make([]toolEntry, 0, len(names))
	for _, name := range names {
		tool := srv.tools[name]
		// InputSchema is `any` upstream; round-trip through JSON to get a
		// stable parsed tree regardless of whether it was set as a
		// json.RawMessage literal or a Go map.
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatalf("marshal tool %q InputSchema: %v", name, err)
		}
		var schema any
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatalf("tool %q has malformed InputSchema JSON: %v", name, err)
		}
		// Re-marshal sorted (json.Marshal sorts map keys for deterministic
		// output, which is exactly the byte-stable property we need).
		canonical, err := json.MarshalIndent(schema, "  ", "  ")
		if err != nil {
			t.Fatalf("re-marshal tool %q schema: %v", name, err)
		}
		entries = append(entries, toolEntry{
			Name:        name,
			Description: tool.Description,
			InputSchema: canonical,
		})
	}

	got, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		t.Fatalf("marshal contract: %v", err)
	}
	got = append(got, '\n')

	goldenPath := filepath.Join("testdata", "tool-contract.json")

	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("rewrote %s (%d tools, %d bytes)", goldenPath, len(entries), len(got))
		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden %s: %v\n  Run `go test ./internal/server/ -update-tool-contract` to create it.", goldenPath, err)
	}
	// Normalize CRLF → LF: git on Windows checks files out with CRLF by
	// default (autocrlf=true), but we emit LF. The contract is logical
	// equality, not byte-identical; line-ending differences would create
	// false-positive failures on Windows runners.
	got = bytes.ReplaceAll(got, []byte("\r\n"), []byte("\n"))
	want = bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))
	if string(got) != string(want) {
		t.Errorf("tool contract diverged from %s.\n"+
			"If the change is intentional and matches the SemVer policy in RELEASING.md, run:\n"+
			"  go test ./internal/server/ -update-tool-contract\n"+
			"and commit the diff alongside the version bump.\n\n"+
			"first divergent characters: ...%s",
			goldenPath, firstDiff(string(got), string(want)))
	}
}

// TestToolContract_DefaultSurface pins what a tools/list client gets
// with NO env set — the shipped default, core/lean since v1.6 (#2005:
// full/rich at scale = 1.44M tokens vs 475k core+lean at identical
// accuracy). The advertisement is exactly coreToolset, every
// description is the lean transform of its rich counterpart, and the
// full surface stays registered underneath (HTTP /v1/<tool>, OpenAPI,
// `batch` dispatch). Restoring the old default is an explicit opt-out:
// PINCHER_TOOLSET=full PINCHER_SCHEMA_STYLE=rich.
func TestToolContract_DefaultSurface(t *testing.T) {
	t.Setenv("PINCHER_TOOLSET", "")
	t.Setenv("PINCHER_SCHEMA_STYLE", "")
	// Router-absent state (router-loop B5, plan §A6): the shipped
	// default advertisement must be byte-identical to the pre-router
	// surface — exactly coreToolset, no models/route, zero delta. The
	// detected-state twin is TestToolContract_DefaultSurface_RouterPresent.
	t.Setenv("PINCHER_ROUTER", "off")
	srv, _, _ := newTestServer(t)

	// Advertisement == coreToolset, both directions.
	for name := range srv.mcpVisible {
		if !coreToolset[name] {
			t.Errorf("default surface advertises %q over MCP — not in coreToolset", name)
		}
	}
	for name := range coreToolset {
		if !srv.mcpVisible[name] {
			t.Errorf("default surface does not advertise core tool %q over MCP", name)
		}
	}

	// Full registration preserved underneath: REST never loses tools.
	for name := range expectedMCPTools {
		if _, ok := srv.handlers[name]; !ok {
			t.Errorf("default surface dropped %q from s.handlers — HTTP /v1/%s would 404", name, name)
		}
		if _, ok := srv.tools[name]; !ok {
			t.Errorf("default surface dropped %q from s.tools — OpenAPI/contract surface broken", name)
		}
	}

	// Descriptions are the lean transform of the rich ones.
	t.Setenv("PINCHER_TOOLSET", "full")
	t.Setenv("PINCHER_SCHEMA_STYLE", "rich")
	rich, _, _ := newTestServer(t)
	for name, tool := range srv.tools {
		richTool := rich.tools[name]
		if richTool == nil {
			t.Errorf("tool %q missing from rich-mode server", name)
			continue
		}
		if want := leanToolDescription(name, richTool.Description); tool.Description != want {
			t.Errorf("default description for %q is not the lean transform:\n got %q\nwant %q",
				name, tool.Description, want)
		}
	}
}

// TestToolContract_DefaultSurface_RouterPresent is the detected-state
// twin of TestToolContract_DefaultSurface (router-loop B5 dual-state
// goldens, plan §A6). PINCHER_ROUTER=on forces detection without a
// live router (the item-B4 override: `on` short-circuits the ladder,
// zero network). The advertisement must be EXACTLY coreToolset plus
// the two router tools — they join the core advertisement when
// detected (a routed loop is the core use-case) — with lean
// descriptions like every other advertised tool, and the full
// registration underneath unchanged.
func TestToolContract_DefaultSurface_RouterPresent(t *testing.T) {
	t.Setenv("PINCHER_TOOLSET", "")
	t.Setenv("PINCHER_SCHEMA_STYLE", "")
	t.Setenv("PINCHER_ROUTER", "on")
	srv, _, _ := newTestServer(t)

	// Advertisement == coreToolset ∪ routerConditionalTools, both directions.
	for name := range srv.mcpVisible {
		if !coreToolset[name] && !routerConditionalTools[name] {
			t.Errorf("router-present default surface advertises %q over MCP — not in coreToolset or routerConditionalTools", name)
		}
	}
	for name := range coreToolset {
		if !srv.mcpVisible[name] {
			t.Errorf("router-present default surface does not advertise core tool %q over MCP", name)
		}
	}
	for name := range routerConditionalTools {
		if !srv.mcpVisible[name] {
			t.Errorf("router detected but %q is not advertised over MCP — the conditional surface must join the core advertisement when present", name)
		}
	}

	// Full registration preserved underneath, exactly as in the absent state.
	for name := range expectedMCPTools {
		if _, ok := srv.handlers[name]; !ok {
			t.Errorf("router-present surface dropped %q from s.handlers — HTTP /v1/%s would 404", name, name)
		}
	}

	// The router tools ship lean descriptions under the default style,
	// including their usage note (leanAuthorityNote) — one sentence +
	// the load-bearing never-block/ownership clause.
	t.Setenv("PINCHER_TOOLSET", "full")
	t.Setenv("PINCHER_SCHEMA_STYLE", "rich")
	rich, _, _ := newTestServer(t)
	for name := range routerConditionalTools {
		got := srv.tools[name]
		richTool := rich.tools[name]
		if got == nil || richTool == nil {
			t.Fatalf("tool %q missing from one of the servers (lean=%v rich=%v)", name, got != nil, richTool != nil)
		}
		if want := leanToolDescription(name, richTool.Description); got.Description != want {
			t.Errorf("default description for %q is not the lean transform:\n got %q\nwant %q", name, got.Description, want)
		}
		if note := leanAuthorityNote[name]; note == "" || !strings.Contains(got.Description, note) {
			t.Errorf("lean description for %q must carry its usage note; note=%q desc=%q", name, note, got.Description)
		}
	}
}

// TestToolContract_RouterAbsent_FullToolset_ZeroSurface pins the
// anti-leverage guarantee from the other side: even under
// PINCHER_TOOLSET=full (which advertises every other registered tool),
// the router tools stay off tools/list when no router was detected —
// absent means ZERO advertised surface in both toolset modes, while
// the HTTP /v1/<tool> registration stays (surface decided at startup,
// liveness per call).
func TestToolContract_RouterAbsent_FullToolset_ZeroSurface(t *testing.T) {
	t.Setenv("PINCHER_TOOLSET", "full")
	t.Setenv("PINCHER_SCHEMA_STYLE", "")
	t.Setenv("PINCHER_ROUTER", "off")
	srv, _, _ := newTestServer(t)

	for name := range routerConditionalTools {
		if srv.mcpVisible[name] {
			t.Errorf("router absent but %q advertised over MCP under PINCHER_TOOLSET=full — zero-surface-when-absent must hold in both toolset modes", name)
		}
		if _, ok := srv.handlers[name]; !ok {
			t.Errorf("router absent and %q missing from s.handlers — registration must be unconditional (HTTP /v1/%s, batch, contract golden)", name, name)
		}
		if _, ok := srv.tools[name]; !ok {
			t.Errorf("router absent and %q missing from s.tools — OpenAPI/contract surface must keep the full registration", name)
		}
	}
}

// firstDiff returns a short slice of both strings around the first
// differing byte, just enough to surface "the kind that changed" without
// dumping the full file.
func firstDiff(got, want string) string {
	n := len(got)
	if len(want) < n {
		n = len(want)
	}
	for i := 0; i < n; i++ {
		if got[i] != want[i] {
			start := i - 40
			if start < 0 {
				start = 0
			}
			end := i + 80
			if end > len(got) {
				end = len(got)
			}
			endW := i + 80
			if endW > len(want) {
				endW = len(want)
			}
			return "got=" + got[start:end] + "\n  want=" + want[start:endW]
		}
	}
	if len(got) != len(want) {
		return "(lengths differ; one is a prefix of the other)"
	}
	return "(strings equal — flake?)"
}
