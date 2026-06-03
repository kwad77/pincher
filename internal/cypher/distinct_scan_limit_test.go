// SPDX-License-Identifier: MIT

package cypher

import (
	"sort"
	"strings"
	"testing"
)

// #929: DISTINCT was applied AFTER the safety scan LIMIT. With
// `MaxRows=100` (scan cap = 200), inserting > 200 rows whose kind
// values cluster alphabetically late meant `RETURN DISTINCT n.kind
// ORDER BY n.kind LIMIT 100` returned only the alphabetically-first
// few kinds — the SQL prefix-sorted, took the first 200, and DISTINCT
// in Go collapsed those 200 down to 4 of 15 kinds. The fix skips the
// safety LIMIT when q.distinct is set so DISTINCT runs on the full
// match set.

func TestDistinctScan_ReturnsAllKindsRegardlessOfRowCount(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	// Seed > 200 rows (MaxRows*2 = 200) across 8 different kinds so the
	// pre-fix path would silently drop most of them.
	kinds := []string{"Alpha", "Beta", "Charlie", "Delta", "Echo", "Foxtrot", "Golf", "Hotel"}
	id := 0
	for k, kind := range kinds {
		// 30 rows per kind = 240 total → exceeds maxRows*2=200
		_ = k
		for r := 0; r < 30; r++ {
			id++
			insertSym(t, db, idStr(id), nameStr(id), kind, "Go")
		}
	}

	r := exec(t, db, "MATCH (n) RETURN DISTINCT n.kind ORDER BY n.kind")

	got := map[string]bool{}
	for _, row := range r.Rows {
		if k, ok := row["n.kind"].(string); ok {
			got[k] = true
		}
	}
	if len(got) != len(kinds) {
		var have []string
		for k := range got {
			have = append(have, k)
		}
		sort.Strings(have)
		t.Errorf("DISTINCT n.kind returned %d kinds, want %d. Got: %v. Wanted all of: %v",
			len(got), len(kinds), have, kinds)
	}
	for _, want := range kinds {
		if !got[want] {
			t.Errorf("DISTINCT n.kind missing %q", want)
		}
	}
}

// Regression for the exact ORDER BY interaction the bug surfaced
// through: with ORDER BY ASC LIMIT 100, pre-fix the SQL fetched the
// alphabetically-first 200 rows and DISTINCT showed only Alpha + Beta
// + Charlie (the first 90 rows of the seed). Post-fix the in-Go
// DISTINCT runs on the full match set and returns all kinds in
// alphabetical order.
func TestDistinctScan_OrderByLimit_DoesNotDropKinds(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	kinds := []string{"Alpha", "Beta", "Charlie", "Delta", "Echo", "Foxtrot", "Golf", "Hotel"}
	id := 0
	for _, kind := range kinds {
		for r := 0; r < 30; r++ {
			id++
			insertSym(t, db, idStr(id), nameStr(id), kind, "Go")
		}
	}
	r := exec(t, db, "MATCH (n) RETURN DISTINCT n.kind ORDER BY n.kind LIMIT 100")
	got := []string{}
	for _, row := range r.Rows {
		if k, ok := row["n.kind"].(string); ok {
			got = append(got, k)
		}
	}
	// Expect all 8 kinds, in ascending alphabetical order.
	if len(got) != 8 {
		t.Fatalf("LIMIT 100 with 8 distinct kinds returned %d rows; want 8. Got: %v", len(got), got)
	}
	if !sort.StringsAreSorted(got) {
		t.Errorf("results not in ascending order: %v", got)
	}
	if !strings.EqualFold(got[0], "Alpha") || !strings.EqualFold(got[len(got)-1], "Hotel") {
		t.Errorf("expected Alpha first and Hotel last; got first=%q last=%q", got[0], got[len(got)-1])
	}
}

func idStr(i int) string {
	return "row-" + itoa(i)
}

func nameStr(i int) string {
	return "name-" + itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [10]byte
	bp := len(buf)
	for i > 0 {
		bp--
		buf[bp] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[bp:])
}
