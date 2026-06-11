// SPDX-License-Identifier: MIT

// Real-tree-sitter Ruby extractor (ADR-0008, Phase 2). modSep is "::".
// class/module → Class (the regex tier maps both to Class via classRE); a
// `method` is a Method inside a class/module else a top-level Function;
// `singleton_method` (`def self.x`) is the same with the receiver skipped. QNs
// are keyed on moduleQN and are namespace-blind (a class inside a module stays
// moduleQN-based, matching the regex tier's single currentClass slot).
// `require`/`require_relative`/`load` calls become net-new IMPORTS.

package ast

import (
	"context"
	"strings"

	"github.com/kwad77/pincher/internal/tsbridge"
)

type rubyTSExtractor struct {
	ts        tsbridge.TreeSitter
	lang      tsbridge.Language
	p         tsbridge.Parser
	ctx       context.Context
	allocated []tsbridge.Node
}

func newRubyTSExtractor(ctx context.Context) (*rubyTSExtractor, error) {
	ts, err := tsbridge.New(ctx)
	if err != nil {
		return nil, err
	}
	lang, err := ts.LanguageRuby(ctx)
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
	return &rubyTSExtractor{ts: ts, lang: lang, p: p, ctx: ctx}, nil
}

func (x *rubyTSExtractor) kind(n tsbridge.Node) string { k, _ := n.Kind(x.ctx); return k }
func (x *rubyTSExtractor) ncount(n tsbridge.Node) int {
	c, _ := n.NamedChildCount(x.ctx)
	return int(c)
}
func (x *rubyTSExtractor) nchild(n tsbridge.Node, i int) tsbridge.Node {
	c, _ := n.NamedChild(x.ctx, uint64(i))
	x.allocated = append(x.allocated, c)
	return c
}
func (x *rubyTSExtractor) text(n tsbridge.Node, src []byte) string {
	s, _ := n.StartByte(x.ctx)
	e, _ := n.EndByte(x.ctx)
	if int(s) <= int(e) && int(e) <= len(src) {
		return string(src[s:e])
	}
	return ""
}

func (x *rubyTSExtractor) span(n tsbridge.Node, src []byte) (sb, eb, sl, el int) {
	s, _ := n.StartByte(x.ctx)
	e, _ := n.EndByte(x.ctx)
	sb, eb = int(s), int(e)
	if eb > len(src) {
		eb = len(src)
	}
	if sb > eb {
		sb = eb
	}
	sl = 1 + strings.Count(string(src[:sb]), "\n")
	el = 1 + strings.Count(string(src[:eb]), "\n")
	return
}

func (x *rubyTSExtractor) addSym(fr *FileResult, name, kind, qn, parent string, n tsbridge.Node, src []byte) {
	sb, eb, sl, el := x.span(n, src)
	fr.Symbols = append(fr.Symbols, ExtractedSymbol{
		Name: name, Kind: kind, QualifiedName: qn, Parent: parent,
		StartByte: sb, EndByte: eb, StartLine: sl, EndLine: el,
		ExtractionConfidence: 1.0,
		IsExported:           true,
	})
}

// joinDC joins a QN prefix and a name with Ruby's "::" separator.
func joinDC(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return a + "::" + b
}

func (x *rubyTSExtractor) extract(source []byte, relPath string) *FileResult {
	fr, _ := x.extractChecked(source, relPath)
	return fr
}

func (x *rubyTSExtractor) extractChecked(source []byte, relPath string) (*FileResult, bool) {
	mod := moduleQN(relPath, "::")
	tree, err := x.p.ParseString(x.ctx, string(source))
	if err != nil {
		return &FileResult{Module: mod}, false
	}
	x.allocated = x.allocated[:0]
	defer func() {
		for i := range x.allocated {
			_ = x.allocated[i].Free(x.ctx)
		}
		x.allocated = x.allocated[:0]
		_ = tree.Close(x.ctx)
	}()
	root, err := tree.RootNode(x.ctx)
	if err != nil {
		return &FileResult{Module: mod}, false
	}
	x.allocated = append(x.allocated, root)
	fr := &FileResult{Module: mod}
	hadErr := x.walk(root, source, rubyWalkCtx{scope: mod}, fr)
	if hadErr {
		return &FileResult{Module: mod}, false
	}
	fr.ConfidenceOverride = 1.0
	return fr, true
}

type rubyWalkCtx struct {
	scope     string // moduleQN of the file (constant; class/module QNs are keyed on this, namespace-blind)
	container string // scope::Type — Parent + QN prefix for methods; "" at file scope
	enclosing string // enclosing method QN — CALLS FromQN
}

func (x *rubyTSExtractor) from(c rubyWalkCtx) string {
	if c.enclosing != "" {
		return c.enclosing
	}
	return c.scope
}

