// SPDX-License-Identifier: MIT

package cypher

import (
	"context"
	"testing"
)

// #792: inline brace props in a MATCH pattern — MATCH (n:Kind {prop:val})
// — were handled inconsistently across the three runners:
//   - runNodeScan applied them but bound bool literals verbatim, so
//     {is_exported:true} bound "TRUE" and matched zero rows.
//   - runJoinQuery dropped them entirely — the {name:"X"} on an edge
//     target was ignored, returning callers of every node.
//   - runBFS dropped them entirely on both ends.

func TestExecute_InlineProps_NodeScan_BoolValue(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSymWithExported(t, db, "exp", "Apple", "Function", true)
	insertSymWithExported(t, db, "unexp", "banana", "Function", false)

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (n:Function {is_exported: true}) RETURN n.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.Total != 1 {
		t.Fatalf("rows=%d, want 1 — inline {is_exported:true} must bool-coerce like WHERE does", r.Total)
	}
	if name, _ := r.Rows[0]["n.name"].(string); name != "Apple" {
		t.Errorf("row name = %q, want Apple", name)
	}
}

func TestExecute_InlineProps_NodeScan_StringValue(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "a", "Open", "Function", "Go")
	insertSym(t, db, "b", "Close", "Function", "Go")

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (n:Function {name: "Open"}) RETURN n.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.Total != 1 {
		t.Fatalf("rows=%d, want 1 (only Open)", r.Total)
	}
}

// The confirmed confidently-wrong case: an inline prop on the edge
// target node. Pre-fix runJoinQuery ignored it, so a nonexistent name
// still returned every caller.
func TestExecute_InlineProps_JoinQuery_TargetNodeFiltered(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "caller1", "CallerOne", "Function", "Go")
	insertSym(t, db, "caller2", "CallerTwo", "Function", "Go")
	insertSym(t, db, "target", "RealTarget", "Function", "Go")
	insertSym(t, db, "other", "OtherTarget", "Function", "Go")
	insertEdge(t, db, "caller1", "target", "CALLS")
	insertEdge(t, db, "caller2", "other", "CALLS")

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}

	// Real target name → only its caller.
	r, err := e.Execute(context.Background(),
		`MATCH (a:Function)-[:CALLS]->(b:Function {name: "RealTarget"}) RETURN a.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.Total != 1 {
		t.Fatalf("rows=%d, want 1 — only CallerOne calls RealTarget", r.Total)
	}
	if name, _ := r.Rows[0]["a.name"].(string); name != "CallerOne" {
		t.Errorf("caller = %q, want CallerOne", name)
	}

	// Nonexistent target name → zero rows. Pre-fix this returned every
	// caller because the {name:...} predicate was dropped.
	r, err = e.Execute(context.Background(),
		`MATCH (a:Function)-[:CALLS]->(b:Function {name: "ZZZ_NONEXISTENT"}) RETURN a.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.Total != 0 {
		t.Fatalf("rows=%d, want 0 — a nonexistent target name must filter, not pass through", r.Total)
	}
}

// runBFS: inline props on both the start and destination ends of a
// variable-length pattern.
func TestExecute_InlineProps_BFS_BothEnds(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "start", "StartFn", "Function", "Go")
	insertSym(t, db, "mid", "MidFn", "Function", "Go")
	insertSym(t, db, "end", "EndFn", "Function", "Go")
	insertSym(t, db, "decoy", "DecoyFn", "Function", "Go")
	insertEdge(t, db, "start", "mid", "CALLS")
	insertEdge(t, db, "mid", "end", "CALLS")
	insertEdge(t, db, "start", "decoy", "CALLS")

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}

	// Destination-end prop: StartFn reaches EndFn in 2 hops, DecoyFn in 1.
	r, err := e.Execute(context.Background(),
		`MATCH (a:Function {name: "StartFn"})-[:CALLS*1..3]->(b:Function {name: "EndFn"}) RETURN b.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.Total != 1 {
		t.Fatalf("rows=%d, want 1 — only EndFn matches the destination prop", r.Total)
	}

	// Nonexistent destination name → zero rows.
	r, err = e.Execute(context.Background(),
		`MATCH (a:Function {name: "StartFn"})-[:CALLS*1..3]->(b:Function {name: "ZZZ_NONEXISTENT"}) RETURN b.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.Total != 0 {
		t.Fatalf("rows=%d, want 0 — nonexistent destination name must filter", r.Total)
	}
}
