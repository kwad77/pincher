// SPDX-License-Identifier: MIT

package cypher

import (
	"context"
	"testing"
)

// #775: the tokenizer scanned `\` as an escape (so `\"` didn't end the
// literal early) but never unescaped the value — so a Windows path
// literal compared as the raw double-backslash string and never matched
// the single-backslash stored value.

func TestUnescapeString(t *testing.T) {
	cases := []struct{ in, want string }{
		{`no escapes`, `no escapes`},
		{`C:\\Users\\proj`, `C:\Users\proj`},      // the headline Windows-path case
		{`a\"b`, `a"b`},                           // escaped double quote
		{`a\'b`, `a'b`},                           // escaped single quote
		{`line\nbreak`, "line\nbreak"},            // \n -> newline
		{`tab\there`, "tab\there"},                // \t -> tab
		{`regex\d+`, `regex\d+`},                  // unrecognised escape kept verbatim (regex survives)
		{`trailing\`, `trailing\`},                // dangling backslash kept as-is, no panic
		{`\\`, `\`},                               // bare double backslash
	}
	for _, c := range cases {
		if got := unescapeString(c.in); got != c.want {
			t.Errorf("unescapeString(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Exact-match WHERE on a backslash-containing value must work end to end
// — tokenize the pinchQL literal, unescape it, compare against the
// stored value. The query is a Go raw string so `\\` reaches the
// tokenizer as two literal backslash chars, exactly as a user would
// type it.
func TestExecute_WhereExactMatch_BackslashPath(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	// insertSymInProject stores its 2nd arg as project_id verbatim.
	insertSymInProject(t, db, "w1", `C:\Users\kev\proj`, "Target", "Function")
	insertSymInProject(t, db, "o1", `C:\Users\kev\other`, "Decoy", "Function")

	e := &Executor{DB: db, MaxRows: 100, AllowAllProjects: true}
	r, err := e.Execute(context.Background(),
		`MATCH (n:Function) WHERE n.project_id = "C:\\Users\\kev\\proj" RETURN n.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(r.Rows) != 1 {
		t.Fatalf("rows=%d, want 1 — exact-match WHERE on a backslash path must resolve", len(r.Rows))
	}
	if name, _ := r.Rows[0]["n.name"].(string); name != "Target" {
		t.Errorf("matched %q, want Target", name)
	}
}
