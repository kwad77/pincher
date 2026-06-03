// SPDX-License-Identifier: MIT

package cypher

import (
	"context"
	"strconv"
	"testing"
)

// #412: WHERE n.id="X" was being post-filtered in Go because cypherPropToCol
// didn't map "id" to a SQL column. The pre-fix scan applied LIMIT
// `e.maxRows()*2` to the un-filtered JOIN and dropped any matching rows
// past that cut, so two queries that should agree on the inbound-edge
// count for one symbol returned different totals depending on the LIMIT
// vs the total edges in the graph.
//
// The repro pattern: many edges in the graph, more than `MaxRows*2`. With
// MaxRows=5 and 30 noise edges, the 5 real inbound edges to the target
// could all land past the cut and disappear from the result.
func TestIDPushdown_DenseGraphInboundCount(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	// Target symbol — the one we're filtering by.
	insertSym(t, db, "target", "Target", "Function", "Go")
	// 5 callers we want to see.
	for i := 0; i < 5; i++ {
		caller := callerID(i)
		insertSym(t, db, caller, caller, "Function", "Go")
		insertEdge(t, db, caller, "target", "CALLS")
	}
	// 60 noise edges between unrelated symbols — pushes the un-filtered
	// JOIN past MaxRows=5*2=10 by a wide margin.
	for i := 0; i < 60; i++ {
		fromID := "noise_from_" + intStr(i)
		toID := "noise_to_" + intStr(i)
		insertSym(t, db, fromID, fromID, "Function", "Go")
		insertSym(t, db, toID, toID, "Function", "Go")
		insertEdge(t, db, fromID, toID, "CALLS")
	}

	e := &Executor{DB: db, MaxRows: 5, ProjectID: "proj1"}

	cases := []struct {
		name  string
		query string
	}{
		{"id_filter_with_kind_filter", `MATCH (a)-[:CALLS]->(b) WHERE b.id="target" RETURN a.id`},
		{"id_filter_with_edge_var", `MATCH (a)-[r]->(b) WHERE b.id="target" RETURN a.id, r.kind`},
		{"id_filter_no_var_no_kind", `MATCH (a)-[]->(b) WHERE b.id="target" RETURN a.id`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := e.Execute(context.Background(), c.query)
			if err != nil {
				t.Fatalf("Execute(%q): %v", c.query, err)
			}
			if r.Total != 5 {
				t.Errorf("Total = %d, want 5 (id-filter must push to SQL so noise edges don't cut the result via LIMIT)", r.Total)
			}
		})
	}
}

func callerID(i int) string {
	return "caller_" + strconv.Itoa(i)
}

func intStr(i int) string {
	return strconv.Itoa(i)
}
