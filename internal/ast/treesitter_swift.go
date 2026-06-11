// SPDX-License-Identifier: MIT

// Real-tree-sitter Swift extractor (ADR-0008, Phase 2). Swift's community
// grammar uses non-standard node names (verified empirically): a single
// `class_declaration` node covers struct/class/actor (→Class), enum (body is
// `enum_class_body` →Enum), AND extension (first named child is `user_type`
// →scope-only, like a Rust impl / PHP trait); `protocol_declaration` →Interface
// with `protocol_function_declaration` members. modSep is "." and there are no
// namespaces, so QNs are keyed on moduleQN to match the regex tier.

package ast

import (
	"context"
	"strings"

	"github.com/kwad77/pincher/internal/tsbridge"
)

type swiftTSExtractor struct {
	ts        tsbridge.TreeSitter
	lang      tsbridge.Language
	p         tsbridge.Parser
	ctx       context.Context
	allocated []tsbridge.Node
}

func newSwiftTSExtractor(ctx context.Context) (*swiftTSExtractor, error) {
	ts, err := tsbridge.New(ctx)
	if err != nil {
		return nil, err
	}
	lang, err := ts.LanguageSwift(ctx)
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
	return &swiftTSExtractor{ts: ts, lang: lang, p: p, ctx: ctx}, nil
}

func (x *swiftTSExtractor) kind(n tsbridge.Node) string { k, _ := n.Kind(x.ctx); return k }
func (x *swiftTSExtractor) ncount(n tsbridge.Node) int {
	c, _ := n.NamedChildCount(x.ctx)
	return int(c)
}
func (x *swiftTSExtractor) nchild(n tsbridge.Node, i int) tsbridge.Node {
	c, _ := n.NamedChild(x.ctx, uint64(i))
	x.allocated = append(x.allocated, c)
	return c
}
func (x *swiftTSExtractor) text(n tsbridge.Node, src []byte) string {
	s, _ := n.StartByte(x.ctx)
	e, _ := n.EndByte(x.ctx)
	if int(s) <= int(e) && int(e) <= len(src) {
		return string(src[s:e])
	}
	return ""
}

