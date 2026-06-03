// SPDX-License-Identifier: MIT

package cypher

import (
	"context"
	"database/sql"
	"testing"
)

// #421: WHERE n.is_entry_point="1" returned 0 rows because is_entry_point
// wasn't mapped in cypherPropToCol — it post-filtered in Go where
// fmt.Sprint(true) is "true", not "1". The bind-arg comparison failed
// silently. Now is_entry_point and is_exported are SQL-pushed (so SQLite
// affinity does the coercion against the INTEGER column), and the in-Go
// fallback uses boolCoerceEqual so all four bool spellings match.

func TestBoolCoercion_IsEntryPoint_StringOne(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSymWithEntryPoint(t, db, "main1", "main", true)
	insertSymWithEntryPoint(t, db, "helper", "helper", false)

	for _, lit := range []string{`"1"`, `"true"`, `1`, `true`, `TRUE`} {
		t.Run(lit, func(t *testing.T) {
			r := exec(t, db, `MATCH (n:Function) WHERE n.is_entry_point=`+lit+` RETURN n.name`)
			if r.Total != 1 {
				t.Fatalf("WHERE n.is_entry_point=%s should match the entry point row, got %d rows", lit, r.Total)
			}
		})
	}
}

func TestBoolCoercion_IsEntryPoint_StringZero(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSymWithEntryPoint(t, db, "main1", "main", true)
	insertSymWithEntryPoint(t, db, "helper", "helper", false)
	insertSymWithEntryPoint(t, db, "util", "util", false)

	for _, lit := range []string{`"0"`, `"false"`, `0`, `false`, `FALSE`} {
		t.Run(lit, func(t *testing.T) {
			r := exec(t, db, `MATCH (n:Function) WHERE n.is_entry_point=`+lit+` RETURN n.name`)
			if r.Total != 2 {
				t.Fatalf("WHERE n.is_entry_point=%s should match the 2 non-entry rows, got %d", lit, r.Total)
			}
		})
	}
}

// In-Go path: NOT (...) wraps the leaf so the AND-chain pushdown
// emits `AND NOT (col=?)`. boolCoerceEqual is exercised when the
// row's bool value is fmt.Sprint(true)="true" against c.value="1".
func TestBoolCoercion_NotEqualsOne(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSymWithEntryPoint(t, db, "main1", "main", true)
	insertSymWithEntryPoint(t, db, "helper", "helper", false)
	insertSymWithEntryPoint(t, db, "util", "util", false)

	r := exec(t, db, `MATCH (n:Function) WHERE n.is_entry_point<>"1" RETURN n.name`)
	if r.Total != 2 {
		t.Fatalf("WHERE n.is_entry_point<>'1' should match the 2 non-entry rows, got %d", r.Total)
	}
}

// SQL pushdown sanity: an OR over the boolean column should not
// undercount past the LIMIT clamp now that the column is mapped.
func TestBoolCoercion_OR_AcrossLimitClamp(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	for i := 0; i < 250; i++ {
		insertSymWithEntryPoint(t, db, padInt(i)+"_n", "noise", false)
	}
	insertSymWithEntryPoint(t, db, "main_late", "main", true)

	r := exec(t, db, `MATCH (n:Function) WHERE n.is_entry_point=true OR n.name="never_matches" RETURN n.name`)
	if r.Total != 1 {
		t.Fatalf("expected 1 entry-point row past the LIMIT clamp, got %d", r.Total)
	}
}

func boolCoerceEqualHelper(t *testing.T, want bool, a, b string) {
	t.Helper()
	got := boolCoerceEqual(a, b)
	if got != want {
		t.Errorf("boolCoerceEqual(%q, %q) = %v, want %v", a, b, got, want)
	}
}

func TestBoolCoerceEqual_Matrix(t *testing.T) {
	// All true forms equal all true forms; same for false.
	trueForms := []string{"1", "true", "TRUE"}
	falseForms := []string{"0", "false", "FALSE"}
	for _, a := range trueForms {
		for _, b := range trueForms {
			boolCoerceEqualHelper(t, true, a, b)
		}
		for _, b := range falseForms {
			boolCoerceEqualHelper(t, false, a, b)
		}
	}
	for _, a := range falseForms {
		for _, b := range falseForms {
			boolCoerceEqualHelper(t, true, a, b)
		}
	}
	// Non-bool strings never coerce.
	boolCoerceEqualHelper(t, false, "Apple", "true")
	boolCoerceEqualHelper(t, false, "1", "Apple")
	boolCoerceEqualHelper(t, false, "", "0")
}

func insertSymWithEntryPoint(t *testing.T, db *sql.DB, id, name string, entry bool) {
	t.Helper()
	v := 0
	if entry {
		v = 1
	}
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO symbols(id, project_id, file_path, name, qualified_name, kind, language,
			start_byte, end_byte, start_line, end_line, is_entry_point) VALUES (?,?,?,?,?,?,?,0,100,1,5,?)`,
		id, "proj1", "f.go", name, name, "Function", "Go", v,
	)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
}
