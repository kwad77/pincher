// SPDX-License-Identifier: MIT

package cypher

import "testing"

// #918: `MATCH (n:Function) RETURN n` returned just the name string
// pre-fix (`{"n": "Open"}`) — comment in buildResult claimed "return
// all properties for the variable" but code returned `.name`. Cypher
// spec says RETURN n on a node variable returns the entire node.
// Same silent-confidently-wrong shape: comment-implementation drift.

func TestRETURN_BareNode_ReturnsAllProperties(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	insertSym(t, db, "id-1", "Open", "Function", "Go")

	r := exec(t, db, "MATCH (n:Function) WHERE n.name=\"Open\" RETURN n LIMIT 1")
	if r.Total != 1 {
		t.Fatalf("Total = %d, want 1", r.Total)
	}
	node, ok := r.Rows[0]["n"].(map[string]any)
	if !ok {
		t.Fatalf("RETURN n must yield a nested object, not a scalar; got %T = %v", r.Rows[0]["n"], r.Rows[0]["n"])
	}
	if got, _ := node["name"].(string); got != "Open" {
		t.Errorf("node[name] = %v, want Open", node["name"])
	}
	if got, _ := node["kind"].(string); got != "Function" {
		t.Errorf("node[kind] = %v, want Function", node["kind"])
	}
	if got, _ := node["language"].(string); got != "Go" {
		t.Errorf("node[language] = %v, want Go", node["language"])
	}
}

// Property-specific projection (existing canonical) unaffected.
func TestRETURN_NodeProperty_StillReturnsScalar(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	insertSym(t, db, "id-1", "Open", "Function", "Go")

	r := exec(t, db, "MATCH (n:Function) RETURN n.name LIMIT 1")
	if got, _ := r.Rows[0]["n.name"].(string); got != "Open" {
		t.Errorf("RETURN n.name = %v, want Open", r.Rows[0]["n.name"])
	}
}
