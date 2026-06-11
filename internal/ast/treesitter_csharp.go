// SPDX-License-Identifier: MIT

// Real-tree-sitter C# extractor (ADR-0008 / #1958). Mirrors the Java
// extractor (treesitter_java.go) — C# shares the "." module separator and
// the same QN/Parent/CALLS conventions as the regex tier. Maps tree-sitter
// nodes onto pincher's ExtractedSymbol/Edge shape.
//
// Like the regex tier (and the Rust/Java extractors), this is scope-blind to
// C# `namespace` declarations: QNs are keyed on moduleQN(relPath) so AST-tier
// symbol IDs stay identical to the regex tier. (Namespace-aware QNs are a
// deliberate later churn, mirroring the deferred Rust mod-block handling.)

package ast

import (
	"context"
	"strings"

	"github.com/kwad77/pincher/internal/tsbridge"
)

type csharpTSExtractor struct {
	ts        tsbridge.TreeSitter
	lang      tsbridge.Language
	p         tsbridge.Parser
	ctx       context.Context
	allocated []tsbridge.Node
}

func newCSharpTSExtractor(ctx context.Context) (*csharpTSExtractor, error) {
	ts, err := tsbridge.New(ctx)
	if err != nil {
		return nil, err
	}
	lang, err := ts.LanguageCSharp(ctx)
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
	return &csharpTSExtractor{ts: ts, lang: lang, p: p, ctx: ctx}, nil
}

func (c *csharpTSExtractor) kind(n tsbridge.Node) string { k, _ := n.Kind(c.ctx); return k }
func (c *csharpTSExtractor) ncount(n tsbridge.Node) int {
	cc, _ := n.NamedChildCount(c.ctx)
	return int(cc)
}
func (c *csharpTSExtractor) nchild(n tsbridge.Node, i int) tsbridge.Node {
	cc, _ := n.NamedChild(c.ctx, uint64(i))
	c.allocated = append(c.allocated, cc)
	return cc
}
func (c *csharpTSExtractor) text(n tsbridge.Node, src []byte) string {
	s, _ := n.StartByte(c.ctx)
	e, _ := n.EndByte(c.ctx)
	if int(s) <= int(e) && int(e) <= len(src) {
		return string(src[s:e])
	}
	return ""
}

