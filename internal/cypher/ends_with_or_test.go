// SPDX-License-Identifier: MIT

package cypher

import (
	"context"
	"database/sql"
	"testing"
)

// #430: ENDS WITH used in an OR-chain returned 0 rows even when each
// branch matched independently. Reproduce the failure from the issue:
// MATCH (n) WHERE n.file_path ENDS WITH ".js"
//                OR n.file_path ENDS WITH ".jsx"
//                OR n.file_path ENDS WITH ".ts"
// must surface the .js, .jsx, .ts rows. Single-branch ENDS WITH already
// works, equality OR-chain already works (#358) — the regression is on
// the OR + non-equality combination.
func TestExecute_ENDS_WITH_OR_Chain(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSymWithFile(t, db, "f1", "Comp", "Function", "JavaScript", "src/Comp.js")
	insertSymWithFile(t, db, "f2", "App", "Function", "JavaScript", "src/App.jsx")
	insertSymWithFile(t, db, "f3", "Util", "Function", "TypeScript", "src/Util.ts")
	insertSymWithFile(t, db, "f4", "Mod", "Function", "Go", "internal/mod.go")
	insertSymWithFile(t, db, "f5", "Other", "Function", "Python", "tools/other.py")

	q := `MATCH (n) WHERE n.file_path ENDS WITH ".js" ` +
		`OR n.file_path ENDS WITH ".jsx" ` +
		`OR n.file_path ENDS WITH ".ts" ` +
		`RETURN n.file_path`

	r := exec(t, db, q)
	if r.Total != 3 {
		t.Fatalf("expected 3 rows for ENDS WITH OR-chain, got %d (rows=%v)", r.Total, r.Rows)
	}
	got := map[string]bool{}
	for _, row := range r.Rows {
		got[row["n.file_path"].(string)] = true
	}
	if !got["src/Comp.js"] || !got["src/App.jsx"] || !got["src/Util.ts"] {
		t.Errorf("expected all three of .js/.jsx/.ts in result, got %v", got)
	}
	if got["internal/mod.go"] || got["tools/other.py"] {
		t.Errorf("non-matching files leaked through: %v", got)
	}
}

// #430 narrower repro: the live failure surfaced when ONE branch
// matched and the others matched nothing (.jsx, .ts didn't exist in
// the test corpus). The first 3-branch test above seeds rows for
// every branch — that path passes. This one seeds only the .js
// branch, which is what the issue actually reported.
func TestExecute_ENDS_WITH_OR_OnlyOneBranchHasMatches(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSymWithFile(t, db, "f1", "Comp", "Function", "JavaScript", "src/Comp.js")
	insertSymWithFile(t, db, "f2", "App2", "Function", "JavaScript", "src/App.js")
	insertSymWithFile(t, db, "f3", "Mod", "Function", "Go", "internal/mod.go")

	q := `MATCH (n) WHERE n.file_path ENDS WITH ".js" ` +
		`OR n.file_path ENDS WITH ".jsx" ` +
		`OR n.file_path ENDS WITH ".ts" ` +
		`RETURN n.file_path`

	r := exec(t, db, q)
	if r.Total != 2 {
		t.Fatalf("expected 2 .js rows when other OR branches are empty, got %d (rows=%v)", r.Total, r.Rows)
	}
}

// #430 EXACT repro from the issue — narrows the trigger from "OR has
// non-equality op" to "OR where every branch happens to match the
// SAME row set (or in the live case, where the structural pushdown
// path collapses an empty branch into a global zero)". Seed lots of
// .js files, NO .jsx / .ts; expect the .js rows back.
func TestExecute_ENDS_WITH_OR_RightBranchesAllEmpty(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	for i := 0; i < 5; i++ {
		insertSymWithFile(t, db, "js"+string(rune('a'+i)), "JS", "Variable", "JavaScript", "plugin/scripts/install.js")
	}
	insertSymWithFile(t, db, "g1", "GoFunc", "Function", "Go", "internal/mod.go")

	q := `MATCH (n) WHERE n.file_path ENDS WITH ".js" ` +
		`OR n.file_path ENDS WITH ".jsx" ` +
		`OR n.file_path ENDS WITH ".ts" ` +
		`OR n.file_path ENDS WITH ".tsx" ` +
		`RETURN n.file_path LIMIT 10`

	r := exec(t, db, q)
	if r.Total != 5 {
		t.Fatalf("expected 5 .js rows when other OR branches match nothing in the corpus, got %d (rows=%v)", r.Total, r.Rows)
	}
}

