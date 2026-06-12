// SPDX-License-Identifier: MIT

package server

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// Schema diet (#2003) — Phase 1 measurement + permanent regression gate.
//
// The messy-corpus loopbench run (PR #2002) measured ~46.5k tokens of
// MCP tool schemas cache-created at session start for the pincher arm
// vs ~27k for native arms — re-read every turn, and the reason a
// 10/10-accuracy, 22-vs-36-turn pincher run still lost on tokens.
// These tests make that weight a first-class, committed number:
//
//   - TestSchemaWeight_Report renders every MCP-visible tool exactly as
//     a tools/list client receives it (name + title + description +
//     inputSchema + annotations), weighs it with db.ApproxTokens, and
//     pins the per-tool table + the full/core × rich/lean totals to
//     testdata/schema-weight.md. Any schema growth shows up as a
//     reviewable diff. Regenerate with:
//     go test ./internal/server/ -run TestSchemaWeight_Report -update-schema-weight
//
//   - TestSchemaWeight_CoreLean_UnderBudget is the hard gate: the
//     core-toolset + lean-style surface must stay under
//     coreLeanTokenBudget approximate tokens.

var updateSchemaWeight = flag.Bool("update-schema-weight", false,
	"rewrite testdata/schema-weight.md instead of asserting against it")

// coreLeanTokenBudget is the hard ceiling (approximate tokens,
// db.ApproxTokens chars/4 heuristic) for the PINCHER_TOOLSET=core +
// PINCHER_SCHEMA_STYLE=lean tools/list surface. Set from the Phase-1
// measurement with headroom for organic arg additions; raising it is a
// deliberate, reviewed decision.
const coreLeanTokenBudget = 4000

