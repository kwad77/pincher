// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kwad77/pincher/internal/db"
	"github.com/kwad77/pincher/internal/index"
)

// TestReportJSON_PincherReportV1Contract pins the v1.2-shipped JSON
// report shape (`pincher_report.v1`) as a versioned contract.
//
// The CLI report is the closest analog to MCP tool I/O for the v1.2
// graph-intelligence surfaces — it's the structured artifact that
// downstream consumers (Pincher Router training, dashboards, external
// analysis tools) read. Removing or renaming any top-level field, or
// dropping a required nested field, would break those consumers
// silently.
//
// This test asserts the **structural contract** — that every documented
// field exists with the right shape. It does NOT assert specific values
// (those vary by corpus / extraction state and belong in the corpus
// fidelity snapshot tests).
//
// When you intentionally evolve the format, follow the same `_v2`
// additive-extension pattern documented for the `_meta` envelope in
// ADR-0002: emit a new field next to the old one, leave the old one
// populated for one minor of back-compat, then deprecate in a follow-up
// minor. Renaming or removing a field listed below is a v2.0 breaking
// change.
//
// Locked at v1.2.0-rc.2 per Tier 1 test-coverage hardening.
func TestReportJSON_PincherReportV1Contract(t *testing.T) {
	// Seed a tiny synthetic project so the contract test doesn't depend
	// on the corpus fixtures (so it doesn't double up with the corpus
	// fidelity test or get skipped if corpora drift).
	dataDir := t.TempDir()
	projDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(projDir, "m.go"), []byte("package main\n\n// WHY: pincher_report.v1 contract fixture.\nfunc main() { Helper() }\n\nfunc Helper() {}\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	store, err := db.Open(dataDir)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	res, err := index.New(store).Index(context.Background(), projDir, false)
	if err != nil {
		store.Close()
		t.Fatalf("index: %v", err)
	}
	project, _ := store.GetProject(res.ProjectID)
	store.Close()

	var out, errb strings.Builder
	code := reportCLI([]string{"--data-dir", dataDir, "--project", project.Name, "--format", "json"}, &out, &errb)
	if code != 0 {
		t.Fatalf("reportCLI --format=json exit = %d, want 0; stderr=%s", code, errb.String())
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(out.String()), &payload); err != nil {
		t.Fatalf("json report unparseable: %v\n%s", err, out.String())
	}

	// 1. Versioned format identifier.
	if got, ok := payload["format"].(string); !ok || got != "pincher_report.v1" {
		t.Errorf(`payload["format"] = %v, want "pincher_report.v1"`, payload["format"])
	}

	// 2. Top-level required fields. Removing or renaming any of these
	// is a 2.0 breaking change.
	required := []string{
		"format",
		"generated_at",
		"project",
		"counts",
		"entry_points",
		"hotspots",
		"rationales",
		"surprising_connections",
		"next_pincher_calls",
		"provenance",
	}
	for _, field := range required {
		if _, present := payload[field]; !present {
			t.Errorf(`payload missing required top-level field %q`, field)
		}
	}

	// 3. project sub-shape.
	projectObj, ok := payload["project"].(map[string]any)
	if !ok {
		t.Fatalf(`payload["project"] is not an object: %T`, payload["project"])
	}
	for _, field := range []string{"id", "name", "path", "indexed_at", "binary_version", "files", "symbols", "edges"} {
		if _, present := projectObj[field]; !present {
			t.Errorf(`project missing required field %q`, field)
		}
	}

	// 4. counts sub-shape.
	countsObj, ok := payload["counts"].(map[string]any)
	if !ok {
		t.Fatalf(`payload["counts"] is not an object: %T`, payload["counts"])
	}
	for _, field := range []string{"languages", "node_kinds", "edge_kinds"} {
		if _, present := countsObj[field]; !present {
			t.Errorf(`counts missing required field %q`, field)
		}
	}

	// 5. rationales sub-shape — v1.2 #1913 / #1920 added grouping and
	// per-row provenance; pin those names.
	rationalesObj, ok := payload["rationales"].(map[string]any)
	if !ok {
		t.Fatalf(`payload["rationales"] is not an object: %T`, payload["rationales"])
	}
	for _, field := range []string{
		"attached",
		"unattached",
		"missing_or_ambiguous_attachment",
		"total_indexed",
		"total_visible",
		"limited",
		"query_keys",
		"source_policy",
		"groups",
		"rows",
	} {
		if _, present := rationalesObj[field]; !present {
			t.Errorf(`rationales missing required field %q`, field)
		}
	}

	// 6. query_keys must include the v1.2-added rationale-query verbs.
	queryKeysRaw, _ := rationalesObj["query_keys"].([]any)
	queryKeys := make(map[string]bool, len(queryKeysRaw))
	for _, k := range queryKeysRaw {
		if s, ok := k.(string); ok {
			queryKeys[s] = true
		}
	}
	for _, k := range []string{"kind", "attachment", "attachment_state", "file_path", "line_span", "extraction_method", "source"} {
		if !queryKeys[k] {
			t.Errorf(`rationales.query_keys missing %q`, k)
		}
	}

	// 7. provenance sub-shape — explicit per-ADR-0004 "source-grounded,
	// missing-is-missing" policy.
	provenanceObj, ok := payload["provenance"].(map[string]any)
	if !ok {
		t.Fatalf(`payload["provenance"] is not an object: %T`, payload["provenance"])
	}
	for _, field := range []string{"source", "missing_policy"} {
		if _, present := provenanceObj[field]; !present {
			t.Errorf(`provenance missing required field %q`, field)
		}
	}

	// 8. next_pincher_calls — each row carries the structured
	// tool/args/why/expected_value contract that pincher-router trains
	// on. At least one row is expected for any non-empty project.
	nextCalls, ok := payload["next_pincher_calls"].([]any)
	if !ok {
		t.Fatalf(`payload["next_pincher_calls"] is not an array: %T`, payload["next_pincher_calls"])
	}
	if len(nextCalls) == 0 {
		t.Fatalf(`next_pincher_calls is empty for a non-empty project — at least one suggestion expected`)
	}
	for i, c := range nextCalls {
		row, ok := c.(map[string]any)
		if !ok {
			t.Errorf(`next_pincher_calls[%d] is not an object: %T`, i, c)
			continue
		}
		for _, field := range []string{"tool", "args", "why"} {
			if _, present := row[field]; !present {
				t.Errorf(`next_pincher_calls[%d] missing required field %q`, i, field)
			}
		}
		// `args` must be an object (not a string). The v1.2 next-best
		// structure replaced legacy prose args with structured args; this
		// pin catches accidental regression to prose.
		if _, ok := row["args"].(map[string]any); !ok {
			t.Errorf(`next_pincher_calls[%d].args is %T, want map[string]any (legacy prose args were retired in v1.2)`, i, row["args"])
		}
	}
}
