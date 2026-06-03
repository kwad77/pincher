// SPDX-License-Identifier: MIT

package cypher

import (
	"context"
	"strings"
	"testing"
)

// #1480 — USES_VAR was missing from the hardcoded knownEdgeKinds allowlist
// despite the extractor (internal/ast/jinja.go, internal/ast/yaml.go) and
// resolver (internal/index/indexer.go::resolveUsesVar) emitting the kind
// via #1380. Pre-fix, MATCH (a)-[:USES_VAR]->(b) returned 0 rows AND a
// misleading "not recognized" warning even when USES_VAR rows existed in
// the DB. Post-fix, USES_VAR is a recognized kind: the query resolves
// and the warning does not fire.

func TestExecute_USES_VAR_EdgeKindRecognized(t *testing.T) {
	// Positive shape. Insert a USES_VAR edge; MATCH on it should
	// resolve and not produce an unknown-kind warning.
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "setting", "config.db_host", "Setting", "YAML")
	insertSym(t, db, "tmpl", "templates/web.j2", "Variable", "Jinja2")
	insertEdge(t, db, "tmpl", "setting", "USES_VAR")

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (a)-[:USES_VAR]->(b) RETURN a.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	for _, w := range r.Warnings {
		if strings.Contains(w, "edge kind") && strings.Contains(w, "USES_VAR") {
			t.Errorf("USES_VAR is now a registered kind — must not warn; got: %v", w)
		}
	}
	if r.Total != 1 {
		t.Errorf("expected USES_VAR edge to resolve; got %d rows", r.Total)
	}
}

func TestExecute_UnknownKindWarning_NamesUSES_VAR(t *testing.T) {
	// Cross-check shape. When a TYPO'd edge kind triggers the
	// not-recognized warning, the warning message must include
	// USES_VAR in its "valid kinds" enumeration so the agent knows
	// the kind exists if they meant to type it.
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "a", "A", "Function", "Go")

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (a)-[:USES_VARZ]->(b) RETURN a.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var foundEnumeration bool
	for _, w := range r.Warnings {
		if strings.Contains(w, "USES_VARZ") && strings.Contains(w, "USES_VAR") {
			foundEnumeration = true
			break
		}
	}
	if !foundEnumeration {
		t.Errorf("warning text must enumerate USES_VAR as a valid kind so agents learn the kind exists; got: %v", r.Warnings)
	}
}

func TestExecute_AnsibleStructuralEdgeKindsRecognized_1869(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "site", "site", "Module", "YAML")
	insertSym(t, db, "role", "roles.web.tasks.main", "Module", "YAML")
	insertSym(t, db, "inventory", "inventory.hosts", "Module", "YAML")
	insertSym(t, db, "hostvars", "host_vars.web_01", "Module", "YAML")
	insertEdge(t, db, "site", "role", "INCLUDES")
	insertEdge(t, db, "inventory", "hostvars", "LOADS")

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	for _, kind := range []string{"INCLUDES", "LOADS"} {
		r, err := e.Execute(context.Background(),
			`MATCH (a)-[:`+kind+`]->(b) RETURN a.name`)
		if err != nil {
			t.Fatalf("Execute %s: %v", kind, err)
		}
		for _, w := range r.Warnings {
			if strings.Contains(w, "edge kind") && strings.Contains(w, kind) {
				t.Errorf("%s is now a registered kind — must not warn; got: %v", kind, w)
			}
		}
		if r.Total != 1 {
			t.Errorf("expected %s edge to resolve; got %d rows", kind, r.Total)
		}
	}
}
