// SPDX-License-Identifier: MIT

package ast

import "testing"

// #1783: methods in separate `impl` blocks on the same Rust type used
// to collide on qualified_name — `impl Debug for Foo` and `impl Display
// for Foo` both scoped their `fmt` method to `Foo`, producing two
// `Foo::fmt` symbols (359 such collisions on a real Rust corpus). A
// trait-impl now QNs its methods as `Type::Trait::method`; the bare
// `Type` stays the Parent.

func TestExtractRust_TraitImplMethods_DistinctQN_1783(t *testing.T) {
	t.Parallel()
	src := []byte(`pub struct Widget {
    id: i32,
}

impl Widget {
    pub fn build(&self) -> i32 {
        self.id
    }
}

impl Debug for Widget {
    fn fmt(&self) -> i32 {
        1
    }
}

impl Display for Widget {
    fn fmt(&self) -> i32 {
        2
    }
}
`)
	r := Extract(src, "Rust", "widgets.rs")
	if r == nil {
		t.Fatal("nil result")
	}

	var fmtQNs []string
	var fmtParents []string
	buildQN := ""
	for _, s := range r.Symbols {
		switch s.Name {
		case "fmt":
			if s.Kind != "Method" {
				t.Errorf("fmt kind = %q, want Method", s.Kind)
			}
			fmtQNs = append(fmtQNs, s.QualifiedName)
			fmtParents = append(fmtParents, s.Parent)
		case "build":
			buildQN = s.QualifiedName
			if s.Parent != "widgets::Widget" {
				t.Errorf("build parent = %q, want widgets::Widget", s.Parent)
			}
		}
	}

	// Both fmt methods must be emitted (pre-fix: same QN → one dropped
	// downstream as qualified_name_collision).
	if len(fmtQNs) != 2 {
		t.Fatalf("expected 2 fmt methods extracted, got %d (%v)", len(fmtQNs), fmtQNs)
	}
	// Their QNs must be distinct — the whole point of the fix.
	if fmtQNs[0] == fmtQNs[1] {
		t.Errorf("#1783: the two fmt methods still share a QN %q — trait-impl disambiguation failed", fmtQNs[0])
	}
	// And distinct in the right way: Type::Trait::method.
	want := map[string]bool{
		"widgets::Widget::Debug::fmt":   true,
		"widgets::Widget::Display::fmt": true,
	}
	for _, qn := range fmtQNs {
		if !want[qn] {
			t.Errorf("fmt QN = %q; want one of widgets::Widget::Debug::fmt / widgets::Widget::Display::fmt", qn)
		}
	}
	// Parent stays the bare type for both — the trait lives only in the QN.
	for _, p := range fmtParents {
		if p != "widgets::Widget" {
			t.Errorf("fmt parent = %q, want widgets::Widget (the trait belongs in the QN, not the Parent)", p)
		}
	}
	// Control: an inherent `impl Widget` method is unchanged — no trait
	// segment in its QN.
	if buildQN != "widgets::Widget::build" {
		t.Errorf("inherent-impl method build QN = %q, want widgets::Widget::build (unchanged)", buildQN)
	}
}
