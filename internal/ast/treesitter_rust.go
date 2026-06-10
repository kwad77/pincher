// SPDX-License-Identifier: MIT

// Experimental real-tree-sitter Rust extractor (ADR-0008 / #1957). NOT wired
// into the registry/dispatcher yet — this is the Phase-1 extractor under
// construction, exercised by the gated differential test against the regex
// tier. Maps tree-sitter nodes onto pincher's ExtractedSymbol/Edge shape.

package ast

import (
	"context"
	"path/filepath"

	"github.com/kwad77/pincher/internal/tsbridge"
)

type rustTSExtractor struct {
	ts   tsbridge.TreeSitter
	lang tsbridge.Language
	p    tsbridge.Parser
	ctx  context.Context
}

func newRustTSExtractor(ctx context.Context) (*rustTSExtractor, error) {
	ts, err := tsbridge.New(ctx)
	if err != nil {
		return nil, err
	}
	lang, err := ts.LanguageRust(ctx)
	if err != nil {
		return nil, err
	}
	p, err := ts.NewParser(ctx)
	if err != nil {
		return nil, err
	}
	if err := p.SetLanguage(ctx, lang); err != nil {
		return nil, err
	}
	return &rustTSExtractor{ts: ts, lang: lang, p: p, ctx: ctx}, nil
}

func (r *rustTSExtractor) kind(n tsbridge.Node) string { k, _ := n.Kind(r.ctx); return k }
func (r *rustTSExtractor) ncount(n tsbridge.Node) int {
	c, _ := n.NamedChildCount(r.ctx)
	return int(c)
}
func (r *rustTSExtractor) nchild(n tsbridge.Node, i int) tsbridge.Node {
	c, _ := n.NamedChild(r.ctx, uint64(i))
	return c
}
func (r *rustTSExtractor) text(n tsbridge.Node, src []byte) string {
	s, _ := n.StartByte(r.ctx)
	e, _ := n.EndByte(r.ctx)
	if int(s) <= int(e) && int(e) <= len(src) {
		return string(src[s:e])
	}
	return ""
}

// nameOfType returns the text of the first named child whose node kind is one
// of the given types — how we read a declaration's identifier without a
// field-name accessor.
func (r *rustTSExtractor) nameOfType(n tsbridge.Node, src []byte, types ...string) string {
	for i := 0; i < r.ncount(n); i++ {
		c := r.nchild(n, i)
		k := r.kind(c)
		for _, t := range types {
			if k == t {
				return r.text(c, src)
			}
		}
	}
	return ""
}

func joinQN(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return a + "::" + b
}

func (r *rustTSExtractor) extract(source []byte, relPath string) *FileResult {
	mod := filepath.ToSlash(filepath.Dir(relPath))
	if mod == "." {
		mod = ""
	}
	tree, err := r.p.ParseString(r.ctx, string(source))
	if err != nil {
		return &FileResult{Module: mod}
	}
	root, err := tree.RootNode(r.ctx)
	if err != nil {
		return &FileResult{Module: mod}
	}
	fr := &FileResult{Module: mod}
	r.walk(root, source, mod, "", fr)
	return fr
}

