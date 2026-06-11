// SPDX-License-Identifier: MIT

// Real-tree-sitter TypeScript / TSX extractor (ADR-0008 / #1958). One
// extractor type serves both grammars (the dispatcher constructs it with the
// typescript grammar for .ts and the tsx grammar for .tsx — node types are
// shared, TSX just adds JSX productions). Mirrors the Java/C# extractors with
// the "." module separator and moduleQN-keyed (namespace-blind) QNs so AST-
// tier symbol IDs match the regex tier for the common ES-module case.
// (Namespace/module-block-aware QNs are a deliberate later churn.)

package ast

import (
	"context"
	"strings"

	"github.com/kwad77/pincher/internal/tsbridge"
)

type tsTSExtractor struct {
	ts        tsbridge.TreeSitter
	lang      tsbridge.Language
	p         tsbridge.Parser
	ctx       context.Context
	allocated []tsbridge.Node
}

// newTSTSExtractor builds an extractor for the typescript grammar (tsx=false)
// or the tsx grammar (tsx=true).
func newTSTSExtractor(ctx context.Context, tsx bool) (*tsTSExtractor, error) {
	ts, err := tsbridge.New(ctx)
	if err != nil {
		return nil, err
	}
	var lang tsbridge.Language
	if tsx {
		lang, err = ts.LanguageTSX(ctx)
	} else {
		lang, err = ts.LanguageTypeScript(ctx)
	}
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
	return &tsTSExtractor{ts: ts, lang: lang, p: p, ctx: ctx}, nil
}

func (x *tsTSExtractor) kind(n tsbridge.Node) string { k, _ := n.Kind(x.ctx); return k }
func (x *tsTSExtractor) ncount(n tsbridge.Node) int {
	c, _ := n.NamedChildCount(x.ctx)
	return int(c)
}
func (x *tsTSExtractor) nchild(n tsbridge.Node, i int) tsbridge.Node {
	c, _ := n.NamedChild(x.ctx, uint64(i))
	x.allocated = append(x.allocated, c)
	return c
}
func (x *tsTSExtractor) text(n tsbridge.Node, src []byte) string {
	s, _ := n.StartByte(x.ctx)
	e, _ := n.EndByte(x.ctx)
	if int(s) <= int(e) && int(e) <= len(src) {
		return string(src[s:e])
	}
	return ""
}

