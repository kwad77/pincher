package server

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// #672 workstream 4 (v0.79 hardening): assert every registered MCP
// tool name appears at least once in docs/REFERENCE.md as a
// backticked code-span. Pre-fix, `context_for_task` (#1259 v0.71)
// was registered + counted in the MCP tool-count metadata line but
// had no row in REFERENCE.md's tool tables — the count was right
// but a user reading the tables would only find 22.
//
// Complementary to TestReferenceMD_ToolCountParity (#1506): that
// pins the COUNT, this pins each NAME. Both gates are needed —
// count parity catches "added a tool but the count got stale";
// name parity catches "the count moved but a tool got dropped
// from the tables silently."
//
// Backtick-anywhere is the lenient check — REFERENCE.md uses both
// formal tool-table rows and prose mentions; either counts as
// "documented." A stricter "row in a tool table" test would force
// docs structure decisions on every PR; we already get that via
// the count-parity test.

func TestReferenceMD_EveryRegisteredToolMentioned(t *testing.T) {
	t.Parallel()

	srv, _, _ := newTestServer(t)
	if len(srv.tools) == 0 {
		t.Fatal("newTestServer registered zero tools")
	}

	refBytes, err := os.ReadFile("../../docs/reference/tools.md")
	if err != nil {
		t.Fatalf("read docs/reference/tools.md: %v", err)
	}
	ref := string(refBytes)

	// Match any backticked code-span that's a registered tool name.
	for name := range srv.tools {
		// We only care about the body of tools.md once. Build
		// the literal needle: an exact backticked occurrence.
		needle := "`" + name + "`"
		if !strings.Contains(ref, needle) {
			t.Errorf("registered tool %q has no backticked mention anywhere in docs/reference/tools.md — add a tool-table row (preferred) or at minimum a prose mention", name)
		}
	}

	// Soft signal in the other direction: REFERENCE.md backticked
	// identifiers that LOOK like tool names (lowercase, underscore,
	// no path separator) but don't map to a registered tool. Log
	// them — could be field names (legitimate) or stale tool
	// references (drift). We don't fail on these; many are field/
	// argument names that share the lower_snake shape.
	identRE := regexp.MustCompile("`([a-z][a-z_0-9]*)`")
	registered := make(map[string]bool)
	for name := range srv.tools {
		registered[name] = true
	}
	// Common false positives: field/arg names, enum values, env vars.
	// We don't try to enumerate them; just log unknowns so a human
	// can scan once when the test is wired up.
	unknown := map[string]bool{}
	for _, m := range identRE.FindAllStringSubmatch(ref, -1) {
		token := m[1]
		if registered[token] {
			continue
		}
		// Single-char and very-short tokens are almost always
		// field names; skip.
		if len(token) < 4 {
			continue
		}
		unknown[token] = true
	}
	// Don't t.Log every unknown — there are hundreds of legitimate
	// field/arg/enum names. The forward direction is the gate.
	_ = unknown
}
