// SPDX-License-Identifier: MIT

package cypher

import (
	"database/sql"
	"testing"
)

// #323: WHERE n.is_exported = true used to return 0 rows because
// the tokenizer emitted "TRUE" while Go's bool formatting yielded
// "true". The condition compared the two strings literal — failed.

// insertSymWithExported is a test-local helper: same shape as
// insertSym but lets us flip the is_exported column.
func insertSymWithExported(t *testing.T, db *sql.DB, id, name, kind string, exported bool) {
	t.Helper()
	exp := 0
	if exported {
		exp = 1
	}
	_, err := db.Exec(
		`INSERT INTO symbols(id, project_id, file_path, name, qualified_name, kind, language,
			start_byte, end_byte, start_line, end_line, is_exported)
		 VALUES (?, 'proj1', 'file.go', ?, ?, ?, 'Go', 0, 100, 1, 5, ?)`,
		id, name, name, kind, exp,
	)
	if err != nil {
		t.Fatalf("insert symbol %q: %v", id, err)
	}
}

// Lowercase boolean literal works.
func TestNodeScan_BooleanEquality_LowercaseTrue(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSymWithExported(t, db, "id-a", "Apple", "Function", true)
	insertSymWithExported(t, db, "id-b", "Banana", "Function", false)

	r := exec(t, db, "MATCH (n:Function) WHERE n.is_exported = true RETURN n.name")
	if r.Total != 1 {
		t.Errorf("Total = %d, want 1 (Apple is the only exported function)", r.Total)
	}
	if r.Total > 0 && r.Rows[0]["n.name"] != "Apple" {
		t.Errorf("Rows[0].name = %v, want Apple", r.Rows[0]["n.name"])
	}
}

// Uppercase TRUE works (case-insensitive on the boolean keyword).
func TestNodeScan_BooleanEquality_UppercaseTRUE(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSymWithExported(t, db, "id-a", "Apple", "Function", true)
	insertSymWithExported(t, db, "id-b", "Banana", "Function", false)

	r := exec(t, db, "MATCH (n:Function) WHERE n.is_exported = TRUE RETURN n.name")
	if r.Total != 1 {
		t.Errorf("Total = %d, want 1 (TRUE is case-fold equivalent to true)", r.Total)
	}
}

// `false` correctly identifies the unexported set.
func TestNodeScan_BooleanEquality_False(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSymWithExported(t, db, "id-a", "Apple", "Function", true)
	insertSymWithExported(t, db, "id-b", "Banana", "Function", false)
	insertSymWithExported(t, db, "id-c", "Cherry", "Function", false)

	r := exec(t, db, "MATCH (n:Function) WHERE n.is_exported = false RETURN n.name")
	if r.Total != 2 {
		t.Errorf("Total = %d, want 2 (Banana + Cherry)", r.Total)
	}
}

// String equality stays case-sensitive — the case-fold is scoped
// to boolean literals, not arbitrary strings.
func TestNodeScan_StringEquality_StillCaseSensitive(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "id-a", "Apple", "Function", "Go")
	insertSym(t, db, "id-b", "apple", "Function", "Go")

	r := exec(t, db, "MATCH (n:Function) WHERE n.name = 'Apple' RETURN n.name")
	if r.Total != 1 {
		t.Errorf("Total = %d, want 1 (case-sensitive name match)", r.Total)
	}
}

// normalizeConditionValue helper coverage.
func TestNormalizeConditionValue(t *testing.T) {
	cases := []struct {
		kind, in, want string
	}{
		// #421: TRUE/FALSE map to SQLite INTEGER form so the SQL
		// pushdown path matches against bool columns under affinity.
		// In-Go evaluator handles the same equivalence via
		// boolCoerceEqual.
		{"KEYWORD", "TRUE", "1"},
		{"KEYWORD", "FALSE", "0"},
		{"KEYWORD", "NULL", ""},
		// Non-boolean keywords pass through unchanged (the parser uses
		// them for control flow on the syntactic path, not as values).
		{"KEYWORD", "MATCH", "MATCH"},
		// String tokens are values — case must be preserved.
		{"STRING", "Apple", "Apple"},
		// Identifiers (rare on the value path but possible) pass through.
		{"IDENT", "Foo", "Foo"},
	}
	for _, c := range cases {
		got := normalizeConditionValue(token{kind: c.kind, value: c.in})
		if got != c.want {
			t.Errorf("normalizeConditionValue({%s, %q}) = %q, want %q",
				c.kind, c.in, got, c.want)
		}
	}
}
