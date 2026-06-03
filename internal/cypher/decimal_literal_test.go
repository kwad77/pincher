// SPDX-License-Identifier: MIT

package cypher

import (
	"context"
	"testing"
)

// #752: the tokenizer's number scanner consumed digits only, so a
// decimal literal `0.5` tokenized as NUMBER(0) PUNCT(.) NUMBER(5).
// The WHERE parser then read `< 0.5` as a column reference `0.5`, and
// `WHERE r.confidence < 0.5` was misclassified as a column-vs-column
// comparison and silently ignored.
func TestTokenize_DecimalLiteral(t *testing.T) {
	cases := []struct {
		in   string
		want []string // expected token values in order
	}{
		{"0.5", []string{"0.5"}},
		{"1.0", []string{"1.0"}},
		{"42", []string{"42"}},
		{"0.71", []string{"0.71"}},
		{"n.complexity > 30", []string{"n", ".", "complexity", ">", "30"}},
		{"r.confidence < 0.5", []string{"r", ".", "confidence", "<", "0.5"}},
		// A trailing dot with no fractional digit stays a separate PUNCT.
		{"10.", []string{"10", "."}},
		// `1..3` (hop-range shape) must NOT be eaten as a decimal.
		{"1..3", []string{"1", ".", ".", "3"}},
	}
	for _, c := range cases {
		toks := tokenize(c.in)
		got := make([]string, len(toks))
		for i, tk := range toks {
			got[i] = tk.value
		}
		if len(got) != len(c.want) {
			t.Errorf("tokenize(%q) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("tokenize(%q)[%d] = %q, want %q (full: %v)", c.in, i, got[i], c.want[i], got)
			}
		}
	}
}

// #752 integration: WHERE on an edge property compared to a decimal
// literal must actually filter, not be silently dropped.
func TestExecute_EdgeConfidenceDecimalComparison(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "caller", "Caller", "Function", "Go")
	insertSym(t, db, "lowTgt", "LowTarget", "Function", "Go")
	insertSym(t, db, "hiTgt", "HiTarget", "Function", "Go")
	// Two CALLS edges from Caller: one low-confidence (0.4), one high (0.9).
	if _, err := db.Exec(
		`INSERT INTO edges(project_id, from_id, to_id, kind, confidence) VALUES ('proj1','caller','lowTgt','CALLS',0.4)`,
	); err != nil {
		t.Fatalf("insert low edge: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO edges(project_id, from_id, to_id, kind, confidence) VALUES ('proj1','caller','hiTgt','CALLS',0.9)`,
	); err != nil {
		t.Fatalf("insert hi edge: %v", err)
	}

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	r, err := e.Execute(context.Background(),
		`MATCH (a)-[rel:CALLS]->(b) WHERE rel.confidence < 0.5 RETURN b.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// Must NOT be misclassified as a column-vs-column comparison.
	for _, w := range r.Warnings {
		if contains(w, "column-vs-column") {
			t.Errorf("decimal literal misclassified as column-vs-column: %q", w)
		}
	}
	if len(r.Rows) != 1 {
		t.Fatalf("rows=%d, want 1 (only the confidence-0.4 edge passes < 0.5)", len(r.Rows))
	}
	if name, _ := r.Rows[0]["b.name"].(string); name != "LowTarget" {
		t.Errorf("row b.name = %q, want LowTarget", name)
	}
}
