// SPDX-License-Identifier: MIT

// Real-tree-sitter C++ extractor (ADR-0008, Phase 2 — the last Phase-2
// language). class/struct/union → Class, enum → Enum, functions/methods →
// Function/Method. modSep is "::" and QNs are keyed on moduleQN
// (namespace-blind, matching the regex tier — C++ `namespace` blocks are
// recursed through without changing the QN base, a deliberate parity choice;
// namespace-aware QNs would churn every symbol ID). An out-of-line method
// definition (`int Foo::bar() {}`) is bound to its class via the qualifier.
// `#include` → net-new IMPORTS. Only language "C++" (.cpp/.cxx/.cc/.hpp/.hh)
// routes here; ".c"/".h" stay on the regex tier.

package ast

import (
	"context"
	"strings"

	"github.com/kwad77/pincher/internal/tsbridge"
)

type cppTSExtractor struct {
	ts        tsbridge.TreeSitter
	lang      tsbridge.Language
	p         tsbridge.Parser
	ctx       context.Context
	allocated []tsbridge.Node
}

func newCppTSExtractor(ctx context.Context) (*cppTSExtractor, error) {
	ts, err := tsbridge.New(ctx)
	if err != nil {
		return nil, err
	}
	lang, err := ts.LanguageCpp(ctx)
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
	return &cppTSExtractor{ts: ts, lang: lang, p: p, ctx: ctx}, nil
}

func (x *cppTSExtractor) kind(n tsbridge.Node) string { k, _ := n.Kind(x.ctx); return k }
func (x *cppTSExtractor) ncount(n tsbridge.Node) int {
	c, _ := n.NamedChildCount(x.ctx)
	return int(c)
}
func (x *cppTSExtractor) nchild(n tsbridge.Node, i int) tsbridge.Node {
	c, _ := n.NamedChild(x.ctx, uint64(i))
	x.allocated = append(x.allocated, c)
	return c
}
func (x *cppTSExtractor) text(n tsbridge.Node, src []byte) string {
	s, _ := n.StartByte(x.ctx)
	e, _ := n.EndByte(x.ctx)
	if int(s) <= int(e) && int(e) <= len(src) {
		return string(src[s:e])
	}
	return ""
}

