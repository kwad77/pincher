// SPDX-License-Identifier: MIT

package ast

import (
	"context"
	"testing"
)

// Self-contained coverage of the real-tree-sitter C# extractor + the tsbridge
// WASM path. Exercises namespace-blind QN scoping (matching the regex tier),
// symbol kinds, bare-identifier return types, constructors, generic methods,
// usings, and calls on an inline snippet so it runs in normal CI.
func TestTreeSitterCSharp_InlineExtract(t *testing.T) {
	const src = `using System;
using System.Collections.Generic;

namespace Acme.Orders
{
    public interface IRepository
    {
        int Count();
    }

    public enum Status { New, Shipped }

    public class OrderService : IRepository
    {
        public OrderService()
        {
            Init();
        }

        // bare-identifier return type (Order) — name must be 'Find', not 'Order'
        public Order Find(int id)
        {
            return Load(id);
        }

        public List<T> Wrap<T>(T item)
        {
            return Make(item);
        }

        public int Count() { return 0; }
    }

    public record Point(int X, int Y);
}
`
	x, err := newCSharpTSExtractor(context.Background())
	if err != nil {
		t.Fatalf("newCSharpTSExtractor: %v", err)
	}
	fr := x.extract([]byte(src), "src/OrderService.cs")

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
		"OrderService": "Class",
		"IRepository":  "Interface",
		"Status":       "Enum",
		"Point":        "Class", // record → Class
	}
	for name, kind := range want {
		if !byKind[kind][name] {
			t.Errorf("missing %s %q; got %v", kind, name, byKind[kind])
		}
	}
	// Methods (incl. constructor + the bare-identifier-return-type method).
	for _, m := range []string{"OrderService", "Find", "Wrap", "Count"} {
		if !byKind["Method"][m] {
			t.Errorf("missing Method %q; got %v", m, byKind["Method"])
		}
	}
	// The bare return type "Order" must NOT be captured as a symbol.
	if byKind["Method"]["Order"] || byKind["Class"]["Order"] {
		t.Errorf("bare return type 'Order' leaked as a symbol")
	}

	// Namespace-blind QN: keyed on moduleQN(file), NOT the C# namespace —
	// matches the regex tier so symbol IDs stay stable.
	var findQN, findParent string
	for _, s := range fr.Symbols {
		if s.Name == "Find" && s.Kind == "Method" {
			findQN = s.QualifiedName
			findParent = s.Parent
		}
	}
	if findQN != "src.OrderService.OrderService.Find" {
		t.Errorf("method Find QN = %q, want src.OrderService.OrderService.Find (namespace-blind)", findQN)
	}
	if findParent != "src.OrderService.OrderService" {
		t.Errorf("method Find parent = %q, want src.OrderService.OrderService", findParent)
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
	for _, want := range []string{"System", "System.Collections.Generic"} {
		if !imports[want] {
			t.Errorf("using %q missing from IMPORTS; got %v", want, imports)
		}
	}
	for _, want := range []string{"Init", "Load", "Make"} {
		if !calls[want] {
			t.Errorf("CALLS edge to %q missing; got %v", want, calls)
		}
	}
}
