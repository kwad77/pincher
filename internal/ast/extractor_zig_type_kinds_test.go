// SPDX-License-Identifier: MIT

package ast

import "testing"

// #1755: zigRE.classRE matched only `const Name = struct`, so a Zig
// type defined as enum / union / union(enum) / opaque was never
// extracted — the type vanished from the symbol graph.

// TestExtractZig_TypeKinds_AllExtract covers every Zig named-type
// shape: struct (classRE), union + union(enum) + opaque (classRE),
// enum (enumRE), plus packed/extern layout qualifiers.
func TestExtractZig_TypeKinds_AllExtract(t *testing.T) {
	t.Parallel()
	src := []byte(`const Point = struct {
    x: i32,
};

const Color = enum {
    red,
    green,
};

const Value = union(enum) {
    int: i64,
    float: f64,
};

const Raw = union {
    a: u8,
};

const Handle = opaque {};

const Header = packed struct {
    flag: u1,
};

const CABI = extern struct {
    n: c_int,
};
`)
	r := Extract(src, "Zig", "types.zig")
	if r == nil {
		t.Fatal("nil result")
	}
	kind := map[string]string{}
	for _, s := range r.Symbols {
		kind[s.Name] = s.Kind
	}
	for _, name := range []string{"Point", "Color", "Value", "Raw", "Handle", "Header", "CABI"} {
		if kind[name] == "" {
			t.Errorf("%q not extracted (kind empty)", name)
		}
	}
	// enum extracts as Enum; the rest as Class.
	if kind["Color"] != "Enum" {
		t.Errorf("Color kind = %q, want Enum", kind["Color"])
	}
	for _, name := range []string{"Point", "Value", "Raw", "Handle", "Header", "CABI"} {
		if kind[name] != "Class" {
			t.Errorf("%q kind = %q, want Class", name, kind[name])
		}
	}
}

// TestExtractZig_TaggedUnionNotDoubleExtracted is the control:
// `union(enum)` carries the substring `enum` but must extract once,
// as Class — enumRE requires `enum` immediately after `=`, and a
// tagged union has `union` there.
func TestExtractZig_TaggedUnionNotDoubleExtracted(t *testing.T) {
	t.Parallel()
	src := []byte(`const Tagged = union(enum) {
    a: i32,
    b: bool,
};
`)
	r := Extract(src, "Zig", "tagged.zig")
	count := 0
	var gotKind string
	for _, s := range r.Symbols {
		if s.Name == "Tagged" {
			count++
			gotKind = s.Kind
		}
	}
	if count != 1 {
		t.Errorf("Tagged emitted %d times, want exactly 1 (no class/enum double-emission)", count)
	}
	if gotKind != "Class" {
		t.Errorf("Tagged kind = %q, want Class", gotKind)
	}
}