// Mirror the bug for STARTS WITH (likely sibling under the same
// pushdown path) and CONTAINS — both operators that emit `LIKE` SQL
// and so plausibly take the same in-Go fallback path.
func TestExecute_STARTS_WITH_OR_Chain(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSymWithFile(t, db, "f1", "A", "Function", "Go", "alpha/x.go")
	insertSymWithFile(t, db, "f2", "B", "Function", "Go", "beta/x.go")
	insertSymWithFile(t, db, "f3", "C", "Function", "Go", "gamma/x.go")

	r := exec(t, db, `MATCH (n) WHERE n.file_path STARTS WITH "alpha" OR n.file_path STARTS WITH "beta" RETURN n.file_path`)
	if r.Total != 2 {
		t.Fatalf("expected 2 rows for STARTS WITH OR-chain, got %d (rows=%v)", r.Total, r.Rows)
	}
}

func TestExecute_CONTAINS_OR_Chain(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSymWithFile(t, db, "f1", "A", "Function", "Go", "alpha/x.go")
	insertSymWithFile(t, db, "f2", "B", "Function", "Go", "beta/x.go")
	insertSymWithFile(t, db, "f3", "C", "Function", "Go", "gamma/x.go")

	r := exec(t, db, `MATCH (n) WHERE n.file_path CONTAINS "alpha" OR n.file_path CONTAINS "beta" RETURN n.file_path`)
	if r.Total != 2 {
		t.Fatalf("expected 2 rows for CONTAINS OR-chain, got %d (rows=%v)", r.Total, r.Rows)
	}
}

// #430 root-cause repro: the bug only manifests when the matching
// rows fall PAST the SQL LIMIT clamp (`maxRows()*2`). For an OR
// query the engine pre-fix didn't push the WHERE to SQL — it
// scanned up to the clamp, then filtered in Go. With 4000 symbols
// in pincher-repo and the .js rows late in the table, they never
// made it into the row loop. Seed > maxRows()*2 noise rows BEFORE
// the matching row to verify the SQL pushdown actually fires.
func TestExecute_OR_RowsBeyondLimitClamp(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	// Seed > maxRows()*2 = 200 noise rows. Default test executor uses
	// MaxRows=100 so the clamp is 200. Insert noise first so the
	// matching row sorts late by primary key.
	for i := 0; i < 250; i++ {
		insertSymWithFile(t, db, "noise"+padInt(i), "Noise", "Function", "Go", "internal/noise.go")
	}
	insertSymWithFile(t, db, "zzz_match", "Match", "Variable", "JavaScript", "plugin/scripts/install.js")

	q := `MATCH (n) WHERE n.file_path ENDS WITH ".js" ` +
		`OR n.file_path ENDS WITH ".jsx" ` +
		`OR n.file_path ENDS WITH ".ts" ` +
		`RETURN n.file_path`

	r := exec(t, db, q)
	if r.Total != 1 {
		t.Fatalf("expected 1 row past the LIMIT clamp, got %d. Pre-#430 the SQL scan capped at 200 rows so matching .js rows past that cap were invisible.", r.Total)
	}
}

func padInt(i int) string {
	s := ""
	for i > 0 {
		s = string(rune('a'+(i%10))) + s
		i /= 10
	}
	if s == "" {
		s = "a"
	}
	return s
}

func insertSymWithFile(t *testing.T, db *sql.DB, id, name, kind, lang, filePath string) {
	t.Helper()
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO symbols(id, project_id, file_path, name, qualified_name, kind, language,
			start_byte, end_byte, start_line, end_line) VALUES (?,?,?,?,?,?,?, 0,100,1,5)`,
		id, "proj1", filePath, name, name, kind, lang,
	)
	if err != nil {
		t.Fatalf("insert symbol %q: %v", id, err)
	}
}
