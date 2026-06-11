// SPDX-License-Identifier: MIT

// Real-tree-sitter PHP extractor (ADR-0008, Phase 2). Mirrors the C#/Java
// extractors but for PHP node types and the "\" module separator. Namespace-
// blind (keyed on moduleQN) to match the regex tier's QN convention, so AST-
// tier symbol IDs stay stable. A `trait` is a scope-only container (the regex
// tier's scopeRE emits no symbol for it), so its methods parent to it but the
// trait itself produces no Class symbol.

package ast

import (
	"context"
	"strings"

	"github.com/kwad77/pincher/internal/tsbridge"
)

type phpTSExtractor struct {
	ts        tsbridge.TreeSitter
	lang      tsbridge.Language
	p         tsbridge.Parser
	ctx       context.Context
	allocated []tsbridge.Node
}

func newPHPTSExtractor(ctx context.Context) (*phpTSExtractor, error) {
	ts, err := tsbridge.New(ctx)
	if err != nil {
		return nil, err
	}
	lang, err := ts.LanguagePHP(ctx)
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
	return &phpTSExtractor{ts: ts, lang: lang, p: p, ctx: ctx}, nil
}

func (x *phpTSExtractor) kind(n tsbridge.Node) string { k, _ := n.Kind(x.ctx); return k }
func (x *phpTSExtractor) ncount(n tsbridge.Node) int {
	c, _ := n.NamedChildCount(x.ctx)
	return int(c)
}
func (x *phpTSExtractor) nchild(n tsbridge.Node, i int) tsbridge.Node {
	c, _ := n.NamedChild(x.ctx, uint64(i))
	x.allocated = append(x.allocated, c)
	return c
}
func (x *phpTSExtractor) text(n tsbridge.Node, src []byte) string {
	s, _ := n.StartByte(x.ctx)
	e, _ := n.EndByte(x.ctx)
	if int(s) <= int(e) && int(e) <= len(src) {
		return string(src[s:e])
	}
	return ""
}

