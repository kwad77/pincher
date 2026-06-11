// SPDX-License-Identifier: MIT

package ast

import (
	"context"
	"testing"
)

// #1958 follow-up: the tree-sitter extractors must emit a CALLS edge for
// `new Foo()` constructor invocations (object_creation_expression), matching
// the regex tier's `name(` scan so "who instantiates Foo" stays in the graph.

func TestTreeSitterJava_ConstructorCallEdge(t *testing.T) {
	const src = `package com.example;
public class Boot {
	public void run() {
		Service s = new Service();
		Helper h = new Helper<String>();
	}
}
`
	x, err := newJavaTSExtractor(context.Background())
	if err != nil {
		t.Fatalf("newJavaTSExtractor: %v", err)
	}
	fr := x.extract([]byte(src), "src/Boot.java")
	got := map[string]bool{}
	for _, e := range fr.Edges {
		if e.Kind == "CALLS" {
			got[e.ToName] = true
		}
	}
	for _, want := range []string{"Service", "Helper"} {
		if !got[want] {
			t.Errorf("Java: missing constructor CALLS edge to %q; got %v", want, got)
		}
	}
}

func TestTreeSitterCSharp_ConstructorCallEdge(t *testing.T) {
	const src = `namespace Acme
{
	public class Boot
	{
		public void Run()
		{
			var s = new Service();
			var h = new Helper<string>();
		}
	}
}
`
	x, err := newCSharpTSExtractor(context.Background())
	if err != nil {
		t.Fatalf("newCSharpTSExtractor: %v", err)
	}
	fr := x.extract([]byte(src), "src/Boot.cs")
	got := map[string]bool{}
	for _, e := range fr.Edges {
		if e.Kind == "CALLS" {
			got[e.ToName] = true
		}
	}
	for _, want := range []string{"Service", "Helper"} {
		if !got[want] {
			t.Errorf("C#: missing constructor CALLS edge to %q; got %v", want, got)
		}
	}
}
