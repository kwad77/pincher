// SPDX-License-Identifier: MIT

package ast

import (
	"strings"
	"testing"
)

// #1762: a TS `namespace Foo { … }` is a namespace-style scope. Adding a
// plain scopeRE for it regressed — the framework switched the scoped
// body to methodRE, and a namespace body holds `function` declarations,
// not method-syntax members, so every `function` inside vanished. The
// new nsScopeRE parents namespace members AND emits a Module symbol for
// the namespace while keeping funcRE for the body.

func TestExtractTS_NamespaceScope_ParentsFunctionMembers_1762(t *testing.T) {
	t.Parallel()
	src := []byte(`export namespace Geometry {
    export function area(): number {
        return 1;
    }

    export function perimeter(): number {
        return 2;
    }
}

export function topLevel(): number {
    return 3;
}
`)
	r := Extract(src, "TypeScript", "geom.ts")
	if r == nil {
		t.Fatal("nil result")
	}

	byName := map[string]ExtractedSymbol{}
	for _, s := range r.Symbols {
		byName[s.Name] = s
	}

	// The namespace emits its own Module symbol.
	if g, ok := byName["Geometry"]; !ok {
		t.Error("namespace Geometry not extracted")
	} else if g.Kind != "Module" {
		t.Errorf("Geometry kind = %q, want Module", g.Kind)
	}

	// Its function members must survive (the #1761 regression dropped
	// them entirely), stay Kind=Function (a namespace function is not a
	// method), and parent to the namespace.
	for _, fn := range []string{"area", "perimeter"} {
		s, ok := byName[fn]
		if !ok {
			t.Errorf("#1762: namespace member %q was dropped", fn)
			continue
		}
		if s.Kind != "Function" {
			t.Errorf("%q kind = %q, want Function (a namespace member function is not a method)", fn, s.Kind)
		}
		if !strings.HasSuffix(s.Parent, "Geometry") {
			t.Errorf("%q parent = %q, want it to end with the namespace name Geometry", fn, s.Parent)
		}
	}

	// Control: a genuinely top-level function is unchanged — Kind
	// Function, no parent.
	if tl, ok := byName["topLevel"]; !ok {
		t.Error("topLevel not extracted")
	} else {
		if tl.Kind != "Function" {
			t.Errorf("topLevel kind = %q, want Function", tl.Kind)
		}
		if tl.Parent != "" {
			t.Errorf("topLevel parent = %q, want empty (top-level, not in a namespace)", tl.Parent)
		}
	}
}
