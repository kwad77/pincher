// SPDX-License-Identifier: MIT

package ast

import "testing"

// #1761: tsRE.methodRE's modifier set omitted `abstract`, so an
// `abstract foo(): T;` declaration had `abstract` captured as the name
// and then failed the `(` anchor — every abstract method was dropped.
// And classRE / interfaceRE / enumRE anchored at column 0 with a bare
// `^`, so a class / interface / enum indented inside a `namespace`
// block was never extracted.

// TestExtractTS_AbstractMethodExtracted pins abstract-method recovery.
func TestExtractTS_AbstractMethodExtracted(t *testing.T) {
	t.Parallel()
	src := []byte(`export abstract class Base {
  abstract run(): void;
  protected abstract load(id: string): Promise<void>;
  start(): void {}
}
`)
	r := Extract(src, "TypeScript", "base.ts")
	if r == nil {
		t.Fatal("nil result")
	}
	kind := map[string]string{}
	parent := map[string]string{}
	for _, s := range r.Symbols {
		kind[s.Name] = s.Kind
		parent[s.Name] = s.Parent
	}
	for _, m := range []string{"run", "load", "start"} {
		if kind[m] != "Method" {
			t.Errorf("%q kind = %q, want Method", m, kind[m])
		}
		if parent[m] != "base.Base" {
			t.Errorf("%q parent = %q, want base.Base", m, parent[m])
		}
	}
	// A symbol literally named `abstract` would mean the modifier was
	// captured as the name — the pre-fix failure shape.
	if _, leaked := kind["abstract"]; leaked {
		t.Error("a symbol named \"abstract\" was extracted — the modifier leaked into the name capture")
	}
}

// TestExtractTS_IndentedTypesExtract pins that a class / interface /
// enum indented inside a namespace block is extracted (the namespace
// itself is not a scope — see #1761 follow-up — but the indented types
// must still surface rather than vanish).
func TestExtractTS_IndentedTypesExtract(t *testing.T) {
	t.Parallel()
	src := []byte(`namespace Util {
  export class Inner {
    go(): void {}
  }

  export interface Spec {
    check(): boolean;
  }

  export enum Mode { On, Off }
}
`)
	r := Extract(src, "TypeScript", "util.ts")
	if r == nil {
		t.Fatal("nil result")
	}
	kind := map[string]string{}
	for _, s := range r.Symbols {
		kind[s.Name] = s.Kind
	}
	if kind["Inner"] != "Class" {
		t.Errorf("Inner (indented class) kind = %q, want Class", kind["Inner"])
	}
	if kind["Spec"] != "Interface" {
		t.Errorf("Spec (indented interface) kind = %q, want Interface", kind["Spec"])
	}
	if kind["Mode"] != "Enum" {
		t.Errorf("Mode (indented enum) kind = %q, want Enum", kind["Mode"])
	}
	// The indented class is a real scope — its method still parents.
	for _, s := range r.Symbols {
		if s.Name == "go" && s.Kind == "Method" && s.Parent != "util.Inner" {
			t.Errorf("go parent = %q, want util.Inner", s.Parent)
		}
	}
}
