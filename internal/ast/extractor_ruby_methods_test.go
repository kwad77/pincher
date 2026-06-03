// SPDX-License-Identifier: MIT

package ast

import "testing"

// Ruby extractor defects found dogfooding the languages the #1389
// cross-language sweep did not cover:
//
//   1. funcRE captured `self` for `def self.create` — a Ruby class
//      method. Every `def self.X` in a file collapsed to the name
//      "self" (and collided).
//   2. classRE used a bare `^class` anchor, so a class indented
//      inside a module — idiomatic Ruby — was never extracted.
//   3. `module` (a constantly-used Ruby type container) was not
//      matched at all.

func TestExtractRuby_ClassMethodNamedCorrectly(t *testing.T) {
	t.Parallel()
	src := []byte(`class Order
  def total
    0
  end

  def self.create(id)
    new(id)
  end

  def self.find(id)
    nil
  end
end
`)
	r := Extract(src, "Ruby", "order.rb")
	if r == nil {
		t.Fatal("nil result")
	}
	got := map[string]bool{}
	for _, s := range r.Symbols {
		got[s.Name] = true
		if s.Name == "self" {
			t.Errorf("a `def self.X` method was extracted with name \"self\" — "+
				"the receiver prefix must be skipped so the method name is captured")
		}
	}
	for _, want := range []string{"total", "create", "find"} {
		if !got[want] {
			t.Errorf("method %q not extracted; got %v", want, mapKeysRuby(got))
		}
	}
}

// Indented class inside a module, plus the module itself, must both
// be extracted as types.
func TestExtractRuby_IndentedClassAndModule(t *testing.T) {
	t.Parallel()
	src := []byte(`module Billing
  class Invoice
    def pay
    end
  end

  module Helpers
  end
end
`)
	r := Extract(src, "Ruby", "billing.rb")
	kind := map[string]string{}
	for _, s := range r.Symbols {
		kind[s.Name] = s.Kind
	}
	for _, want := range []string{"Billing", "Invoice", "Helpers"} {
		if kind[want] != "Class" {
			t.Errorf("%q kind = %q; want Class (module / indented class must extract)", want, kind[want])
		}
	}
}

// Control: an ordinary instance method is unaffected by the
// receiver-prefix skip — `def total` still yields name `total`.
func TestExtractRuby_PlainMethodUnaffected(t *testing.T) {
	t.Parallel()
	src := []byte(`class Widget
  def render
    "x"
  end
end
`)
	r := Extract(src, "Ruby", "widget.rb")
	var sawRender bool
	for _, s := range r.Symbols {
		if s.Name == "render" {
			sawRender = true
		}
	}
	if !sawRender {
		t.Error("plain method `render` not extracted")
	}
}

func mapKeysRuby(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
