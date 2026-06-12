// SPDX-License-Identifier: MIT

package server

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// #672 workstream 4 (v0.79 capability-advertisement audit): every
// capability tag surfaced in `_meta.capabilities` MUST have a row in
// the canonical capability table in docs/integrations/meta-envelope-contract.md.
// Router developers consult that table when wiring against a pincher
// build; an undocumented tag is silently-confidently-wrong from their
// vantage point (they don't know what `mcp_logging` means without
// reading source).
//
// Drift caught at audit: v0.78's `mcp_logging` tag (#1085) was
// advertised + tested but never added to the doc table. The probe
// gate (TestCapability_EveryAdvertisedTagHasRuntimeProbe) covered
// runtime parity but the doc-parity hole let the tag ship undocumented.
//
// This test extracts every tag from the markdown tables (both the
// always-on and conditional sections) and asserts every entry in
// computeCapabilities() has a documented row. The reverse (doc row
// without code) also fails: a documented tag that the code no longer
// advertises is a router-reading-stale-claim problem.

func TestCapability_EveryAdvertisedTagDocumented(t *testing.T) {
	t.Parallel()

	docBytes, err := os.ReadFile("../../docs/integrations/meta-envelope-contract.md")
	if err != nil {
		t.Fatalf("read meta-envelope-contract.md: %v", err)
	}
	docText := string(docBytes)

	// Slice the doc to the capabilities[] section so we don't match
	// tag-shaped strings in other tables (e.g. doctor advisories).
	startIdx := strings.Index(docText, "### `capabilities[]`")
	if startIdx < 0 {
		t.Fatal("could not locate ### `capabilities[]` section header in doc")
	}
	// End at the next ### heading at the same depth.
	rest := docText[startIdx+len("### `capabilities[]`"):]
	endRel := strings.Index(rest, "\n### ")
	var section string
	if endRel < 0 {
		section = rest
	} else {
		section = rest[:endRel]
	}

	// Markdown table rows in this section: leading `| `\`tag\`` ` form.
	// Tolerate optional `schema_v\d+` since the doc lists a concrete
	// version e.g. `schema_v33` — we want any schema_v{N} variant to
	// satisfy the parity check.
	rowRE := regexp.MustCompile(`\|\s*` + "`" + `([a-z][a-z0-9_]*)` + "`" + `\s*\|`)
	documented := make(map[string]bool)
	for _, m := range rowRE.FindAllStringSubmatch(section, -1) {
		documented[m[1]] = true
	}
	if len(documented) == 0 {
		t.Fatalf("no capability rows parsed from section — regex shape may have drifted vs doc shape\nSECTION:\n%s", section)
	}

	srv, _, _ := newTestServer(t)
	advertised := make(map[string]bool)
	for _, tag := range srv.capabilities {
		advertised[tag] = true
	}

	// Forward: every advertised tag has a doc row.
	for tag := range advertised {
		want := tag
		// schema_v33 → match either exact or any schema_v{N}.
		if strings.HasPrefix(tag, "schema_v") {
			// Treat ANY schema_v{N} row in the doc as documenting the family.
			if hasAnySchemaVRow(documented) {
				continue
			}
			want = "schema_v{N}"
		}
		if !documented[tag] {
			t.Errorf("capability %q is advertised at runtime but no row in docs/integrations/meta-envelope-contract.md → ### `capabilities[]` table — add a row or drop the advertisement (looked for %q)", tag, want)
		}
	}

	// Reverse: every documented tag is either currently advertised by
	// THIS test server or is a known conditional that this test server
	// doesn't enable.
	conditionals := map[string]bool{
		"http_auth":       true,
		"streamable_http": true,
		"closure_tables":  true,
		"traces_otlp":     true,
		// Router-loop item B4: advertised only when the detection
		// ladder (router_detect.go) finds a live, identity-validated
		// pincher-router — never on a default test server.
		"router": true,
	}
	for tag := range documented {
		if strings.HasPrefix(tag, "schema_v") {
			continue
		}
		if advertised[tag] {
			continue
		}
		if conditionals[tag] {
			continue
		}
		t.Errorf("capability %q has a doc row but is NOT advertised by a default server and is not a known conditional — drop the row or wire the capability", tag)
	}
}

func hasAnySchemaVRow(m map[string]bool) bool {
	for k := range m {
		if strings.HasPrefix(k, "schema_v") {
			return true
		}
	}
	return false
}
