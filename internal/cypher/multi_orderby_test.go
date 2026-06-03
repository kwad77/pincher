// SPDX-License-Identifier: MIT

package cypher

import (
	"strings"
	"testing"
)

// #883: a multi-column ORDER BY (`ORDER BY a, b`) used to fall through
// the parser's clause-keyword catch-all and surface as "unexpected
// token ',' — expected WHERE, RETURN, ORDER BY, LIMIT", which
// actively misleads — the real constraint is "multi-column ORDER BY
// isn't supported." Now the parser rejects the trailing comma with a
// remediation pointer, matching the #871 / #433 pattern for explicitly-
// rejected pinchQL shapes.
func TestParse_MultiColumnOrderBy_RejectsWithRemediation(t *testing.T) {
	_, err := parse(`MATCH (n:Function) RETURN n.name ORDER BY n.language ASC, n.complexity DESC`)
	if err == nil {
		t.Fatal("expected parse error rejecting multi-column ORDER BY; got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "multi-column ORDER BY") {
		t.Errorf("error should name 'multi-column ORDER BY'; got %q", msg)
	}
	if !strings.Contains(msg, "single") || !strings.Contains(msg, "client-side") {
		t.Errorf("error should point at the single-column / client-side remediation; got %q", msg)
	}
}

// Control: a single-column ORDER BY still parses.
func TestParse_SingleColumnOrderBy_Parses(t *testing.T) {
	q, err := parse(`MATCH (n:Function) RETURN n.name ORDER BY n.complexity DESC`)
	if err != nil {
		t.Fatalf("single-column ORDER BY must parse; got %v", err)
	}
	if q.orderBy != "n.complexity" || q.orderDir != "DESC" {
		t.Errorf("expected orderBy=n.complexity DESC, got %q %q", q.orderBy, q.orderDir)
	}
}
