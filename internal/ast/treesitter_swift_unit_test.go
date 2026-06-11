// SPDX-License-Identifier: MIT

package ast

import (
	"context"
	"testing"
)

func TestTreeSitterSwift_InlineExtract(t *testing.T) {
	const src = `import Foo

protocol Greeter {
    func greet() -> String
}

struct Point {
    let x: Int
    func dist() -> Int { return abs(x) }
}

class Service: Greeter {
    init() { setup() }
    func greet() -> String { return load() }
}

enum Status {
    case active
    func on() -> Bool { return true }
}

extension Point {
    func zero() -> Point { return Point() }
}

func topLevel() -> Int {
    let s = Service()
    return 1
}
`
	x, err := newSwiftTSExtractor(context.Background())
	if err != nil {
		t.Fatalf("newSwiftTSExtractor: %v", err)
	}
	fr, ok := x.extractChecked([]byte(src), "Sources/Service.swift")
	if !ok {
		t.Fatal("Swift parse hit an ERROR node on valid source")
	}

	byKind := map[string]map[string]bool{}
	parent := map[string]string{}
	for _, s := range fr.Symbols {
		if byKind[s.Kind] == nil {
			byKind[s.Kind] = map[string]bool{}
		}
		byKind[s.Kind][s.Name] = true
		parent[s.Name] = s.Parent
		if s.ExtractionConfidence != 1.0 {
			t.Errorf("symbol %s: confidence %v, want 1.0", s.Name, s.ExtractionConfidence)
		}
	}
	for name, kind := range map[string]string{
		"Greeter": "Interface", "Point": "Class", "Service": "Class", "Status": "Enum", "topLevel": "Function",
	} {
		if !byKind[kind][name] {
			t.Errorf("missing %s %q; got %v", kind, name, byKind[kind])
		}
	}
	for _, m := range []string{"greet", "dist", "on", "init", "zero"} {
		if !byKind["Method"][m] {
			t.Errorf("missing Method %q; got %v", m, byKind["Method"])
		}
	}
	// extension is scope-only: no second Point symbol, but `zero` parents to it.
	if parent["zero"] != "Sources.Service.Point" {
		t.Errorf("zero parent = %q, want Sources.Service.Point (extension scope)", parent["zero"])
	}
	// protocol method greet parents to the protocol.
	if !byKind["Method"]["greet"] {
		t.Error("protocol method greet missing")
	}

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
	if !imports["Foo"] {
		t.Errorf("import Foo missing; got %v", imports)
	}
	for _, want := range []string{"setup", "load", "abs", "Service"} {
		if !calls[want] {
			t.Errorf("CALLS edge to %q missing; got %v", want, calls)
		}
	}
}