func (x *swiftTSExtractor) span(n tsbridge.Node, src []byte) (sb, eb, sl, el int) {
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

func (x *swiftTSExtractor) addSym(fr *FileResult, name, kind, qn, parent string, n tsbridge.Node, src []byte) {
	sb, eb, sl, el := x.span(n, src)
	fr.Symbols = append(fr.Symbols, ExtractedSymbol{
		Name: name, Kind: kind, QualifiedName: qn, Parent: parent,
		StartByte: sb, EndByte: eb, StartLine: sl, EndLine: el,
		ExtractionConfidence: 1.0,
		IsExported:           true, // match the regex tier: treat every symbol as exported.
	})
}

func (x *swiftTSExtractor) childOfKind(n tsbridge.Node, kinds ...string) (tsbridge.Node, bool) {
	for i := 0; i < x.ncount(n); i++ {
		c := x.nchild(n, i)
		k := x.kind(c)
		for _, want := range kinds {
			if k == want {
				return c, true
			}
		}
	}
	return tsbridge.Node{}, false
}

func (x *swiftTSExtractor) textOfKind(n tsbridge.Node, src []byte, kinds ...string) string {
	if c, ok := x.childOfKind(n, kinds...); ok {
		return x.text(c, src)
	}
	return ""
}

func (x *swiftTSExtractor) extract(source []byte, relPath string) *FileResult {
	fr, _ := x.extractChecked(source, relPath)
	return fr
}

func (x *swiftTSExtractor) extractChecked(source []byte, relPath string) (*FileResult, bool) {
	mod := moduleQN(relPath, ".")
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
	hadErr := x.walk(root, source, swiftWalkCtx{scope: mod}, fr)
	if hadErr {
		return &FileResult{Module: mod}, false
	}
	fr.ConfidenceOverride = 1.0
	return fr, true
}

type swiftWalkCtx struct {
	scope     string // moduleQN of the file
	container string // scope.Type — Parent + QN prefix for members; "" at file scope
	enclosing string // enclosing function/method QN — CALLS FromQN
}

func (x *swiftTSExtractor) from(c swiftWalkCtx) string {
	if c.enclosing != "" {
		return c.enclosing
	}
	return c.scope
}

func (x *swiftTSExtractor) walk(n tsbridge.Node, src []byte, c swiftWalkCtx, fr *FileResult) bool {
	hadErr := false
	if e, _ := n.IsError(x.ctx); e {
		hadErr = true
	}
	child := c
	switch x.kind(n) {
	case "class_declaration":
		// One node for struct/class/actor (→Class), enum (enum_class_body
		// →Enum) and extension (first named child is user_type →scope-only).
		if first := x.firstTypeNode(n, src); first.kind == "user_type" {
			// extension Type { … }: scope-only, methods parent to the type.
			if first.name != "" {
				child.container = joinDot(c.scope, first.name)
				child.enclosing = ""
			}
		} else if first.name != "" {
			kind := "Class"
			if _, isEnum := x.childOfKind(n, "enum_class_body"); isEnum {
				kind = "Enum"
			}
			qn := joinDot(c.scope, first.name)
			x.addSym(fr, first.name, kind, qn, x.inheritParent(n, src), n, src)
			child.container = qn
			child.enclosing = ""
		}
	case "protocol_declaration":
		if name := x.textOfKind(n, src, "type_identifier"); name != "" {
			qn := joinDot(c.scope, name)
			x.addSym(fr, name, "Interface", qn, "", n, src)
			child.container = qn
			child.enclosing = ""
		}
	case "function_declaration", "protocol_function_declaration":
		if name := x.textOfKind(n, src, "simple_identifier"); name != "" {
			child = x.declare(n, src, c, name, fr)
		}
	case "init_declaration":
		child = x.declare(n, src, c, "init", fr)
	case "import_declaration":
		if t := x.textOfKind(n, src, "identifier", "simple_identifier"); t != "" {
			fr.Edges = append(fr.Edges, ExtractedEdge{FromQN: c.scope, ToName: lastSeg(t), Kind: "IMPORTS", Confidence: 1.0})
		}
	case "call_expression":
		if callee := x.calleeName(n, src); callee != "" {
			fr.Edges = append(fr.Edges, ExtractedEdge{FromQN: x.from(c), ToName: callee, Kind: "CALLS", Confidence: 0.6})
		}
	}
	for i := 0; i < x.ncount(n); i++ {
		if x.walk(x.nchild(n, i), src, child, fr) {
			hadErr = true
		}
	}
	return hadErr
}

// declare emits a func/init as a Method (inside a type container) or Function
// (top level) and returns the child ctx with enclosing set to its QN.
func (x *swiftTSExtractor) declare(n tsbridge.Node, src []byte, c swiftWalkCtx, name string, fr *FileResult) swiftWalkCtx {
	var qn, parent, kind string
	if c.container != "" {
		kind, qn, parent = "Method", joinDot(c.container, name), c.container
	} else {
		kind, qn = "Function", joinDot(c.scope, name)
	}
	x.addSym(fr, name, kind, qn, parent, n, src)
	child := c
	child.enclosing = qn
	child.container = ""
	return child
}

type swiftTypeNode struct {
	kind string
	name string
}

// firstTypeNode returns the first type-name-bearing named child of a
// class_declaration: a `type_identifier` (struct/class/enum) or a `user_type`
// (extension), with the bare type name extracted.
func (x *swiftTSExtractor) firstTypeNode(n tsbridge.Node, src []byte) swiftTypeNode {
	for i := 0; i < x.ncount(n); i++ {
		c := x.nchild(n, i)
		switch x.kind(c) {
		case "type_identifier":
			return swiftTypeNode{kind: "type_identifier", name: x.text(c, src)}
		case "user_type":
			// extension's `user_type` wraps a type_identifier.
			return swiftTypeNode{kind: "user_type", name: x.textOfKind(c, src, "type_identifier")}
		}
	}
	return swiftTypeNode{}
}

// inheritParent returns the bare name of the first inherited type / conformed
// protocol — the regex `parent` analogue.
func (x *swiftTSExtractor) inheritParent(n tsbridge.Node, src []byte) string {
	if spec, ok := x.childOfKind(n, "inheritance_specifier"); ok {
		return lastSeg(x.textOfKind(spec, src, "user_type", "type_identifier"))
	}
	return ""
}

// calleeName returns the called name for a call_expression: a bare
// simple_identifier (`foo()` / `Foo()`), or the trailing member of a
// navigation_expression (`a.b.c()` → "c").
func (x *swiftTSExtractor) calleeName(n tsbridge.Node, src []byte) string {
	if x.ncount(n) == 0 {
		return ""
	}
	fn := x.nchild(n, 0)
	switch x.kind(fn) {
	case "simple_identifier":
		return x.text(fn, src)
	case "navigation_expression":
		if suf, ok := x.childOfKind(fn, "navigation_suffix"); ok {
			return x.textOfKind(suf, src, "simple_identifier")
		}
	}
	return ""
}