func (c *csharpTSExtractor) span(n tsbridge.Node, src []byte) (sb, eb, sl, el int) {
	s, _ := n.StartByte(c.ctx)
	e, _ := n.EndByte(c.ctx)
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

func (c *csharpTSExtractor) addSym(fr *FileResult, name, kind, qn, parent string, n tsbridge.Node, src []byte) {
	sb, eb, sl, el := c.span(n, src)
	fr.Symbols = append(fr.Symbols, ExtractedSymbol{
		Name: name, Kind: kind, QualifiedName: qn, Parent: parent,
		StartByte: sb, EndByte: eb, StartLine: sl, EndLine: el,
		ExtractionConfidence: 1.0,
		IsExported:           true, // match the regex tier: C# visibility is keyword-based; treat every symbol as exported.
	})
}

// typeName returns the declared name of a type declaration: the first direct
// named `identifier` child (the name precedes type-parameter and base lists,
// which are separate nested nodes).
func (c *csharpTSExtractor) typeName(n tsbridge.Node, src []byte) string {
	for i := 0; i < c.ncount(n); i++ {
		if ch := c.nchild(n, i); c.kind(ch) == "identifier" {
			return c.text(ch, src)
		}
	}
	return ""
}

// memberName returns a method/constructor's name: the last direct named
// `identifier` appearing before the `parameter_list`. Taking the last (not
// first) identifier skips a bare-identifier return type (C# return types are
// often plain `identifier`, unlike Java's `type_identifier`); stopping at
// `parameter_list` avoids a generic type-parameter identifier (which is
// nested in `type_parameter_list`, after the name).
func (c *csharpTSExtractor) memberName(n tsbridge.Node, src []byte) string {
	name := ""
	for i := 0; i < c.ncount(n); i++ {
		ch := c.nchild(n, i)
		k := c.kind(ch)
		if k == "parameter_list" {
			break
		}
		if k == "identifier" {
			name = c.text(ch, src)
		}
	}
	return name
}

func (c *csharpTSExtractor) extract(source []byte, relPath string) *FileResult {
	fr, _ := c.extractChecked(source, relPath)
	return fr
}

func (c *csharpTSExtractor) extractChecked(source []byte, relPath string) (*FileResult, bool) {
	mod := moduleQN(relPath, ".")
	tree, err := c.p.ParseString(c.ctx, string(source))
	if err != nil {
		return &FileResult{Module: mod}, false
	}
	c.allocated = c.allocated[:0]
	defer func() {
		for i := range c.allocated {
			_ = c.allocated[i].Free(c.ctx)
		}
		c.allocated = c.allocated[:0]
		_ = tree.Close(c.ctx)
	}()
	root, err := tree.RootNode(c.ctx)
	if err != nil {
		return &FileResult{Module: mod}, false
	}
	c.allocated = append(c.allocated, root)
	fr := &FileResult{Module: mod}
	hadErr := c.walk(root, source, csharpWalkCtx{scope: mod}, fr)
	if hadErr {
		return &FileResult{Module: mod}, false
	}
	fr.ConfidenceOverride = 1.0
	return fr, true
}

type csharpWalkCtx struct {
	scope     string // module path (moduleQN of the file) — namespace-blind, matching the regex tier
	typeScope string // scope.Type — Parent + QN prefix for members
	caller    string // enclosing method/constructor QN — FromQN for CALLS edges
}

func (c *csharpTSExtractor) walk(n tsbridge.Node, src []byte, ctx csharpWalkCtx, fr *FileResult) bool {
	hadErr := false
	if e, _ := n.IsError(c.ctx); e {
		hadErr = true
	}
	child := ctx
	switch c.kind(n) {
	case "class_declaration", "struct_declaration", "record_declaration", "record_struct_declaration":
		if name := c.typeName(n, src); name != "" {
			qn := joinDot(ctx.scope, name)
			c.addSym(fr, name, "Class", qn, c.baseType(n, src), n, src)
			child.typeScope = qn
		}
	case "interface_declaration":
		if name := c.typeName(n, src); name != "" {
			qn := joinDot(ctx.scope, name)
			c.addSym(fr, name, "Interface", qn, "", n, src)
			child.typeScope = qn
		}
	case "enum_declaration":
		if name := c.typeName(n, src); name != "" {
			qn := joinDot(ctx.scope, name)
			c.addSym(fr, name, "Enum", qn, "", n, src)
			child.typeScope = qn
		}
	case "method_declaration", "constructor_declaration", "destructor_declaration", "operator_declaration":
		if name := c.memberName(n, src); name != "" {
			var qn, parent, kind string
			if ctx.typeScope != "" {
				kind = "Method"
				qn = joinDot(ctx.typeScope, name)
				parent = ctx.typeScope
			} else {
				kind = "Function"
				qn = joinDot(ctx.scope, name)
			}
			c.addSym(fr, name, kind, qn, parent, n, src)
			child.caller = qn
			child.typeScope = "" // a local type inside a method body is not a member of the enclosing type
		}
	case "using_directive":
		if t := c.usingTarget(n, src); t != "" {
			fr.Edges = append(fr.Edges, ExtractedEdge{FromQN: ctx.scope, ToName: t, Kind: "IMPORTS", Confidence: 1.0})
		}
	case "invocation_expression":
		if callee := c.calleeName(n, src); callee != "" {
			from := ctx.caller
			if from == "" {
				from = ctx.scope
			}
			fr.Edges = append(fr.Edges, ExtractedEdge{FromQN: from, ToName: callee, Kind: "CALLS", Confidence: 0.6})
		}
	}
	for i := 0; i < c.ncount(n); i++ {
		if c.walk(c.nchild(n, i), src, child, fr) {
			hadErr = true
		}
	}
	return hadErr
}

// baseType returns the bare name of a type declaration's first base type
// (base class or interface), or "" — the closest analogue to the regex
// tier's `parent` capture. Parent is not part of the symbol ID, so an
// imperfect match here causes no ID churn.
func (c *csharpTSExtractor) baseType(n tsbridge.Node, src []byte) string {
	for i := 0; i < c.ncount(n); i++ {
		ch := c.nchild(n, i)
		if c.kind(ch) == "base_list" {
			for k := 0; k < c.ncount(ch); k++ {
				return baseTypeName(c.text(c.nchild(ch, k), src))
			}
		}
	}
	return ""
}

// usingTarget returns the namespace/type path of a using_directive, e.g.
// "System.Text" for `using System.Text;`. The C# regex tier has no import
// pattern, so these IMPORTS edges are net-new AST-tier signal.
func (c *csharpTSExtractor) usingTarget(n tsbridge.Node, src []byte) string {
	for i := 0; i < c.ncount(n); i++ {
		ch := c.nchild(n, i)
		switch c.kind(ch) {
		case "qualified_name", "identifier":
			return c.text(ch, src)
		}
	}
	return ""
}

// calleeName returns the invoked method's name for an invocation_expression:
// a bare `identifier` call, or the trailing member name of a
// `member_access_expression` (`obj.Method()` → "Method").
func (c *csharpTSExtractor) calleeName(n tsbridge.Node, src []byte) string {
	if c.ncount(n) == 0 {
		return ""
	}
	fn := c.nchild(n, 0) // the function/callee expression
	switch c.kind(fn) {
	case "identifier":
		return c.text(fn, src)
	case "member_access_expression":
		// trailing identifier is the member name
		name := ""
		for i := 0; i < c.ncount(fn); i++ {
			ch := c.nchild(fn, i)
			if c.kind(ch) == "identifier" {
				name = c.text(ch, src)
			}
		}
		return name
	case "generic_name":
		return c.typeName(fn, src)
	}
	return ""
}
