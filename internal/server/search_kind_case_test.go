// SPDX-License-Identifier: MIT

package server

import (
	"strings"
	"testing"
)

// #910: parallel to #902 (language case). The kind filter is also
// case-sensitive at the DB layer; pre-fix `kind=FuNcTiOn` recommended
// "drop the filter" — which over-broadens to all kinds. The right
// move is to teach the canonical case.

func TestVerifyEmptySearchCause_KindWrongCase_SuggestsCanonicalCase(t *testing.T) {
	t.Parallel()
	relaxer := &fakeRelaxer{counts: map[string]int{
		"handleSearch|Function||": 1, // canonical case finds the match
		"handleSearch|||":         1, // no filter finds the match too
	}}
	cause, steps, ok := verifyEmptySearchCause(
		"handleSearch", "FuNcTiOn", "", "", 0, 0, relaxer.relax(),
	)
	if !ok {
		t.Fatal("verifier returned ok=false")
	}
	if !strings.Contains(cause, `kind="FuNcTiOn"`) {
		t.Errorf("cause must name the user's input case; got %q", cause)
	}
	if !strings.Contains(cause, "wrong case") {
		t.Errorf("cause must say wrong case; got %q", cause)
	}
	if !strings.Contains(cause, `"Function"`) {
		t.Errorf("cause must name the canonical form; got %q", cause)
	}
	if len(steps) == 0 {
		t.Fatal("steps empty")
	}
	if !strings.Contains(steps[0]["args"], `"kind":"Function"`) {
		t.Errorf("next_step must suggest canonical kind; got %q", steps[0]["args"])
	}
}

// Unknown kind (genuinely typo'd, not case): fall back to "drop the
// filter" — there's no canonical form to recover.
func TestVerifyEmptySearchCause_UnknownKind_FallsBackToDrop(t *testing.T) {
	t.Parallel()
	relaxer := &fakeRelaxer{counts: map[string]int{
		"handleSearch|||": 2, // dropping the filter finds matches
	}}
	cause, steps, ok := verifyEmptySearchCause(
		"handleSearch", "BogusKind", "", "", 0, 0, relaxer.relax(),
	)
	if !ok {
		t.Fatal("verifier returned ok=false")
	}
	if !strings.Contains(cause, "drop the kind filter") {
		t.Errorf("unknown-kind cause should fall back to drop-filter advice; got %q", cause)
	}
	if len(steps) == 0 {
		t.Fatal("steps empty")
	}
	if strings.Contains(steps[0]["args"], `"kind"`) {
		t.Errorf("fallback next_step must drop the kind; got %q", steps[0]["args"])
	}
}

func TestCanonicalKindCase(t *testing.T) {
	cases := []struct{ in, want string }{
		{"function", "Function"},
		{"FUNCTION", "Function"},
		{"FuNcTiOn", "Function"},
		{"Function", "Function"},
		{"method", "Method"},
		{"class", "Class"},
		{"interface", "Interface"},
		{"setting", "Setting"},
		{"section", "Section"},
		{"datasource", "DataSource"},
		{"BogusKind", ""},
		{"", ""},
	}
	for _, c := range cases {
		got := canonicalKindCase(c.in)
		if got != c.want {
			t.Errorf("canonicalKindCase(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
