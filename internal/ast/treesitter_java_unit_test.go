// SPDX-License-Identifier: MIT

package ast

import (
	"context"
	"testing"
)

// Self-contained (no external corpus) coverage of the real-tree-sitter Java
// extractor + the tsbridge WASM path. Exercises symbol kinds, method/parent
// scoping, imports, and call extraction on an inline snippet so it runs in
// normal CI.
func TestTreeSitterJava_InlineExtract(t *testing.T) {
	const src = `package com.example;

import java.util.List;
import java.util.Map;

public class OrderService implements Repository {
    public OrderService() {
        init();
    }

    public List<String> names() {
        return load();
    }

    public int total(int x) {
        return compute(x);
    }
}

interface Repository {
    int size();
}

enum OrderStatus {
    NEW, SHIPPED;

    public boolean done() {
        return this == SHIPPED;
    }
}

record Point(int x, int y) {}
`
	x, err := newJavaTSExtractor(context.Background())
	if err != nil {
		t.Fatalf("newJavaTSExtractor: %v", err)
	}
	fr := x.extract([]byte(src), "src/OrderService.java")

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
		"Repository":   "Interface",
		"OrderStatus":  "Enum",
		"Point":        "Class", // record → Class (Java 14+)
	}
	for name, kind := range want {
		if !byKind[kind][name] {
			t.Errorf("missing %s %q; got %v", kind, name, byKind[kind])
		}
	}
	for _, m := range []string{"names", "total", "size", "done"} {
		if !byKind["Method"][m] {
			t.Errorf("missing Method %q; got %v", m, byKind["Method"])
		}
	}

	// Parent + QN follow pincher's moduleQN convention (relPath stem + "." +
	// type), matching the regex tier so symbol IDs stay stable.
	var totalParent, totalQN string
	for _, s := range fr.Symbols {
		if s.Name == "total" && s.Kind == "Method" {
			totalParent = s.Parent
			totalQN = s.QualifiedName
		}
	}
	if totalParent != "src.OrderService.OrderService" {
		t.Errorf("method total parent = %q, want src.OrderService.OrderService", totalParent)
	}
	if totalQN != "src.OrderService.OrderService.total" {
		t.Errorf("method total QN = %q, want src.OrderService.OrderService.total", totalQN)
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
	for _, want := range []string{"java.util.List", "java.util.Map"} {
		if !imports[want] {
			t.Errorf("import %q missing (want full dotted path); got %v", want, imports)
		}
	}
	for _, want := range []string{"init", "load", "compute"} {
		if !calls[want] {
			t.Errorf("CALLS edge to %q missing; got %v", want, calls)
		}
	}
}

func TestJoinDot(t *testing.T) {
	if got := joinDot("", "b"); got != "b" {
		t.Errorf("joinDot empty-a = %q", got)
	}
	if got := joinDot("a", ""); got != "a" {
		t.Errorf("joinDot empty-b = %q", got)
	}
	if got := joinDot("a", "b"); got != "a.b" {
		t.Errorf("joinDot = %q, want a.b", got)
	}
}
