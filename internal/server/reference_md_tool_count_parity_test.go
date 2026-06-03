// SPDX-License-Identifier: MIT

package server

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// #672 workstream 4 (v0.79 capability-advertisement audit, doc parity
// half). Pre-fix three tutorial pages linked to a stale tool-count anchor —
// a stale anchor that no longer resolved in REFERENCE.md
// (for example, `## The N MCP tools` for the wrong N). Users clicking those
// links landed at the top of REFERENCE.md instead of the tool
// catalogue, looking exactly like a 404 to the anchor-jumping
// behaviour they expected. This drift shipped through six release
// cycles undetected because no test pinned the count.
//
// The mirror gates already exist for schema_version drift
// (TestReferenceMD_SchemaVersionParity, TestClaudeMD_SchemaVersionParity)
// and the leading metadata line — but the tool-count surface had no
// gate. This test pins:
//
//   1. REFERENCE.md `**MCP tools:** N` (metadata line, line 5) ==
//      `## The N MCP tools` heading (line 180).
//   2. Both == runtime `len(srv.tools)` for a default test server.
//   3. Every `docs/tutorials/*.md` link to `#the-N-mcp-tools` uses
//      the same N.
//
// All three directions fire on drift. Future tool addition: bump
// REFERENCE.md heading + metadata line in lockstep with registerTools();
// tutorial anchors update by find-replace.

func TestReferenceMD_ToolCountParity(t *testing.T) {
	t.Parallel()

	srv, _, _ := newTestServer(t)
	runtimeCount := len(srv.tools)
	if runtimeCount == 0 {
		t.Fatal("registerTools() registered zero tools — newTestServer wiring drift")
	}

	// Metadata line lives in the reference index file.
	idxBytes, err := os.ReadFile("../../docs/reference/README.md")
	if err != nil {
		t.Fatalf("read docs/reference/README.md: %v", err)
	}
	idx := string(idxBytes)

	// Tool catalogue heading lives in the tools file.
	toolsBytes, err := os.ReadFile("../../docs/reference/tools.md")
	if err != nil {
		t.Fatalf("read docs/reference/tools.md: %v", err)
	}
	tools := string(toolsBytes)

	// Metadata line: **Schema version:** v34 · **MCP tools:** N · ...
	metaRE := regexp.MustCompile(`\*\*MCP tools:\*\*\s+(\d+)`)
	metaMatch := metaRE.FindStringSubmatch(idx)
	if metaMatch == nil {
		t.Fatal("could not parse `**MCP tools:** N` from docs/reference/README.md metadata line")
	}
	metaCount, _ := strconv.Atoi(metaMatch[1])

	// Heading: # The N MCP tools
	headingRE := regexp.MustCompile(`(?m)^# The (\d+) MCP tools\s*$`)
	headingMatch := headingRE.FindStringSubmatch(tools)
	if headingMatch == nil {
		t.Fatal("could not parse `# The N MCP tools` heading from docs/reference/tools.md")
	}
	headingCount, _ := strconv.Atoi(headingMatch[1])

	if metaCount != runtimeCount {
		t.Errorf("docs/reference/README.md metadata claims %d MCP tools but runtime registers %d — update the leading metadata line", metaCount, runtimeCount)
	}
	if headingCount != runtimeCount {
		t.Errorf("docs/reference/tools.md heading reads `# The %d MCP tools` but runtime registers %d — update the heading", headingCount, runtimeCount)
	}
	if metaCount != headingCount {
		t.Errorf("reference docs self-inconsistent: README.md metadata says %d, tools.md heading says %d", metaCount, headingCount)
	}
}

// TestTutorials_ToolCountAnchorParity walks docs/tutorials/*.md and
// asserts every `#the-N-mcp-tools` anchor link uses the same N as
// REFERENCE.md's heading. Caught pre-fix: claude-code.md / cursor.md /
// vscode-copilot.md still linked `#the-22-mcp-tools` after the count
// bumped to 23 in v0.65 (#1192/#1191).
func TestTutorials_ToolCountAnchorParity(t *testing.T) {
	t.Parallel()

	refBytes, err := os.ReadFile("../../docs/reference/tools.md")
	if err != nil {
		t.Fatalf("read docs/reference/tools.md: %v", err)
	}
	headingRE := regexp.MustCompile(`(?m)^# The (\d+) MCP tools\s*$`)
	headingMatch := headingRE.FindStringSubmatch(string(refBytes))
	if headingMatch == nil {
		t.Fatal("could not parse `# The N MCP tools` heading from docs/reference/tools.md")
	}
	want := headingMatch[1]
	wantAnchor := "#the-" + want + "-mcp-tools"

	tutorialDir := "../../docs/tutorials"
	entries, err := os.ReadDir(tutorialDir)
	if err != nil {
		t.Fatalf("read docs/tutorials: %v", err)
	}

	// Match any `#the-N-mcp-tools` form regardless of which N.
	linkRE := regexp.MustCompile(`#the-(\d+)-mcp-tools`)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		full := filepath.Join(tutorialDir, name)
		b, err := os.ReadFile(full)
		if err != nil {
			t.Errorf("read %s: %v", name, err)
			continue
		}
		text := string(b)
		matches := linkRE.FindAllStringSubmatchIndex(text, -1)
		for _, m := range matches {
			gotN := text[m[2]:m[3]]
			if gotN != want {
				// Find line number for a useful error message.
				line := 1 + strings.Count(text[:m[0]], "\n")
				t.Errorf("docs/tutorials/%s:%d links to %q but docs/reference/tools.md heading is %q — anchor is stale, update or the tools.md heading moved", name, line, "#the-"+gotN+"-mcp-tools", wantAnchor)
			}
		}
	}
}