func (x *phpTSExtractor) span(n tsbridge.Node, src []byte) (sb, eb, sl, el int) {
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

func (x *phpTSExtractor) addSym(fr *FileResult, name, kind, qn, parent string, n tsbridge.Node, src []byte) {
	sb, eb, sl, el := x.span(n, src)
	fr.Symbols = append(fr.Symbols, ExtractedSymbol{
		Name: name, Kind: kind, QualifiedName: qn, Parent: parent,
		StartByte: sb, EndByte: eb, StartLine: sl, EndLine: el,
		ExtractionConfidence: 1.0,
		IsExported:           true, // match the regex tier: PHP visibility is keyword-based; treat every symbol as exported.
	})
}

func (x *phpTSExtractor) nameOfType(n tsbridge.Node, src []byte, types ...string) string {
	for i := 0; i < x.ncount(n); i++ {
		c := x.nchild(n, i)
		k := x.kind(c)
		for _, t := range types {
			if k == t {
				return x.text(c, src)
			}
		}
	}
	return ""
}

// joinBS joins a QN prefix and a name with PHP's "\" namespace separator.
func joinBS(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return a + "\\" + b
}

func (x *phpTSExtractor) extract(source []byte, relPath string) *FileResult {
	fr, _ := x.extractChecked(source, relPath)
	return fr
}

func (x *phpTSExtractor) extractChecked(source []byte, relPath string) (*FileResult, bool) {
	mod := moduleQN(relPath, "\\")
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
	hadErr := x.walk(root, source, phpWalkCtx{scope: mod}, fr)
	if hadErr {
		return &FileResult{Module: mod}, false
	}
	fr.ConfidenceOverride = 1.0
	return fr, true
}

type phpWalkCtx struct {
	scope     string // moduleQN of the file (namespace-blind, matching the regex tier)
	container string // scope\Type — Parent + QN prefix for members; "" at file scope
	enclosing string // enclosing function/method QN — CALLS FromQN
}

func (x *phpTSExtractor) walk(n tsbridge.Node, src []byte, c phpWalkCtx, fr *FileResult) bool {
	hadErr := false
	if e, _ := n.IsError(x.ctx); e {
		hadErr = true
	}
	child := c
	switch x.kind(n) {
	case "class_declaration":
		if name := x.nameOfType(n, src, "name"); name != "" {
			qn := joinBS(c.scope, name)
			x.addSym(fr, name, "Class", qn, x.baseClass(n, src), n, src)
			child.container, child.enclosing = qn, ""
		}
	case "interface_declaration":
		if name := x.nameOfType(n, src, "name"); name != "" {
			qn := joinBS(c.scope, name)
			x.addSym(fr, name, "Interface", qn, "", n, src)
			child.container, child.enclosing = qn, ""
		}
	case "enum_declaration":
		if name := x.nameOfType(n, src, "name"); name != "" {
			qn := joinBS(c.scope, name)
			x.addSym(fr, name, "Enum", qn, "", n, src)
			child.container, child.enclosing = qn, ""
		}
	case "trait_declaration":
		// A trait is a scope-only container (regex scopeRE): its methods parent
		// to it, but it emits no Class symbol of its own.
		if name := x.nameOfType(n, src, "name"); name != "" {
			child.container, child.enclosing = joinBS(c.scope, name), ""
		}
	case "method_declaration":
		if name := x.nameOfType(n, src, "name"); name != "" {
			var qn, parent, kind string
			if c.container != "" {
				kind, qn, parent = "Method", joinBS(c.container, name), c.container
			} else {
				kind, qn = "Function", joinBS(c.scope, name)
			}
			x.addSym(fr, name, kind, qn, parent, n, src)
			child.enclosing, child.container = qn, ""
		}
	case "function_definition":
		if name := x.nameOfType(n, src, "name"); name != "" {
			qn := joinBS(c.scope, name)
			x.addSym(fr, name, "Function", qn, "", n, src)
			child.enclosing, child.container = qn, ""
		}
	case "namespace_use_declaration":
		for _, t := range x.useTargets(n, src) {
			fr.Edges = append(fr.Edges, ExtractedEdge{FromQN: c.scope, ToName: t, Kind: "IMPORTS", Confidence: 1.0})
		}
	case "function_call_expression", "member_call_expression", "scoped_call_expression", "nullsafe_member_call_expression":
		if callee := x.calleeName(n, src); callee != "" {
			fr.Edges = append(fr.Edges, ExtractedEdge{FromQN: x.from(c), ToName: callee, Kind: "CALLS", Confidence: 0.6})
		}
	case "object_creation_expression":
		if t := x.ctorTypeName(n, src); t != "" {
			fr.Edges = append(fr.Edges, ExtractedEdge{FromQN: x.from(c), ToName: t, Kind: "CALLS", Confidence: 0.6})
		}
	}
	for i := 0; i < x.ncount(n); i++ {
		if x.walk(x.nchild(n, i), src, child, fr) {
			hadErr = true
		}
	}
	return hadErr
}

func (x *phpTSExtractor) from(c phpWalkCtx) string {
	if c.enclosing != "" {
		return c.enclosing
	}
	return c.scope
}

// baseClass returns the bare name of a class's `extends` target, or "".
func (x *phpTSExtractor) baseClass(n tsbridge.Node, src []byte) string {
	for i := 0; i < x.ncount(n); i++ {
		ch := x.nchild(n, i)
		if x.kind(ch) == "base_clause" {
			return lastSegBS(x.nameOfType(ch, src, "name", "qualified_name"))
		}
	}
	return ""
}

// useTargets enumerates the imported names of a `use Foo\Bar, Baz\Qux;`
// declaration — net-new signal (the PHP regex tier has no import pattern).
func (x *phpTSExtractor) useTargets(n tsbridge.Node, src []byte) []string {
	var out []string
	var rec func(m tsbridge.Node)
	rec = func(m tsbridge.Node) {
		switch x.kind(m) {
		case "qualified_name", "name":
			if t := x.text(m, src); t != "" {
				out = append(out, t)
			}
			return
		}
		for i := 0; i < x.ncount(m); i++ {
			rec(x.nchild(m, i))
		}
	}
	rec(n)
	return out
}

// calleeName returns the invoked name for a call expression: the function name
// for a free call, or the method name for `$o->m()` / `C::m()`.
func (x *phpTSExtractor) calleeName(n tsbridge.Node, src []byte) string {
	switch x.kind(n) {
	case "function_call_expression":
		if x.ncount(n) > 0 {
			fn := x.nchild(n, 0)
			switch x.kind(fn) {
			case "name":
				return x.text(fn, src)
			case "qualified_name":
				return lastSegBS(x.text(fn, src))
			}
		}
	case "member_call_expression", "scoped_call_expression", "nullsafe_member_call_expression":
		// The method name is a `name` child that is not the receiver.
		return x.lastName(n, src)
	}
	return ""
}

// lastName returns the text of the last direct `name` child — the member /
// method name in a `$o->m()` or `C::m()` call.
func (x *phpTSExtractor) lastName(n tsbridge.Node, src []byte) string {
	name := ""
	for i := 0; i < x.ncount(n); i++ {
		ch := x.nchild(n, i)
		if x.kind(ch) == "name" {
			name = x.text(ch, src)
		}
	}
	return name
}

// ctorTypeName returns the bare type name of a `new Foo()` object creation.
func (x *phpTSExtractor) ctorTypeName(n tsbridge.Node, src []byte) string {
	for i := 0; i < x.ncount(n); i++ {
		ch := x.nchild(n, i)
		switch x.kind(ch) {
		case "name":
			return x.text(ch, src)
		case "qualified_name":
			return lastSegBS(x.text(ch, src))
		}
	}
	return ""
}

// lastSegBS returns the final segment of a PHP backslash-qualified name
// ("App\\Models\\User" → "User").
func lastSegBS(s string) string {
	if i := strings.LastIndexByte(s, '\\'); i >= 0 {
		return s[i+1:]
	}
	return s
}