func (x *tsTSExtractor) span(n tsbridge.Node, src []byte) (sb, eb, sl, el int) {
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

func (x *tsTSExtractor) addSym(fr *FileResult, name, kind, qn, parent string, n tsbridge.Node, src []byte) {
	sb, eb, sl, el := x.span(n, src)
	fr.Symbols = append(fr.Symbols, ExtractedSymbol{
		Name: name, Kind: kind, QualifiedName: qn, Parent: parent,
		StartByte: sb, EndByte: eb, StartLine: sl, EndLine: el,
		ExtractionConfidence: 1.0,
		IsExported:           true, // match the regex tier: TS exportedFn is always-true.
	})
}

// nameOfType returns the text of the first direct named child whose kind is
// one of the given types.
func (x *tsTSExtractor) nameOfType(n tsbridge.Node, src []byte, types ...string) string {
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

func (x *tsTSExtractor) extract(source []byte, relPath string) *FileResult {
	fr, _ := x.extractChecked(source, relPath)
	return fr
}

func (x *tsTSExtractor) extractChecked(source []byte, relPath string) (*FileResult, bool) {
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
	hadErr := x.walk(root, source, tsWalkCtx{scope: mod}, fr)
	if hadErr {
		return &FileResult{Module: mod}, false
	}
	fr.ConfidenceOverride = 1.0
	return fr, true
}

type tsWalkCtx struct {
	scope          string            // moduleQN of the file (constant; never extended — matches the regex tier's moduleQN base)
	container      string            // nearest class/namespace QN (moduleQN-based, single slot — a class overwrites a namespace, mirroring the regex currentClass); "" at file scope
	containerKind  string            // "class" | "namespace" | "" — controls Method-vs-Function and Parent semantics for direct members
	containerClass string            // bare class name of the nearest class container — the ReceiverType stamped on `this.X()` calls (#1177)
	enclosing      string            // innermost enclosing function/method QN — nested-function QN chain, local-var scope, and CALLS FromQN (#1422); "" at top level
	bindings       map[string]string // local variable/parameter name → declared type name, for receiver-type-aware CALLS (#1177)
}

// memberBase returns the QN prefix a function/method/class member attaches to:
// a class/namespace container first (moduleQN-based), then the innermost
// enclosing function (nested-function chain), then the module scope.
func (c tsWalkCtx) memberBase() string {
	if c.container != "" {
		return c.container
	}
	if c.enclosing != "" {
		return c.enclosing
	}
	return c.scope
}

// varBase returns the QN prefix a local/top-level variable attaches to: the
// innermost enclosing function if any, else the module scope (#1422/#261).
func (c tsWalkCtx) varBase() string {
	if c.enclosing != "" {
		return c.enclosing
	}
	return c.scope
}

func (x *tsTSExtractor) walk(n tsbridge.Node, src []byte, c tsWalkCtx, fr *FileResult) bool {
	hadErr := false
	if e, _ := n.IsError(x.ctx); e {
		hadErr = true
	}
	child := c
	switch x.kind(n) {
	case "internal_module", "module":
		// `namespace Foo { … }` / `module Foo { … }` (#1762): a Module symbol
		// whose direct function members parent to it. moduleQN-based (the
		// namespace does not nest the QN of a class inside it — matching the
		// regex tier's single currentClass slot).
		if name := x.nameOfType(n, src, "identifier"); name != "" {
			qn := joinDot(c.scope, name)
			x.addSym(fr, name, "Module", qn, "", n, src)
			child.container, child.containerKind, child.containerClass = qn, "namespace", ""
			child.enclosing = ""
		}
	case "class_declaration", "abstract_class_declaration":
		if name := x.nameOfType(n, src, "type_identifier"); name != "" {
			qn := joinDot(c.scope, name)
			x.addSym(fr, name, "Class", qn, x.heritage(n, src), n, src)
			child.container, child.containerKind, child.containerClass = qn, "class", name
			child.enclosing = ""
		}
	case "interface_declaration":
		if name := x.nameOfType(n, src, "type_identifier"); name != "" {
			qn := joinDot(c.scope, name)
			x.addSym(fr, name, "Interface", qn, "", n, src)
			child.container, child.containerKind, child.containerClass = qn, "class", name
			child.enclosing = ""
		}
	case "enum_declaration":
		if name := x.nameOfType(n, src, "identifier"); name != "" {
			x.addSym(fr, name, "Enum", joinDot(c.scope, name), "", n, src)
			child.enclosing = ""
		}
	case "function_declaration", "generator_function_declaration", "function_signature":
		if name := x.nameOfType(n, src, "identifier"); name != "" {
			child = x.declareCallable(n, src, c, name, "identifier-fn", fr)
		}
	case "method_definition", "method_signature", "abstract_method_signature":
		if name := x.nameOfType(n, src, "property_identifier"); name != "" {
			child = x.declareCallable(n, src, c, name, "method", fr)
		}
	case "public_field_definition":
		// A class field initialized to an arrow/function is a method-shaped
		// member (`foo = () => {}`). Plain data fields fall to the Variable path.
		if name := x.nameOfType(n, src, "property_identifier"); name != "" && x.hasFnValue(n) && c.containerKind == "class" {
			child = x.declareCallable(n, src, c, name, "method", fr)
		}
	case "lexical_declaration", "variable_declaration":
		x.handleVarDecl(n, src, c, fr)
	case "pair":
		// Object-literal method: `{ load: function(){}, render: () => {} }`.
		// The regex funcRE captures these via its `name: function` / `name: =>`
		// branches; emit a Function so they aren't dropped.
		if x.hasFnValue(n) {
			name := x.nameOfType(n, src, "property_identifier")
			if name == "" {
				name = x.nameOfType(n, src, "identifier")
			}
			if name != "" {
				qn := joinDot(c.varBase(), name)
				x.addSym(fr, name, "Function", qn, "", n, src)
				child.enclosing = qn
			}
		}
	case "import_statement":
		if t := x.importSource(n, src); t != "" {
			fr.Edges = append(fr.Edges, ExtractedEdge{FromQN: c.scope, ToName: t, Kind: "IMPORTS", Confidence: 1.0})
		}
	case "call_expression":
		x.handleCall(n, src, c, fr)
	}
	for i := 0; i < x.ncount(n); i++ {
		if x.walk(x.nchild(n, i), src, child, fr) {
			hadErr = true
		}
	}
	return hadErr
}

// declareCallable emits a Function/Method symbol and returns the child context
// for its body: enclosing := this QN, container cleared (a nested function is
// not a member of the enclosing class/namespace), and the binding table seeded
// with this callable's typed parameters + typed locals (#1177).
func (x *tsTSExtractor) declareCallable(n tsbridge.Node, src []byte, c tsWalkCtx, name, shape string, fr *FileResult) tsWalkCtx {
	var qn, parent, kind string
	switch {
	case c.containerKind == "class":
		kind, qn, parent = "Method", joinDot(c.container, name), c.container
	case c.containerKind == "namespace":
		kind, qn, parent = "Function", joinDot(c.container, name), c.container
	default:
		kind, qn = "Function", joinDot(c.memberBase(), name)
	}
	x.addSym(fr, name, kind, qn, parent, n, src)

	child := c
	child.enclosing = qn
	child.container, child.containerKind = "", ""
	if kind == "Method" {
		child.containerClass = c.containerClass // `this` inside a method refers to the enclosing class
	}
	child.bindings = x.collectBindings(n, src, c.bindings)
	return child
}

// handleVarDecl emits a symbol per declarator: a Function when the initializer
// is an arrow/function (regex funcRE arrow branch), else a Variable (#261/#1422
// TS parity). Both scope to the innermost enclosing function (or module scope);
// the Variable's Parent is the enclosing function so `trace`/`neighborhood` can
// bind a local to its owner.
func (x *tsTSExtractor) handleVarDecl(n tsbridge.Node, src []byte, c tsWalkCtx, fr *FileResult) {
	for i := 0; i < x.ncount(n); i++ {
		d := x.nchild(n, i)
		if x.kind(d) != "variable_declarator" {
			continue
		}
		name := x.nameOfType(d, src, "identifier")
		if name == "" {
			continue
		}
		qn := joinDot(c.varBase(), name)
		if x.declHasFnValue(d) {
			x.addSym(fr, name, "Function", qn, "", d, src)
		} else {
			x.addSym(fr, name, "Variable", qn, c.enclosing, d, src)
		}
	}
}

// handleCall emits a CALLS edge. For a member call `recv.method`, ToName is the
// dotted `recv.method` and ReceiverType is stamped from the binding table
// (`this` → the enclosing class name; a typed local/param → its declared type;
// otherwise empty) — the #1177 receiver-type-aware convention.
func (x *tsTSExtractor) handleCall(n tsbridge.Node, src []byte, c tsWalkCtx, fr *FileResult) {
	if x.ncount(n) == 0 {
		return
	}
	fn := x.nchild(n, 0)
	from := c.enclosing
	if from == "" {
		from = c.scope
	}
	switch x.kind(fn) {
	case "identifier":
		fr.Edges = append(fr.Edges, ExtractedEdge{FromQN: from, ToName: x.text(fn, src), Kind: "CALLS", Confidence: 0.6})
	case "member_expression":
		prop := x.nameOfType(fn, src, "property_identifier")
		if prop == "" {
			return
		}
		recv := ""
		if x.ncount(fn) > 0 {
			recv = x.text(x.nchild(fn, 0), src)
		}
		if recv == "" {
			return
		}
		var recvType string
		if recv == "this" {
			recvType = c.containerClass
		} else {
			recvType = c.bindings[recv]
		}
		fr.Edges = append(fr.Edges, ExtractedEdge{
			FromQN: from, ToName: recv + "." + prop, Kind: "CALLS",
			Confidence: 0.6, ReceiverType: recvType,
		})
	}
}

// collectBindings returns a name→type map for a callable: a copy of the parent
// bindings plus this callable's typed parameters and typed locals (`x: T`),
// scanned across the whole function body (the resolver only needs the binding
// in scope somewhere in the function, mirroring the regex per-function scan).
func (x *tsTSExtractor) collectBindings(fn tsbridge.Node, src []byte, parent map[string]string) map[string]string {
	out := make(map[string]string, len(parent)+4)
	for k, v := range parent {
		out[k] = v
	}
	var rec func(m tsbridge.Node)
	rec = func(m tsbridge.Node) {
		switch x.kind(m) {
		case "required_parameter", "optional_parameter", "variable_declarator":
			if name := x.nameOfType(m, src, "identifier"); name != "" {
				if t := x.typeOfAnnotation(m, src); t != "" {
					out[name] = t
				}
			}
		}
		for i := 0; i < x.ncount(m); i++ {
			rec(x.nchild(m, i))
		}
	}
	rec(fn)
	return out
}

// tsBottomTypes are TS top/bottom types that name no bindable class — a
// receiver typed as one of these must not stamp a ReceiverType, or the resolver
// chases a phantom class (#1177).
var tsBottomTypes = map[string]struct{}{
	"any": {}, "unknown": {}, "never": {}, "void": {},
	"null": {}, "undefined": {}, "object": {},
}

// typeOfAnnotation returns the bare base name of a node's `type_annotation`
// child (e.g. "Cart" for `c: Cart`), or "" — also "" for a bottom/top type.
func (x *tsTSExtractor) typeOfAnnotation(n tsbridge.Node, src []byte) string {
	for i := 0; i < x.ncount(n); i++ {
		c := x.nchild(n, i)
		if x.kind(c) == "type_annotation" {
			for k := 0; k < x.ncount(c); k++ {
				t := baseTypeName(x.text(x.nchild(c, k), src))
				if _, bottom := tsBottomTypes[t]; bottom {
					return ""
				}
				return t
			}
		}
	}
	return ""
}

func (x *tsTSExtractor) declHasFnValue(declarator tsbridge.Node) bool {
	for i := 0; i < x.ncount(declarator); i++ {
		switch x.kind(x.nchild(declarator, i)) {
		case "arrow_function", "function_expression", "function", "generator_function":
			return true
		}
	}
	return false
}

func (x *tsTSExtractor) hasFnValue(field tsbridge.Node) bool {
	for i := 0; i < x.ncount(field); i++ {
		switch x.kind(x.nchild(field, i)) {
		case "arrow_function", "function_expression", "function", "generator_function":
			return true
		}
	}
	return false
}

// heritage returns the bare name of the first type in a class's extends
// clause, or "" — the closest analogue to the regex `parent` capture.
func (x *tsTSExtractor) heritage(n tsbridge.Node, src []byte) string {
	for i := 0; i < x.ncount(n); i++ {
		c := x.nchild(n, i)
		if x.kind(c) == "class_heritage" {
			for k := 0; k < x.ncount(c); k++ {
				h := x.nchild(c, k)
				if x.kind(h) == "extends_clause" {
					for j := 0; j < x.ncount(h); j++ {
						return baseTypeName(x.text(x.nchild(h, j), src))
					}
				}
			}
		}
	}
	return ""
}

// importSource returns the module specifier of an import_statement, e.g.
// "zod" for `import { z } from "zod"` — the regex importRE's `path` capture.
func (x *tsTSExtractor) importSource(n tsbridge.Node, src []byte) string {
	for i := 0; i < x.ncount(n); i++ {
		c := x.nchild(n, i)
		if x.kind(c) == "string" {
			s := x.text(c, src)
			return strings.Trim(s, `'"`)
		}
	}
	return ""
}

