// SPDX-License-Identifier: MIT

package ast

import (
	"context"
	"testing"
)

func TestTreeSitterKotlin_InlineExtract(t *testing.T) {
	const src = `package com.x

import a.b.Foo
import c.d.Bar

interface Greeter {
    fun greet(): String
}

data class Point(val x: Int) {
    fun dist(): Int = abs(x)
}

class Service : Greeter {
    override fun greet(): String { return load() }
    companion object Factory { fun make() = Service() }
}

enum class Status {
    ACTIVE;
    fun on(): Boolean = true
}

object Singleton {
    fun run() { setup(); obj.go() }
}

fun topLevel(): Int {
    val s = Service()
    return 1
}
`
	x, err := newKotlinTSExtractor(context.Background())
	if err != nil {
		t.Fatalf("newKotlinTSExtractor: %v", err)
	}
	fr, ok := x.extractChecked([]byte(src), "src/Service.kt")
	if !ok {
		t.Fatal("Kotlin parse hit an ERROR node on valid source")
	}

	byKind := map[string]map[string]bool{}
	qn := map[string]string{}
	for _, s := range fr.Symbols {
		if byKind[s.Kind] == nil {
			byKind[s.Kind] = map[string]bool{}
		}
		byKind[s.Kind][s.Name] = true
		qn[s.Name] = s.QualifiedName
		if s.ExtractionConfidence != 1.0 {
			t.Errorf("symbol %s: confidence %v, want 1.0", s.Name, s.ExtractionConfidence)
		}
	}
	for name, kind := range map[string]string{
		"Greeter": "Interface", "Point": "Class", "Service": "Class",
		"Status": "Enum", "Singleton": "Class", "Factory": "Class", "topLevel": "Function",
	} {
		if !byKind[kind][name] {
			t.Errorf("missing %s %q; got %v", kind, name, byKind[kind])
		}
	}
	for _, m := range []string{"greet", "dist", "make", "on", "run"} {
		if !byKind["Method"][m] {
			t.Errorf("missing Method %q; got %v", m, byKind["Method"])
		}
	}
	if qn["greet"] != "src.Service.Service.greet" {
		t.Errorf("greet QN = %q, want src.Service.Service.greet", qn["greet"])
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
	for _, want := range []string{"a.b.Foo", "c.d.Bar"} {
		if !imports[want] {
			t.Errorf("import %q missing; got %v", want, imports)
		}
	}
	for _, want := range []string{"abs", "load", "setup", "go", "Service"} {
		if !calls[want] {
			t.Errorf("CALLS edge to %q missing; got %v", want, calls)
		}
	}
}
