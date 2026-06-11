// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// v0.65 description-honesty audit (continuation of v0.64 work):
// the query tool's `cypher` alias was added in #712 with a
// "kept for one release" warning. Many releases later it's still
// honored. The description's "kept for one release" was a
// commitment that didn't survive the next ten releases. Audit
// rewrites the description + warning to match reality: the alias
// is honored with a per-call deprecation warning and slated for
// removal in v1.0 (#638 tool-schema-freeze).
//
// Table-from-the-start (#1152):
//   - Positive: description (both `pinchql` description AND
//     `cypher` schema field description) names the v1.0 removal
//     plan + warning behavior.
//   - Negative: stale "kept for one release" phrasing must be
//     gone from both texts.
//   - Control: runtime still honors the cypher alias with a
//     deprecation warning (existing
//     TestQuery_CypherAlias_EmitsDeprecation already pins this;
//     repeated here for completeness).
//   - Cross-check: the warning emitted at runtime matches what
//     the description promises — names v1.0 + #638.

func findQueryToolSchema(t *testing.T) map[string]any {
	t.Helper()
	srv, _, _ := newRichTestServer(t)
	tool := srv.tools["query"]
	if tool == nil {
		t.Fatal("query tool not registered")
	}
	raw, ok := tool.InputSchema.(json.RawMessage)
	if !ok {
		t.Fatalf("InputSchema not json.RawMessage: %T", tool.InputSchema)
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("schema not valid JSON: %v", err)
	}
	return schema
}

func TestQueryDescription_NamesV1RemovalPlan(t *testing.T) {
	schema := findQueryToolSchema(t)
	props, _ := schema["properties"].(map[string]any)

	// pinchql description mentions the alias status.
	pinchql, _ := props["pinchql"].(map[string]any)
	pinchqlDesc, _ := pinchql["description"].(string)
	for _, want := range []string{"deprecated", "v1.0"} {
		if !strings.Contains(pinchqlDesc, want) {
			t.Errorf("pinchql description missing %q\nGOT:\n%s", want, pinchqlDesc)
		}
	}

	// cypher description mentions same. Case-insensitive on
	// "deprecated" since the cypher description leads with a
	// capitalized "Deprecated alias for pinchql".
	cypher, _ := props["cypher"].(map[string]any)
	cypherDesc, _ := cypher["description"].(string)
	cypherDescLower := strings.ToLower(cypherDesc)
	for _, want := range []string{"deprecated", "v1.0"} {
		if !strings.Contains(cypherDescLower, want) {
			t.Errorf("cypher description missing %q (case-insensitive)\nGOT:\n%s", want, cypherDesc)
		}
	}
}

// Negative: "kept for one release" was a v0.x commitment that
// didn't survive past one release. Pin against regression to
// that phrasing.
func TestQueryDescription_DoesNotClaimOneReleaseWindow(t *testing.T) {
	schema := findQueryToolSchema(t)
	props, _ := schema["properties"].(map[string]any)
	pinchql, _ := props["pinchql"].(map[string]any)
	cypher, _ := props["cypher"].(map[string]any)
	pd, _ := pinchql["description"].(string)
	cd, _ := cypher["description"].(string)
	for _, desc := range []string{pd, cd} {
		if strings.Contains(desc, "kept for one release") {
			t.Errorf("description still claims 'kept for one release' — the commitment has been in place for many releases\nGOT:\n%s", desc)
		}
	}
}

// Cross-check: the warning emitted at runtime matches what the
// description promises. Pre-fix the warning said "kept for one
// release"; after the audit, both warning and description point
// at v1.0 (#638) as the removal plan.
func TestQueryCypherAlias_WarningMatchesDescription(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "p-query-warn"
	store.UpsertProject(db.Project{
		ID: pid, Path: t.TempDir(), Name: pid, IndexedAt: time.Now(),
	})
	srv.sessionID = pid

	res, err := srv.handleQuery(context.Background(), makeReq(map[string]any{
		"cypher": "MATCH (n:Function) RETURN n.name LIMIT 1",
	}))
	if err != nil {
		t.Fatalf("handleQuery: %v", err)
	}
	body := decode(t, res)
	meta, _ := body["_meta"].(map[string]any)
	warns, _ := meta["warnings"].([]any)

	sawV1Mention := false
	for _, w := range warns {
		if s, _ := w.(string); strings.Contains(s, "v1.0") && strings.Contains(s, "deprecated") {
			sawV1Mention = true
			break
		}
	}
	if !sawV1Mention {
		t.Errorf("warning emitted at runtime must match description's v1.0 removal plan; got warns=%v", warns)
	}
}
