// SPDX-License-Identifier: MIT

package ast

import (
	"strings"
	"testing"
)

// #1859: design-rationale comments (NOTE/HACK/WHY/FIXME/XXX/TODO/BUG)
// are extracted as queryable Rationale symbols, parented to the
// enclosing func.
const rationaleGoSrc = `package widget

// NOTE: this counter is intentionally not atomic — single-goroutine by design.
var count int

// process doubles x.
func process(x int) int {
	// HACK: clamp negatives until the upstream fix in #999 lands.
	if x < 0 {
		x = 0
	}
	return x * 2
}

type Widget struct{ id int }

func (w *Widget) Render() string {
	// TODO: cache the rendered string — recomputed on every call.
	return "widget"
}

// helper is just a plain doc comment, not a rationale annotation.
func helper() {}
`

func rationaleSymbols(result *FileResult) []ExtractedSymbol {
	var out []ExtractedSymbol
	for _, s := range result.Symbols {
		if s.Kind == "Rationale" {
			out = append(out, s)
		}
	}
	return out
}

func TestExtractGo_Rationale(t *testing.T) {
	result := Extract([]byte(rationaleGoSrc), "Go", "widget/w.go")
	if result == nil {
		t.Fatal("nil result")
	}
	rats := rationaleSymbols(result)
	if len(rats) != 3 {
		t.Fatalf("want 3 Rationale symbols (NOTE, HACK, TODO); got %d: %+v", len(rats), rats)
	}

	byTag := map[string]ExtractedSymbol{}
	for _, r := range rats {
		tag := strings.SplitN(r.Name, ":", 2)[0]
		byTag[tag] = r
		if r.ExtractionConfidence != 1.0 {
			t.Errorf("%s: confidence = %v, want 1.0", r.Name, r.ExtractionConfidence)
		}
		if r.EndByte <= r.StartByte {
			t.Errorf("%s: bad byte span [%d,%d)", r.Name, r.StartByte, r.EndByte)
		}
	}

	// NOTE sits at file scope — no enclosing func.
	if n, ok := byTag["NOTE"]; !ok {
		t.Error("NOTE rationale not extracted")
	} else if n.Parent != "" {
		t.Errorf("file-level NOTE should have no parent; got %q", n.Parent)
	}

	// HACK is inside process().
	if h, ok := byTag["HACK"]; !ok {
		t.Error("HACK rationale not extracted")
	} else if h.Parent != "widget.process" {
		t.Errorf("HACK parent = %q, want widget.process", h.Parent)
	}

	// TODO is inside the *Widget.Render method.
	if td, ok := byTag["TODO"]; !ok {
		t.Error("TODO rationale not extracted")
	} else if td.Parent != "widget.Widget.Render" {
		t.Errorf("TODO parent = %q, want widget.Widget.Render", td.Parent)
	}
}

// A plain doc comment (no tag) must NOT become a Rationale symbol.
func TestExtractGo_Rationale_IgnoresPlainComments(t *testing.T) {
	result := Extract([]byte(rationaleGoSrc), "Go", "widget/w.go")
	for _, r := range rationaleSymbols(result) {
		if strings.Contains(r.Name, "plain doc comment") || strings.Contains(r.Name, "process doubles") {
			t.Errorf("plain doc comment leaked into a Rationale symbol: %q", r.Name)
		}
	}
}

// Multi-line // rationale: one Rationale symbol spanning the whole group.
func TestExtractGo_Rationale_MultiLineGroup(t *testing.T) {
	src := `package p

func f() {
	// WHY: the retry budget is 3 because the upstream gateway
	// drops the connection after ~2s and a 4th attempt would
	// exceed the caller's 10s deadline.
	_ = 1
}
`
	rats := rationaleSymbols(Extract([]byte(src), "Go", "p/p.go"))
	if len(rats) != 1 {
		t.Fatalf("multi-line group should yield exactly 1 Rationale symbol; got %d", len(rats))
	}
	if !strings.HasPrefix(rats[0].Name, "WHY:") {
		t.Errorf("name = %q, want a WHY: prefix", rats[0].Name)
	}
	if rats[0].EndLine <= rats[0].StartLine {
		t.Errorf("multi-line rationale should span >1 line; got lines %d-%d", rats[0].StartLine, rats[0].EndLine)
	}
}