func (x *rubyTSExtractor) walk(n tsbridge.Node, src []byte, c rubyWalkCtx, fr *FileResult) bool {
	hadErr := false
	if e, _ := n.IsError(x.ctx); e {
		hadErr = true
	}
	child := c
	switch x.kind(n) {
	case "class", "module":
		if name := x.typeName(n, src); name != "" {
			qn := joinDC(c.scope, name)
			x.addSym(fr, name, "Class", qn, x.superName(n, src), n, src)
			child.container = qn
			child.enclosing = ""
		}
	case "method", "singleton_method":
		if name := x.methodName(n, src); name != "" {
			var qn, parent, kind string
			if c.container != "" {
				kind, qn, parent = "Method", joinDC(c.container, name), c.container
			} else {
				kind, qn = "Function", joinDC(c.scope, name)
			}
			x.addSym(fr, name, kind, qn, parent, n, src)
			child.enclosing = qn
			child.container = ""
		}
	case "call":
		x.handleCall(n, src, c, fr)
	}
	for i := 0; i < x.ncount(n); i++ {
		if x.walk(x.nchild(n, i), src, child, fr) {
			hadErr = true
		}
	}
	return hadErr
}

// typeName returns a class/module name: the `constant`, or the last segment of
// a `scope_resolution` (`Foo::Bar` → "Bar").
func (x *rubyTSExtractor) typeName(n tsbridge.Node, src []byte) string {
	for i := 0; i < x.ncount(n); i++ {
		c := x.nchild(n, i)
		switch x.kind(c) {
		case "constant":
			return x.text(c, src)
		case "scope_resolution":
			return lastSegDC(x.text(c, src))
		}
	}
	return ""
}

// superName returns the bare superclass name from a class's `superclass` child.
func (x *rubyTSExtractor) superName(n tsbridge.Node, src []byte) string {
	for i := 0; i < x.ncount(n); i++ {
		c := x.nchild(n, i)
		if x.kind(c) == "superclass" {
			for k := 0; k < x.ncount(c); k++ {
				return lastSegDC(x.text(x.nchild(c, k), src))
			}
		}
	}
	return ""
}

// methodName returns a method's name: the last `identifier`/`constant`/
// `operator`/`setter` before the parameters or body, so a `singleton_method`'s
// receiver (`def self.x` / `def Klass.x`) is skipped.
func (x *rubyTSExtractor) methodName(n tsbridge.Node, src []byte) string {
	name := ""
	for i := 0; i < x.ncount(n); i++ {
		c := x.nchild(n, i)
		k := x.kind(c)
		if k == "method_parameters" || k == "body_statement" || k == "bare_parameters" {
			break
		}
		switch k {
		case "identifier", "constant", "operator", "setter":
			name = x.text(c, src)
		}
	}
	return name
}

// handleCall emits an IMPORTS edge for require/require_relative/load with a
// string argument (net-new — the Ruby regex tier has no import pattern), else
// a CALLS edge to the invoked method name.
func (x *rubyTSExtractor) handleCall(n tsbridge.Node, src []byte, c rubyWalkCtx, fr *FileResult) {
	callee := x.calleeName(n, src)
	if callee == "" {
		return
	}
	if callee == "require" || callee == "require_relative" || callee == "load" {
		if s := x.stringArg(n, src); s != "" {
			fr.Edges = append(fr.Edges, ExtractedEdge{FromQN: c.scope, ToName: s, Kind: "IMPORTS", Confidence: 1.0})
			return
		}
	}
	fr.Edges = append(fr.Edges, ExtractedEdge{FromQN: x.from(c), ToName: callee, Kind: "CALLS", Confidence: 0.6})
}

// calleeName returns the invoked method name of a `call`: the last `identifier`
// before the argument_list (skipping any receiver before the `.`).
func (x *rubyTSExtractor) calleeName(n tsbridge.Node, src []byte) string {
	argIdx := -1
	for i := 0; i < x.ncount(n); i++ {
		if x.kind(x.nchild(n, i)) == "argument_list" {
			argIdx = i
			break
		}
	}
	limit := argIdx
	if limit < 0 {
		limit = x.ncount(n)
	}
	name := ""
	for i := 0; i < limit; i++ {
		if x.kind(x.nchild(n, i)) == "identifier" {
			name = x.text(x.nchild(n, i), src)
		}
	}
	return name
}

// stringArg returns the content of the first string argument of a call.
func (x *rubyTSExtractor) stringArg(n tsbridge.Node, src []byte) string {
	for i := 0; i < x.ncount(n); i++ {
		c := x.nchild(n, i)
		if x.kind(c) != "argument_list" {
			continue
		}
		for k := 0; k < x.ncount(c); k++ {
			s := x.nchild(c, k)
			if x.kind(s) == "string" {
				if cnt, ok := x.childOfKind(s, "string_content"); ok {
					return x.text(cnt, src)
				}
				return strings.Trim(x.text(s, src), `"'`)
			}
		}
	}
	return ""
}

func (x *rubyTSExtractor) childOfKind(n tsbridge.Node, kind string) (tsbridge.Node, bool) {
	for i := 0; i < x.ncount(n); i++ {
		c := x.nchild(n, i)
		if x.kind(c) == kind {
			return c, true
		}
	}
	return tsbridge.Node{}, false
}

func lastSegDC(s string) string {
	if i := strings.LastIndex(s, "::"); i >= 0 {
		return s[i+2:]
	}
	return s
}
