// SPDX-License-Identifier: MIT

package cypher

import (
	"context"
	"strings"
	"testing"
)

// #928: arithmetic operators aren't yet supported in WHERE/RETURN.
// Pre-fix the engine errored with bare "unsupported operator: -",
// which gave callers no path forward. #921's line-count audit
// template emits exactly the unsupported `(n.end_line - n.start_line)
// > N` shape, so this failure has a high-traffic blast radius.
// Surface a teaching error: name the limitation, link to #928,
// describe the workaround.

func TestOperatorHint_Arithmetic_TeachesWorkaround(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	// Note: `*` and `/` hit special tokenizer paths in pinchQL (HOPS for
	// variable-length matches; regex delimiter for =~) so the hint here
	// covers the two arithmetic ops that DO reach operatorHint cleanly.
	// The line-count audit shape (canonical #921 use case) uses `-`,
	// which is the high-traffic path.
	cases := []string{
		`MATCH (n:Function) WHERE n.end_line - n.start_line > 100 RETURN n.name`,
		`MATCH (n:Function) WHERE n.complexity + 5 > 10 RETURN n.name`,
	}
	for _, q := range cases {
		e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
		_, err := e.Execute(context.Background(), q)
		if err == nil {
			t.Errorf("expected error on arithmetic predicate %q; got nil", q)
			continue
		}
		msg := err.Error()
		if !strings.Contains(msg, "#928") {
			t.Errorf("error message should link to #928 for %q; got %q", q, msg)
		}
		if !strings.Contains(msg, "not yet supported") && !strings.Contains(msg, "arithmetic") {
			t.Errorf("error message should name the limitation for %q; got %q", q, msg)
		}
	}
}
