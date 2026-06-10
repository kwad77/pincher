// SPDX-License-Identifier: MIT

package ast

import (
	"context"
	"testing"
)

// Self-contained (no external corpus) coverage of the real-tree-sitter Rust
// extractor + the tsbridge WASM path. Exercises symbol kinds, impl/trait
// method scoping, grouped-use enumeration, and call extraction on an inline
// snippet so it runs in normal CI.
func TestTreeSitterRust_InlineExtract(t *testing.T) {
	const src = `use std::collections::{HashMap, HashSet};
use std::io::Write as W;
use std::fmt;

pub struct Calc { total: i64 }

pub enum Op { Add, Sub }

pub trait Compute { fn compute(&self) -> i64; }

impl Calc {
    pub fn new() -> Self { Calc { total: 0 } }
    pub fn add(&mut self, x: i64) -> i64 {
        self.total += x;
        helper(x);            // identifier call
        let _ = self.compute(); // field-expression (method) call
        let _ = Calc::new();    // scoped-identifier call
        self.total
    }
}

impl Compute for Calc {
    fn compute(&self) -> i64 { self.total }
}

fn helper(n: i64) -> i64 { n * 2 }
`
	x, err := newRustTSExtractor(context.Background())
	if err != nil {
		t.Fatalf("newRustTSExtractor: %v", err)
	}
	fr := x.extract([]byte(src), "src/calc.rs")

	byKind := map[string]map[string]bool{}
	for _, s := range fr.Symbols {
		if byKind[s.Kind] == nil {
			byKind[s.Kind] = map[string]bool{}
		}
		byKind[s.Kind][s.Name] = true
		if s.ExtractionConfidence != 1.0 {
			t.Errorf("symbol %s: confidence %v, want 1.0", s.Name, s.ExtractionConfidence)
		}
	}

	want := map[string]string{
		"Calc":    "Class",
		"Op":      "Enum",
		"Compute": "Interface",
		"helper":  "Function",
	}
	for name, kind := range want {
		if !byKind[kind][name] {
			t.Errorf("missing %s %q; got %v", kind, name, byKind[kind])
		}
	}
	// Methods scoped to their impl/trait type.
	for _, m := range []string{"new", "add", "compute"} {
		if !byKind["Method"][m] {
			t.Errorf("missing Method %q; got %v", m, byKind["Method"])
		}
	}
	// Parent scoping: the `add` method must carry a parent type.
	var addParent string
	for _, s := range fr.Symbols {
		if s.Name == "add" && s.Kind == "Method" {
			addParent = s.Parent
		}
	}
	if addParent != "Calc" {
		t.Errorf("method add parent = %q, want Calc", addParent)
	}

	// Grouped use-tree enumerated (regex truncates at '{').
	imports := map[string]bool{}
	calls := map[string]bool{}
	for _, e := range fr.Edges {
		switch e.Kind {
		case "IMPORTS":
			imports[e.ToName] = true
		case "CALLS":
			calls[e.ToName] = true
		}
	}
	for _, want := range []string{"HashMap", "HashSet"} {
		if !imports[want] {
			t.Errorf("grouped import %q not enumerated; got %v", want, imports)
		}
	}
	if !calls["helper"] {
		t.Errorf("CALLS edge to helper missing; got %v", calls)
	}
}

func TestLastSeg(t *testing.T) {
	cases := map[string]string{
		"a::b::C": "C",
		"C":       "C",
		"":        "",
		"x::y":    "y",
	}
	for in, want := range cases {
		if got := lastSeg(in); got != want {
			t.Errorf("lastSeg(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestJoinQN(t *testing.T) {
	if got := joinQN("", "b"); got != "b" {
		t.Errorf("joinQN empty-a = %q", got)
	}
	if got := joinQN("a", ""); got != "a" {
		t.Errorf("joinQN empty-b = %q", got)
	}
	if got := joinQN("a", "b"); got != "a::b" {
		t.Errorf("joinQN = %q, want a::b", got)
	}
}