// clientToolEntry is the tools/list wire shape a client pays for, in
// the SDK's field order. Annotations and Title are included because
// the client receives them too — the weight must be honest.
type clientToolEntry struct {
	Name        string          `json:"name"`
	Title       string          `json:"title,omitempty"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	Annotations any             `json:"annotations,omitempty"`
}

// schemaWeights renders every MCP-visible tool on srv as a client
// receives it and returns per-tool approximate token weights plus the
// total.
func schemaWeights(t *testing.T, srv *Server) (map[string]int, int) {
	t.Helper()
	perTool := make(map[string]int, len(srv.mcpVisible))
	total := 0
	for name := range srv.mcpVisible {
		tool := srv.tools[name]
		if tool == nil {
			t.Fatalf("mcpVisible tool %q missing from s.tools", name)
		}
		rawSchema, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatalf("marshal %q InputSchema: %v", name, err)
		}
		entry := clientToolEntry{
			Name:        tool.Name,
			Title:       tool.Title,
			Description: tool.Description,
			InputSchema: rawSchema,
		}
		if tool.Annotations != nil {
			entry.Annotations = tool.Annotations
		}
		b, err := json.Marshal(entry)
		if err != nil {
			t.Fatalf("marshal %q entry: %v", name, err)
		}
		w := db.ApproxTokens(string(b))
		perTool[name] = w
		total += w
	}
	return perTool, total
}

// newSchemaWeightServer builds a server under the given toolset/style
// env and the given router-detection state (router-loop B5: "off"
// pins the absent state the historical totals were measured in; "on"
// forces detection without a live router, adding the conditional
// models/route advertisement). Not parallel-safe (t.Setenv); callers
// run sequentially.
func newSchemaWeightServer(t *testing.T, toolset, style, router string) *Server {
	t.Helper()
	t.Setenv("PINCHER_TOOLSET", toolset)
	t.Setenv("PINCHER_SCHEMA_STYLE", style)
	t.Setenv("PINCHER_ROUTER", router)
	srv, _, _ := newTestServer(t)
	return srv
}

func TestSchemaWeight_Report(t *testing.T) {
	combos := []struct{ toolset, style string }{
		{"full", "rich"},
		{"full", "lean"},
		{"core", "rich"},
		{"core", "lean"},
	}
	totals := make(map[string]int, len(combos))
	var fullRich map[string]int
	for _, c := range combos {
		srv := newSchemaWeightServer(t, c.toolset, c.style, "off")
		perTool, total := schemaWeights(t, srv)
		totals[c.toolset+"/"+c.style] = total
		if c.toolset == "full" && c.style == "rich" {
			fullRich = perTool
		}
	}

	// Router-present surface (router-loop B5): the conditional
	// models/route tools join the advertisement, so the totals shift —
	// pinned here so the cost of the detected state is a committed,
	// reviewable number, while the table above stays the absent state
	// (zero delta against the pre-router goldens, plan §A6).
	routerTotals := make(map[string]int, len(combos))
	routerPerTool := make(map[string]map[string]int, len(combos))
	for _, c := range combos {
		srv := newSchemaWeightServer(t, c.toolset, c.style, "on")
		perTool, total := schemaWeights(t, srv)
		routerTotals[c.toolset+"/"+c.style] = total
		routerPerTool[c.toolset+"/"+c.style] = perTool
	}

	// Per-tool table, heaviest first (full/rich — the complete surface;
	// the shipped default is full/lean since #2054).
	names := make([]string, 0, len(fullRich))
	for name := range fullRich {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if fullRich[names[i]] != fullRich[names[j]] {
			return fullRich[names[i]] > fullRich[names[j]]
		}
		return names[i] < names[j]
	})

	var b strings.Builder
	b.WriteString("# MCP schema weight (approx tokens, db.ApproxTokens)\n\n")
	b.WriteString("Generated by TestSchemaWeight_Report (`-update-schema-weight`). #2003.\n")
	b.WriteString("Weight = the tools/list entry a client receives (name + title +\ndescription + inputSchema + annotations), chars/4 heuristic.\n\nShipped default since #2054: toolset=full, style=lean (the lean lever\ncarries the dominant saving; the pre-#2054 core default omitted\nbootstrap/diagnose-essential tools). PINCHER_TOOLSET=core re-narrows the\nadvertisement; PINCHER_SCHEMA_STYLE=rich restores full descriptions.\n\n")
	b.WriteString("## Totals by mode\n\n")
	b.WriteString("| toolset | style | tools | total tokens |\n|---|---|--:|--:|\n")
	for _, c := range combos {
		nTools := len(fullRich)
		if c.toolset == "core" {
			nTools = len(coreToolset)
		}
		fmt.Fprintf(&b, "| %s | %s | %d | %d |\n", c.toolset, c.style, nTools, totals[c.toolset+"/"+c.style])
	}
	b.WriteString("\n## Per-tool weight, full/rich (heaviest first)\n\n")
	b.WriteString("| tool | tokens | core |\n|---|--:|:--:|\n")
	for _, name := range names {
		core := ""
		if coreToolset[name] {
			core = "x"
		}
		fmt.Fprintf(&b, "| %s | %d | %s |\n", name, fullRich[name], core)
	}

	b.WriteString("\n## Router-present surface (router-loop B5)\n\n")
	b.WriteString("`models` + `route` join the advertisement only when a live pincher-router\nis detected (in BOTH toolset modes — they ride with the core set). The\ntables above ARE the absent state: zero delta against the pre-router\nsurface (plan §A6). The detected-state core+lean total is held to the\nsame budget gate (TestSchemaWeight_CoreLean_RouterPresent_UnderBudget).\n\n")
	b.WriteString("| toolset | style | tools | total tokens |\n|---|---|--:|--:|\n")
	for _, c := range combos {
		nTools := len(fullRich) + len(routerConditionalTools)
		if c.toolset == "core" {
			nTools = len(coreToolset) + len(routerConditionalTools)
		}
		fmt.Fprintf(&b, "| %s | %s | %d | %d |\n", c.toolset, c.style, nTools, routerTotals[c.toolset+"/"+c.style])
	}
	b.WriteString("\n| router tool | rich | lean |\n|---|--:|--:|\n")
	routerNames := make([]string, 0, len(routerConditionalTools))
	for name := range routerConditionalTools {
		routerNames = append(routerNames, name)
	}
	sort.Strings(routerNames)
	for _, name := range routerNames {
		fmt.Fprintf(&b, "| %s | %d | %d |\n", name,
			routerPerTool["full/rich"][name], routerPerTool["full/lean"][name])
	}

	got := []byte(b.String())
	goldenPath := filepath.Join("testdata", "schema-weight.md")
	if *updateSchemaWeight {
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatalf("write %s: %v", goldenPath, err)
		}
		t.Logf("rewrote %s (full/rich total %d tokens)", goldenPath, totals["full/rich"])
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read %s: %v\n  Run `go test ./internal/server/ -run TestSchemaWeight_Report -update-schema-weight` to create it.", goldenPath, err)
	}
	// Normalize CRLF → LF: git on Windows checks files out with CRLF
	// (autocrlf=true) but we emit LF — same fix as the tool-contract
	// golden. Logical equality, not byte-identical.
	got = bytes.ReplaceAll(got, []byte("\r\n"), []byte("\n"))
	want = bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))
	if string(got) != string(want) {
		t.Errorf("schema weight diverged from %s — tool schemas changed size.\n"+
			"If intentional, regenerate with:\n"+
			"  go test ./internal/server/ -run TestSchemaWeight_Report -update-schema-weight\n"+
			"and review the diff (the diff IS the schema-cost change).", goldenPath)
	}
}

// TestSchemaWeight_CoreLean_UnderBudget is the permanent regression
// gate: the diet surface (core toolset + lean descriptions) must stay
// under coreLeanTokenBudget approximate tokens. If this fails, a
// schema/description grew — either shrink it or consciously raise the
// budget in review.
func TestSchemaWeight_CoreLean_UnderBudget(t *testing.T) {
	srv := newSchemaWeightServer(t, "core", "lean", "off")
	perTool, total := schemaWeights(t, srv)
	if len(perTool) != len(coreToolset) {
		t.Errorf("core toolset advertises %d tools, want %d", len(perTool), len(coreToolset))
	}
	if total >= coreLeanTokenBudget {
		names := make([]string, 0, len(perTool))
		for n := range perTool {
			names = append(names, n)
		}
		sort.Slice(names, func(i, j int) bool { return perTool[names[i]] > perTool[names[j]] })
		var lines []string
		for _, n := range names {
			lines = append(lines, fmt.Sprintf("  %-14s %5d", n, perTool[n]))
		}
		t.Errorf("core+lean schema weight %d tokens >= budget %d.\nPer-tool (heaviest first):\n%s",
			total, coreLeanTokenBudget, strings.Join(lines, "\n"))
	}
	t.Logf("core+lean total: %d tokens (budget %d)", total, coreLeanTokenBudget)
}

// TestSchemaWeight_CoreLean_RouterPresent_UnderBudget holds the
// DETECTED-state default surface (core+lean plus the conditional
// models/route advertisement, router-loop B5) to the same
// coreLeanTokenBudget ceiling. Decision: the budget is not raised for
// the router — the two lean proxy schemas must fit inside the existing
// headroom, so a machine that installs pincher-router still gets a
// sub-4k tools/list. If this fails, shrink the router tool schemas;
// raising the shared budget is a deliberate, reviewed decision.
func TestSchemaWeight_CoreLean_RouterPresent_UnderBudget(t *testing.T) {
	srv := newSchemaWeightServer(t, "core", "lean", "on")
	perTool, total := schemaWeights(t, srv)
	if want := len(coreToolset) + len(routerConditionalTools); len(perTool) != want {
		t.Errorf("router-present core toolset advertises %d tools, want %d (coreToolset + models/route)", len(perTool), want)
	}
	for name := range routerConditionalTools {
		if perTool[name] == 0 {
			t.Errorf("router-present core surface is missing %q from the advertisement", name)
		}
	}
	if total >= coreLeanTokenBudget {
		t.Errorf("router-present core+lean schema weight %d tokens >= budget %d — shrink the models/route schemas (the budget is shared, not raised, for the router surface)",
			total, coreLeanTokenBudget)
	}
	t.Logf("router-present core+lean total: %d tokens (budget %d)", total, coreLeanTokenBudget)
}
