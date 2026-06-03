// SPDX-License-Identifier: MIT

package ast

import (
	"testing"
)

// #1148: C reserved keywords occasionally satisfy the funcRE shape
// `(?:\w+\s+)+name\s*\(` when they appear inside expressions like
// `n = sizeof(struct foo)` or column-0 statements after multi-word
// type prefixes. Pre-fix, the regex emitted `sizeof`/`struct`/etc.
// as Function symbols per occurrence; multiple matches in one file
// collided on `<mod>::sizeof` and reported `qualified_name_collision`
// in extraction_failures. Same shape as #69 (DEVICE_ATTR collision)
// at the keyword layer.
//
// Tests follow the four-case shape (#1152): positive (keyword
// dropped), negative (real-function-name preserved), control
// (Class/Setting kinds with keyword-shaped names pass through), and
// cross-check (each banned keyword is filtered, but a name CONTAINING
// the keyword as a substring is preserved).

// Positive: source with several keyword false-positives. Pre-fix
// these would be lifted to Function symbols and produce
// qualified_name_collision rows. Post-fix they're filtered out
// before the collision pass.
const cKeywordFalsePositiveSrc = `#include <stddef.h>

int real_function(int x) {
	size_t a = sizeof(struct foo);
	size_t b = sizeof(int);
	size_t c = offsetof(struct bar, field);
	return x + 1;
}

int another_real_function(void) {
	return 0;
}
`

func TestExtractC_KeywordsDroppedBeforeCollisionPass(t *testing.T) {
	result := Extract([]byte(cKeywordFalsePositiveSrc), "C", "src/kw_test.c")
	if result == nil {
		t.Fatal("nil result")
	}
	banned := map[string]struct{}{
		"sizeof": {}, "offsetof": {}, "typeof": {},
		"struct": {}, "union": {}, "enum": {},
		"if": {}, "else": {}, "for": {}, "while": {},
		"do": {}, "switch": {}, "case": {}, "default": {},
		"break": {}, "continue": {}, "return": {}, "goto": {},
	}
	for _, s := range result.Symbols {
		if s.Kind != "Function" {
			continue
		}
		if _, isBanned := banned[s.Name]; isBanned {
			t.Errorf("C reserved keyword %q emitted as Function symbol — should be filtered (qn=%q)",
				s.Name, s.QualifiedName)
		}
	}
}

// Positive: real-function-name source — names that LOOK keyword-ish
// but aren't actually keywords (e.g. `sizeof_thing`, `struct_init`)
// must NOT be dropped. Substring match would over-filter; the
// blocklist is exact-match only.
const cKeywordLookalikeSrc = `int sizeof_thing(int x) { return x; }
int struct_init(void) { return 0; }
int enum_count(int *e) { return *e; }
int return_value(int v) { return v; }
`

func TestExtractC_KeywordLookalikesPreserved(t *testing.T) {
	result := Extract([]byte(cKeywordLookalikeSrc), "C", "src/lookalike.c")
	if result == nil {
		t.Fatal("nil result")
	}
	want := map[string]bool{
		"sizeof_thing": false,
		"struct_init":  false,
		"enum_count":   false,
		"return_value": false,
	}
	for _, s := range result.Symbols {
		if s.Kind == "Function" {
			if _, expected := want[s.Name]; expected {
				want[s.Name] = true
			}
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("expected Function %q (keyword-prefix lookalike) preserved; got dropped",
				name)
		}
	}
}

// Negative (control): "real_function" + "another_real_function" from
// the positive test must survive — the filter touches keywords only.
func TestExtractC_KeywordFilterPreservesRealFunctions(t *testing.T) {
	result := Extract([]byte(cKeywordFalsePositiveSrc), "C", "src/kw_test.c")
	if result == nil {
		t.Fatal("nil result")
	}
	want := map[string]bool{
		"real_function":         false,
		"another_real_function": false,
	}
	for _, s := range result.Symbols {
		if s.Kind == "Function" {
			if _, expected := want[s.Name]; expected {
				want[s.Name] = true
			}
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("real function %q dropped by keyword filter (over-filter regression)",
				name)
		}
	}
}

// Cross-check: dropCKeywordFalsePositives operates on the symbol
// slice directly. Exercise it with a hand-built slice to confirm the
// exact-Name filter, the Kind != Function bypass, and the order
// preservation. Decouples from extractC's full pipeline so a future
// pipeline change can't accidentally silence this test.
func TestDropCKeywordFalsePositives_Direct(t *testing.T) {
	in := []ExtractedSymbol{
		{Name: "real_func", Kind: "Function", QualifiedName: "m::real_func"},
		{Name: "sizeof", Kind: "Function", QualifiedName: "m::sizeof"},
		{Name: "struct", Kind: "Function", QualifiedName: "m::struct"},
		// Kind = Class — keyword-named but a different kind. Pass through.
		{Name: "struct", Kind: "Class", QualifiedName: "m::struct"},
		{Name: "another_real", Kind: "Function", QualifiedName: "m::another_real"},
	}
	out := dropCKeywordFalsePositives(in)
	wantNames := []string{"real_func", "struct", "another_real"}
	if len(out) != len(wantNames) {
		t.Fatalf("len(out) = %d, want %d; out names: %v",
			len(out), len(wantNames), namesOf(out))
	}
	for i, s := range out {
		if s.Name != wantNames[i] {
			t.Errorf("out[%d].Name = %q, want %q", i, s.Name, wantNames[i])
		}
	}
	// Sanity: the Class struct survived — it's an unrelated symbol
	// with a coincidentally keyword-shaped name; the filter is
	// Function-only.
	hasClass := false
	for _, s := range out {
		if s.Kind == "Class" && s.Name == "struct" {
			hasClass = true
		}
	}
	if !hasClass {
		t.Error("Class kind with keyword-shaped name was incorrectly dropped — filter should be Function-only")
	}
}

func namesOf(syms []ExtractedSymbol) []string {
	out := make([]string, 0, len(syms))
	for _, s := range syms {
		out = append(out, s.Name)
	}
	return out
}
