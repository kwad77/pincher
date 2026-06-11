// SPDX-License-Identifier: MIT

package ast

import (
	"context"
	"testing"
)

func TestTreeSitterRuby_InlineExtract(t *testing.T) {
	const src = `require "foo"
require_relative "../bar"

module Animals
  class Dog < Animal
    def initialize(name)
      @name = name
      setup()
    end

    def self.create
      Dog.new("rex")
    end

    def speak
      self.wag
      other.run(1)
    end
  end
end

def standalone
  true
end
`
	x, err := newRubyTSExtractor(context.Background())
	if err != nil {
		t.Fatalf("newRubyTSExtractor: %v", err)
	}
	fr, ok := x.extractChecked([]byte(src), "lib/animal.rb")
	if !ok {
		t.Fatal("Ruby parse hit an ERROR node on valid source")
	}

	byKind := map[string]map[string]bool{}
	qn := map[string]string{}
	parent := map[string]string{}
	for _, s := range fr.Symbols {
		if byKind[s.Kind] == nil {
			byKind[s.Kind] = map[string]bool{}
		}
		byKind[s.Kind][s.Name] = true
		qn[s.Name] = s.QualifiedName
		parent[s.Name] = s.Parent
		if s.ExtractionConfidence != 1.0 {
			t.Errorf("symbol %s: confidence %v, want 1.0", s.Name, s.ExtractionConfidence)
		}
	}
	// class + module both → Class (regex classRE parity).
	for _, name := range []string{"Animals", "Dog"} {
		if !byKind["Class"][name] {
			t.Errorf("missing Class %q; got %v", name, byKind["Class"])
		}
	}
	if !byKind["Function"]["standalone"] {
		t.Errorf("missing top-level Function standalone; got %v", byKind["Function"])
	}
	for _, m := range []string{"initialize", "create", "speak"} {
		if !byKind["Method"][m] {
			t.Errorf("missing Method %q; got %v", m, byKind["Method"])
		}
	}
	// Namespace-blind: class inside a module stays moduleQN-based.
	if qn["Dog"] != "lib::animal::Dog" {
		t.Errorf("Dog QN = %q, want lib::animal::Dog (namespace-blind)", qn["Dog"])
	}
	if qn["speak"] != "lib::animal::Dog::speak" {
		t.Errorf("speak QN = %q, want lib::animal::Dog::speak", qn["speak"])
	}
	if parent["speak"] != "lib::animal::Dog" {
		t.Errorf("speak parent = %q, want lib::animal::Dog", parent["speak"])
	}
	// superclass captured.
	if parent["Dog"] != "Animal" {
		t.Errorf("Dog parent = %q, want Animal", parent["Dog"])
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
	for _, want := range []string{"foo", "../bar"} {
		if !imports[want] {
			t.Errorf("require %q missing from IMPORTS; got %v", want, imports)
		}
	}
	for _, want := range []string{"setup", "wag", "new", "run"} {
		if !calls[want] {
			t.Errorf("CALLS edge to %q missing; got %v", want, calls)
		}
	}
	// require must NOT also appear as a CALLS edge (it's an IMPORTS).
	if calls["require"] || calls["require_relative"] {
		t.Errorf("require/require_relative leaked as a CALLS edge; got %v", calls)
	}
}
