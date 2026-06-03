// SPDX-License-Identifier: MIT

package ast

import (
	"strings"
	"testing"
)

// #1159 v0.62: per-file CALLS pass for Ruby. Required generalizing
// regexCallScan's signature-skip from "find `{`" to "find `{` or
// first newline" so end-keyword block bodies work the same as
// C-family braces.
//
// Tests: positive (CALLS emitted from a Ruby def body), control
// (empty body emits zero CALLS), cross-check on the signature-skip
// fallback handling Ruby's def-without-{ shape.

const rubyWithCallsSrc = `class Bootstrap
  def run
    load_config
    c = parse_config
    render(c)
  end
end
`

func TestExtractRuby_PerFileCalls_EmitsEdges(t *testing.T) {
	r := Extract([]byte(rubyWithCallsSrc), "Ruby", "src/boot.rb")
	if r == nil {
		t.Fatal("nil result")
	}
	// Only `render(c)` is captured by regexCallRE — it requires `(`
	// after the name. Ruby allows method calls without parens
	// (`load_config`, `parse_config`) which regexCallRE doesn't see;
	// that's a known limitation of the regex-tier CALLS pass and
	// matches the documented behaviour (parens-required). The
	// positive case here therefore asserts at least the
	// parens-bearing call resolves.
	hasRender := false
	for _, e := range r.Edges {
		if e.Kind != "CALLS" {
			continue
		}
		if e.ToName == "render" && strings.HasSuffix(e.FromQN, "run") {
			hasRender = true
		}
	}
	if !hasRender {
		t.Errorf("Ruby: expected CALLS edge run → render(...); missing")
	}
}

// Control: a def body with no parens-bearing call emits zero CALLS.
// Ruby idiom often elides parens; the regex-tier pass can't see
// those, and that's documented. This test pins the "no call sites
// → zero edges" contract; if a future change accidentally surfaced
// every identifier as a CALLS target it would fail loudly.
const rubyNoCallsSrc = `class Const
  def value
    42
  end
end
`

func TestExtractRuby_PerFileCalls_NoParenCallsEmitsZero(t *testing.T) {
	r := Extract([]byte(rubyNoCallsSrc), "Ruby", "src/const.rb")
	if r == nil {
		t.Fatal("nil result")
	}
	for _, e := range r.Edges {
		if e.Kind == "CALLS" && strings.HasSuffix(e.FromQN, "value") {
			t.Errorf("Ruby: paren-less body emitted unexpected CALLS edge to %q", e.ToName)
		}
	}
}

// Cross-check: the regexCallScan signature-skip fallback handles
// "no `{` in body" by skipping past the first newline. Exercise the
// fallback path directly with a synthetic def-style body — first
// line is "def name", body starts on the next line. The function
// name itself ("name") must NOT appear as a CALLS target — the
// signature skip prevented that pre-fix when `{` existed; the
// newline fallback must do the same.
func TestRegexCallScan_NewlineFallback_SkipsSignatureLine(t *testing.T) {
	body := []byte("def renderAll(items)\n  render(items)\n  other(items)\nend\n")
	edges := regexCallScan(body, "m.Foo.renderAll")
	// "renderAll" must NOT be among the targets — the signature was
	// skipped past the first newline.
	for _, e := range edges {
		if e.ToName == "renderAll" {
			t.Errorf("regexCallScan with newline-fallback emitted the function's own name %q as a CALLS target", e.ToName)
		}
	}
	// "render" and "other" should be present (the body's actual
	// call sites).
	want := map[string]bool{"render": false, "other": false}
	for _, e := range edges {
		if _, expected := want[e.ToName]; expected {
			want[e.ToName] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("regexCallScan with newline-fallback missing expected target %q", name)
		}
	}
}
