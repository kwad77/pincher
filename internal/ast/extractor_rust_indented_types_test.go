// SPDX-License-Identifier: MIT

package ast

import "testing"

// #1757: rustRE's classRE / interfaceRE / enumRE / scopeRE / importRE
// anchored at column 0 with a bare `^`, so a struct / trait / enum /
// impl / use inside a `mod { … }` block (indented) was never
// extracted. #1183 had added the leading-whitespace tolerance to
// funcRE only. These tests pin every Rust type kind inside a module.

// TestExtractRust_TypesInsideModBlock is the headline fix: struct /
// enum / trait declared inside `mod { … }` must extract.
func TestExtractRust_TypesInsideModBlock(t *testing.T) {
	t.Parallel()
	src := []byte(`pub struct TopLevel {
    x: i32,
}

mod shapes {
    pub struct Circle {
        r: f64,
    }

    pub enum Color {
        Red,
        Green,
    }

    pub trait Drawable {
        fn draw(&self);
    }
}
`)
	r := Extract(src, "Rust", "shapes.rs")
	if r == nil {
		t.Fatal("nil result")
	}
	kind := map[string]string{}
	for _, s := range r.Symbols {
		kind[s.Name] = s.Kind
	}
	if kind["TopLevel"] != "Class" {
		t.Errorf("TopLevel kind = %q, want Class", kind["TopLevel"])
	}
	if kind["Circle"] != "Class" {
		t.Errorf("Circle (struct in mod) kind = %q, want Class", kind["Circle"])
	}
	if kind["Color"] != "Enum" {
		t.Errorf("Color (enum in mod) kind = %q, want Enum", kind["Color"])
	}
	if kind["Drawable"] != "Interface" {
		t.Errorf("Drawable (trait in mod) kind = %q, want Interface", kind["Drawable"])
	}
}

// TestExtractRust_ImplInsideModBlock_MethodsParent verifies an indented
// `impl` block inside a `mod` is recognised as a scope, so its methods
// extract as Method parented to the type — not as orphan Functions.
func TestExtractRust_ImplInsideModBlock_MethodsParent(t *testing.T) {
	t.Parallel()
	src := []byte(`mod geo {
    pub struct Point {
        x: f64,
    }

    impl Point {
        pub fn magnitude(&self) -> f64 {
            self.x
        }
    }
}
`)
	r := Extract(src, "Rust", "geo.rs")
	if r == nil {
		t.Fatal("nil result")
	}
	var found bool
	for _, s := range r.Symbols {
		if s.Name != "magnitude" {
			continue
		}
		found = true
		if s.Kind != "Method" {
			t.Errorf("magnitude kind = %q, want Method (an indented impl block must be a scope)", s.Kind)
		}
		if s.Parent != "geo::Point" {
			t.Errorf("magnitude parent = %q, want geo::Point", s.Parent)
		}
	}
	if !found {
		t.Error("magnitude not extracted")
	}
}
