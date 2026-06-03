// SPDX-License-Identifier: MIT

package ast

import (
	"testing"
)

// #1693 (#1389 cross-language sweep): C# funcRE's `(returntype)+ name (`
// form treats statement keywords as return-type tokens, so an
// object-creation expression — `new Vector2(...)`,
// `return new Foo(...)`, the object-initializer
// `new PrototypePlacedObjectData() { ... }` (195× in one Yevolve
// file) — emits a phantom Method named for the *type*. Those phantoms
// collide on qualified_name. dropCSharpConstructorCalls drops them.
//
// Four-case shape (#1152): positive (object-creation lines drop),
// negative (real methods/constructors kept), control (the rare
// `new`-modifier method-hiding shape kept), cross-check
// (csharpSymbolIsObjectCreation discriminator on each shape).

func TestExtractCSharp_ConstructorCallsDropped_1693(t *testing.T) {
	t.Parallel()
	src := []byte(`class Builder
{
    void Build()
    {
        var list = new List<int>();
        return new Vector2(1, 2);
    }
    object[] items = {
        new PrototypePlacedObjectData() {
            x = 1,
        },
        new PrototypePlacedObjectData() {
            x = 2,
        },
    };
}
`)
	result := Extract(src, "C#", "src/Builder.cs")
	if result == nil {
		t.Fatal("Extract returned nil")
	}
	for _, s := range result.Symbols {
		if s.Kind != "Function" && s.Kind != "Method" {
			continue
		}
		switch s.Name {
		case "Vector2", "PrototypePlacedObjectData", "List":
			t.Errorf("phantom Method %q from an object-creation expression — should be dropped (#1693)", s.Name)
		}
	}
}

// Negative: real methods and constructors MUST survive — including a
// constructor that builds its own type in an expression body
// (`public Vec() => new Vec(...)`), the worst false-drop trap.
func TestExtractCSharp_RealMethodsKept_1693(t *testing.T) {
	t.Parallel()
	src := []byte(`class Vec
{
    public Vec() => new Vec(0, 0);
    public int Length() { return 0; }
    public static Vec Make() => new Vec(1, 1);
}
`)
	result := Extract(src, "C#", "src/Vec.cs")
	got := map[string]bool{}
	for _, s := range result.Symbols {
		if s.Kind == "Function" || s.Kind == "Method" {
			got[s.Name] = true
		}
	}
	for _, want := range []string{"Vec", "Length", "Make"} {
		if !got[want] {
			t.Errorf("real method/constructor %q dropped — dropCSharpConstructorCalls is too broad (symbol loss); got %v", want, got)
		}
	}
}

// Control: the rare `new`-modifier method-hiding shape —
// `new void Foo()` — has `new` before a RETURN TYPE, then the name.
// It must NOT be mistaken for object creation.
func TestExtractCSharp_NewModifierMethodKept_1693(t *testing.T) {
	t.Parallel()
	src := []byte(`class Derived : Base
{
    new void Render() { }
    public new string Describe() { return ""; }
}
`)
	result := Extract(src, "C#", "src/Derived.cs")
	got := map[string]bool{}
	for _, s := range result.Symbols {
		if s.Kind == "Function" || s.Kind == "Method" {
			got[s.Name] = true
		}
	}
	for _, want := range []string{"Render", "Describe"} {
		if !got[want] {
			t.Errorf("method-hiding `new` method %q dropped — `new <ReturnType> <Name>(` is a real method; got %v", want, got)
		}
	}
}

// Cross-check: the csharpSymbolIsObjectCreation discriminator.
func TestCSharpSymbolIsObjectCreation_Discriminator_1693(t *testing.T) {
	t.Parallel()
	cases := []struct {
		line   string
		name   string
		want   bool
		why    string
	}{
		{"        new Vector2(1, 2)", "Vector2", true, "bare new + paren"},
		{"    new Foo() {", "Foo", true, "object initializer brace"},
		{"        return new Bar();", "Bar", true, "return new"},
		{"    throw new InvalidOp();", "InvalidOp", true, "throw new"},
		{"        yield return new Item();", "Item", true, "yield return new"},
		{"            else GetTree().Foo();", "GetTree", true, "else + brace-less call"},
		{"        do Tick();", "Tick", true, "do + brace-less call"},
		{"    new List<int>()", "List", true, "generic new"},
		{"    new void Foo() { }", "Foo", false, "new modifier + return type"},
		{"    public void Render() { }", "Render", false, "ordinary method"},
		{"    public Vec() => new Vec();", "Vec", false, "constructor (line opens with access modifier)"},
		{"    returnCode();", "returnCode", false, "name starting with `return` is not the keyword"},
	}
	for _, c := range cases {
		s := ExtractedSymbol{Name: c.name, StartByte: 0}
		got := csharpSymbolIsObjectCreation(s, []byte(c.line))
		if got != c.want {
			t.Errorf("csharpSymbolIsObjectCreation(%q, name=%q) = %v, want %v (%s)", c.line, c.name, got, c.want, c.why)
		}
	}
}