func (r *rustTSExtractor) walk(n tsbridge.Node, src []byte, mod, parentType string, fr *FileResult) {
	switch r.kind(n) {
	case "function_item":
		name := r.nameOfType(n, src, "identifier")
		if name != "" {
			if parentType != "" {
				fr.Symbols = append(fr.Symbols, ExtractedSymbol{
					Name: name, Kind: "Method", Parent: parentType,
					QualifiedName: joinQN(joinQN(mod, parentType), name), ExtractionConfidence: 1.0,
				})
			} else {
				fr.Symbols = append(fr.Symbols, ExtractedSymbol{
					Name: name, Kind: "Function",
					QualifiedName: joinQN(mod, name), ExtractionConfidence: 1.0,
				})
			}
		}
	case "struct_item":
		if name := r.nameOfType(n, src, "type_identifier"); name != "" {
			fr.Symbols = append(fr.Symbols, ExtractedSymbol{Name: name, Kind: "Class", QualifiedName: joinQN(mod, name), ExtractionConfidence: 1.0})
		}
	case "enum_item":
		if name := r.nameOfType(n, src, "type_identifier"); name != "" {
			fr.Symbols = append(fr.Symbols, ExtractedSymbol{Name: name, Kind: "Enum", QualifiedName: joinQN(mod, name), ExtractionConfidence: 1.0})
		}
	case "trait_item":
		if name := r.nameOfType(n, src, "type_identifier"); name != "" {
			fr.Symbols = append(fr.Symbols, ExtractedSymbol{Name: name, Kind: "Interface", QualifiedName: joinQN(mod, name), ExtractionConfidence: 1.0})
		}
	case "use_declaration":
		for _, t := range r.useTargets(n, src) {
			fr.Edges = append(fr.Edges, ExtractedEdge{FromQN: mod, ToName: t, Kind: "IMPORTS", Confidence: 1.0})
		}
	case "call_expression":
		if callee := r.calleeName(n, src); callee != "" {
			fr.Edges = append(fr.Edges, ExtractedEdge{FromQN: joinQN(mod, parentType), ToName: callee, Kind: "CALLS", Confidence: 1.0})
		}
	}

	childParent := parentType
	switch r.kind(n) {
	case "impl_item":
		// impl [Trait for] Type — the receiver Type is the last type_identifier
		// before the declaration_list body.
		var last string
		for i := 0; i < r.ncount(n); i++ {
			c := r.nchild(n, i)
			if r.kind(c) == "declaration_list" {
				break
			}
			if k := r.kind(c); k == "type_identifier" || k == "generic_type" || k == "scoped_type_identifier" {
				last = r.text(c, src)
			}
		}
		if last != "" {
			childParent = last
		}
	case "trait_item":
		if name := r.nameOfType(n, src, "type_identifier"); name != "" {
			childParent = name
		}
	}
	for i := 0; i < r.ncount(n); i++ {
		r.walk(r.nchild(n, i), src, mod, childParent, fr)
	}
}

// useTargets enumerates the concrete imported leaf names of a use_declaration
// — the thing the regex importRE truncates at the first '{'.
func (r *rustTSExtractor) useTargets(n tsbridge.Node, src []byte) []string {
	var out []string
	var rec func(m tsbridge.Node)
	rec = func(m tsbridge.Node) {
		switch r.kind(m) {
		case "use_list":
			for i := 0; i < r.ncount(m); i++ {
				rec(r.nchild(m, i))
			}
			return
		case "scoped_identifier", "identifier":
			// final path segment
			name := r.nameOfType(m, src, "identifier")
			if name == "" {
				name = r.text(m, src)
			}
			out = append(out, lastSeg(name))
			return
		case "use_as_clause":
			if a := r.nameOfType(m, src, "identifier"); a != "" {
				out = append(out, a)
			}
			return
		}
		for i := 0; i < r.ncount(m); i++ {
			rec(r.nchild(m, i))
		}
	}
	rec(n)
	return out
}

func (r *rustTSExtractor) calleeName(n tsbridge.Node, src []byte) string {
	if r.ncount(n) == 0 {
		return ""
	}
	fn := r.nchild(n, 0) // the function being called
	switch r.kind(fn) {
	case "identifier":
		return r.text(fn, src)
	case "field_expression":
		if id := r.nameOfType(fn, src, "field_identifier"); id != "" {
			return id
		}
	case "scoped_identifier":
		return lastSeg(r.text(fn, src))
	}
	return lastSeg(r.text(fn, src))
}

func lastSeg(s string) string {
	for i := len(s) - 1; i >= 1; i-- {
		if s[i] == ':' && s[i-1] == ':' {
			return s[i+1:]
		}
	}
	return s
}