func (x *cppTSExtractor) span(n tsbridge.Node, src []byte) (sb, eb, sl, el int) {
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

func (x *cppTSExtractor) addSym(fr *FileResult, name, kind, qn, parent string, n tsbridge.Node, src []byte) {
	sb, eb, sl, el := x.span(n, src)
	fr.Symbols = append(fr.Symbols, ExtractedSymbol{
		Name: name, Kind: kind, QualifiedName: qn, Parent: parent,
		StartByte: sb, EndByte: eb, StartLine: sl, EndLine: el,
		ExtractionConfidence: 1.0,
		IsExported:           true,
	})
}

func (x *cppTSExtractor) childOfKind(n tsbridge.Node, kinds ...string) (tsbridge.Node, bool) {
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

func (x *cppTSExtractor) extract(source []byte, relPath string) *FileResult {
	fr, _ := x.extractChecked(source, relPath)
	return fr
}

func (x *cppTSExtractor) extractChecked(source []byte, relPath string) (*FileResult, bool) {
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
	hadErr := x.walk(root, source, cppWalkCtx{scope: mod}, fr)
	if hadErr {
		return &FileResult{Module: mod}, false
	}
	fr.ConfidenceOverride = 1.0
	return fr, true
}

type cppWalkCtx struct {
	scope     string // moduleQN of the file (namespace-blind)
	container string // scope::Type — Parent + QN prefix for in-class methods; "" at file scope
	enclosing string // enclosing function/method QN — CALLS FromQN
}

func (x *cppTSExtractor) from(c cppWalkCtx) string {
	if c.enclosing != "" {
		return c.enclosing
	}
	return c.scope
}

func (x *cppTSExtractor) walk(n tsbridge.Node, src []byte, c cppWalkCtx, fr *FileResult) bool {
	hadErr := false
	if e, _ := n.IsError(x.ctx); e {
		hadErr = true
	}
	child := c
	switch x.kind(n) {
	case "class_specifier", "struct_specifier", "union_specifier":
		// Only a real definition (with a member list) is a Class. A
		// body-less `class Foo;` (forward declaration) or `class MODULE_API`
		// fragment from an export-macro mis-parse is skipped — matching the
		// regex tier's dropCForwardDecls / export-macro handling (#1693).
		if _, hasBody := x.childOfKind(n, "field_declaration_list"); hasBody {
			if name := x.typeName(n, src); name != "" {
				qn := joinDC(c.scope, name)
				x.addSym(fr, name, "Class", qn, x.baseClass(n, src), n, src)
				child.container = qn
				child.enclosing = ""
			}
		}
	case "enum_specifier":
		if name := x.typeName(n, src); name != "" {
			x.addSym(fr, name, "Enum", joinDC(c.scope, name), "", n, src)
			child.enclosing = ""
		}
	case "function_definition", "declaration", "field_declaration":
		// Export-macro mis-parse recovery (#1693): `class MODULE_API Name {…}`
		// is read by tree-sitter as a function_definition whose return type is
		// a (body-less) class/struct_specifier and whose "name" is a bare
		// identifier — the real type name. Emit that as the Class and recurse
		// the body as its members.
		if cs, ok := x.childOfKind(n, "class_specifier", "struct_specifier", "union_specifier"); ok {
			if _, hasBody := x.childOfKind(cs, "field_declaration_list"); !hasBody {
				if id, ok := x.childOfKind(n, "identifier", "type_identifier"); ok {
					name := x.text(id, src)
					qn := joinDC(c.scope, name)
					x.addSym(fr, name, "Class", qn, "", n, src)
					child.container = qn
					child.enclosing = ""
					for i := 0; i < x.ncount(n); i++ {
						if x.walk(x.nchild(n, i), src, child, fr) {
							hadErr = true
						}
					}
					return hadErr
				}
			}
		}
		// Each can carry a function_declarator (a method/function with or
		// without a body). Emit the callable; only function_definition opens
		// a new enclosing scope for the calls in its body. Constructors and
		// destructors (no return type) are skipped to match the regex tier,
		// whose funcRE requires a return-type token — and to avoid a Method
		// shadowing its own Class on the constructor's name.
		if name, qualifier, ok := x.funcName(n, src); ok && name != "" && x.hasReturnType(n) {
			var qn, parent, kind string
			switch {
			case qualifier != "":
				// Out-of-line definition `Ret Class::method() {…}`.
				kind = "Method"
				parent = joinDC(c.scope, qualifier)
				qn = joinDC(parent, name)
			case c.container != "":
				kind, qn, parent = "Method", joinDC(c.container, name), c.container
			default:
				kind, qn = "Function", joinDC(c.scope, name)
			}
			x.addSym(fr, name, kind, qn, parent, n, src)
			if x.kind(n) == "function_definition" {
				child.enclosing = qn
				child.container = ""
			}
		}
	case "preproc_include":
		if t := x.includeTarget(n, src); t != "" {
			fr.Edges = append(fr.Edges, ExtractedEdge{FromQN: c.scope, ToName: t, Kind: "IMPORTS", Confidence: 1.0})
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

// hasReturnType reports whether a function_definition/declaration carries a
// return-type node before its function_declarator — false for a constructor or
// destructor (which the regex tier also does not emit).
func (x *cppTSExtractor) hasReturnType(n tsbridge.Node) bool {
	for i := 0; i < x.ncount(n); i++ {
		switch x.kind(x.nchild(n, i)) {
		case "function_declarator", "pointer_declarator", "reference_declarator", "parenthesized_declarator", "init_declarator":
			return false // hit the declarator first → no return type
		case "primitive_type", "type_identifier", "sized_type_specifier", "qualified_identifier",
			"template_type", "placeholder_type_specifier", "auto", "scoped_type_identifier",
			"struct_specifier", "class_specifier", "enum_specifier", "union_specifier", "type_descriptor",
			"dependent_type", "decltype":
			return true
		}
	}
	return false
}

func (x *cppTSExtractor) typeName(n tsbridge.Node, src []byte) string {
	if c, ok := x.childOfKind(n, "type_identifier"); ok {
		return x.text(c, src)
	}
	return ""
}

// baseClass returns the bare name of the first base in a base_class_clause.
func (x *cppTSExtractor) baseClass(n tsbridge.Node, src []byte) string {
	if bc, ok := x.childOfKind(n, "base_class_clause"); ok {
		if t, ok := x.childOfKind(bc, "type_identifier"); ok {
			return x.text(t, src)
		}
		return baseTypeName(lastSegDC(x.text(bc, src)))
	}
	return ""
}

// funcName extracts a function/method name from a node carrying a
// function_declarator, returning (name, qualifier, ok). qualifier is the class
// name for an out-of-line `Class::method` definition, else "".
func (x *cppTSExtractor) funcName(n tsbridge.Node, src []byte) (name, qualifier string, ok bool) {
	fd, found := x.findFuncDeclarator(n)
	if !found {
		return "", "", false
	}
	// The declarator part of a function_declarator is its first named child
	// that is an identifier-ish node.
	for i := 0; i < x.ncount(fd); i++ {
		ch := x.nchild(fd, i)
		switch x.kind(ch) {
		case "identifier", "field_identifier", "destructor_name", "operator_name":
			return x.text(ch, src), "", true
		case "qualified_identifier":
			// `Class::method` (possibly nested `A::B::method`).
			full := x.text(ch, src)
			return lastSegDC(full), x.qualifierOf(full), true
		}
	}
	return "", "", false
}

// findFuncDeclarator descends pointer/reference/parenthesized declarators to
// the function_declarator, if any.
func (x *cppTSExtractor) findFuncDeclarator(n tsbridge.Node) (tsbridge.Node, bool) {
	for i := 0; i < x.ncount(n); i++ {
		ch := x.nchild(n, i)
		switch x.kind(ch) {
		case "function_declarator":
			return ch, true
		case "pointer_declarator", "reference_declarator", "parenthesized_declarator", "init_declarator":
			if fd, ok := x.findFuncDeclarator(ch); ok {
				return fd, true
			}
		}
	}
	return tsbridge.Node{}, false
}

// qualifierOf returns the class qualifier of `A::B::method` → "B" (the
// immediately-enclosing class), or "" for an unqualified name.
func (x *cppTSExtractor) qualifierOf(full string) string {
	i := strings.LastIndex(full, "::")
	if i < 0 {
		return ""
	}
	left := full[:i]
	if j := strings.LastIndex(left, "::"); j >= 0 {
		return left[j+2:]
	}
	return left
}

// includeTarget returns the included path (`<vector>` → "vector",
// `"foo.h"` → "foo.h").
func (x *cppTSExtractor) includeTarget(n tsbridge.Node, src []byte) string {
	for i := 0; i < x.ncount(n); i++ {
		ch := x.nchild(n, i)
		switch x.kind(ch) {
		case "system_lib_string", "string_literal", "string_content":
			return strings.Trim(x.text(ch, src), `<>"`)
		}
	}
	return ""
}

// calleeName returns the called function/method name of a call_expression.
func (x *cppTSExtractor) calleeName(n tsbridge.Node, src []byte) string {
	if x.ncount(n) == 0 {
		return ""
	}
	fn := x.nchild(n, 0)
	switch x.kind(fn) {
	case "identifier":
		return x.text(fn, src)
	case "field_expression":
		if f, ok := x.childOfKind(fn, "field_identifier"); ok {
			return x.text(f, src)
		}
	case "qualified_identifier":
		return lastSegDC(x.text(fn, src))
	case "template_function":
		if id, ok := x.childOfKind(fn, "identifier"); ok {
			return x.text(id, src)
		}
	}
	return ""
}
