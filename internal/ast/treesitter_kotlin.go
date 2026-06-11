// SPDX-License-Identifier: MIT

// Real-tree-sitter Kotlin extractor (ADR-0008, Phase 2). Kotlin's community
// grammar (verified empirically) uses one `class_declaration` node for
// class / interface / data class / enum class — interface is told apart by the
// `interface` keyword in the declaration prefix, enum by an `enum_class_body`
// child; `object_declaration` and named `companion_object` are Classes too.
// `function_declaration` → Method (in a type) or Function (top level). modSep
// is "." and QNs are moduleQN-keyed (namespace-blind, matching the regex tier).
// `import_header` → net-new IMPORTS.

package ast

import (
	"context"
	"strings"

	"github.com/kwad77/pincher/internal/tsbridge"
)

type kotlinTSExtractor struct {
	ts        tsbridge.TreeSitter
	lang      tsbridge.Language
	p         tsbridge.Parser
	ctx       context.Context
	allocated []tsbridge.Node
}

func newKotlinTSExtractor(ctx context.Context) (*kotlinTSExtractor, error) {
	ts, err := tsbridge.New(ctx)
	if err != nil {
		return nil, err
	}
	lang, err := ts.LanguageKotlin(ctx)
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
	return &kotlinTSExtractor{ts: ts, lang: lang, p: p, ctx: ctx}, nil
}

func (x *kotlinTSExtractor) kind(n tsbridge.Node) string { k, _ := n.Kind(x.ctx); return k }
func (x *kotlinTSExtractor) ncount(n tsbridge.Node) int {
	c, _ := n.NamedChildCount(x.ctx)
	return int(c)
}
func (x *kotlinTSExtractor) nchild(n tsbridge.Node, i int) tsbridge.Node {
	c, _ := n.NamedChild(x.ctx, uint64(i))
	x.allocated = append(x.allocated, c)
	return c
}
func (x *kotlinTSExtractor) startByte(n tsbridge.Node) int { s, _ := n.StartByte(x.ctx); return int(s) }
func (x *kotlinTSExtractor) text(n tsbridge.Node, src []byte) string {
	s, _ := n.StartByte(x.ctx)
	e, _ := n.EndByte(x.ctx)
	if int(s) <= int(e) && int(e) <= len(src) {
		return string(src[s:e])
	}
	return ""
}

func (x *kotlinTSExtractor) span(n tsbridge.Node, src []byte) (sb, eb, sl, el int) {
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

func (x *kotlinTSExtractor) addSym(fr *FileResult, name, kind, qn, parent string, n tsbridge.Node, src []byte) {
	sb, eb, sl, el := x.span(n, src)
	fr.Symbols = append(fr.Symbols, ExtractedSymbol{
		Name: name, Kind: kind, QualifiedName: qn, Parent: parent,
		StartByte: sb, EndByte: eb, StartLine: sl, EndLine: el,
		ExtractionConfidence: 1.0,
		IsExported:           true,
	})
}

func (x *kotlinTSExtractor) childOfKind(n tsbridge.Node, kinds ...string) (tsbridge.Node, bool) {
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

func (x *kotlinTSExtractor) textOfKind(n tsbridge.Node, src []byte, kinds ...string) string {
	if c, ok := x.childOfKind(n, kinds...); ok {
		return x.text(c, src)
	}
	return ""
}

func (x *kotlinTSExtractor) extract(source []byte, relPath string) *FileResult {
	fr, _ := x.extractChecked(source, relPath)
	return fr
}

func (x *kotlinTSExtractor) extractChecked(source []byte, relPath string) (*FileResult, bool) {
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
	hadErr := x.walk(root, source, kotlinWalkCtx{scope: mod}, fr)
	if hadErr {
		return &FileResult{Module: mod}, false
	}
	fr.ConfidenceOverride = 1.0
	return fr, true
}

type kotlinWalkCtx struct {
	scope     string // moduleQN of the file (namespace-blind)
	container string // scope.Type — Parent + QN prefix for members; "" at file scope
	enclosing string // enclosing function QN — CALLS FromQN
}

func (x *kotlinTSExtractor) from(c kotlinWalkCtx) string {
	if c.enclosing != "" {
		return c.enclosing
	}
	return c.scope
}

func (x *kotlinTSExtractor) walk(n tsbridge.Node, src []byte, c kotlinWalkCtx, fr *FileResult) bool {
	hadErr := false
	if e, _ := n.IsError(x.ctx); e {
		hadErr = true
	}
	child := c
	switch x.kind(n) {
	case "class_declaration":
		if kind, name := x.classKind(n, src); name != "" {
			qn := joinDot(c.scope, name)
			x.addSym(fr, name, kind, qn, x.heritage(n, src), n, src)
			child.container = qn
			child.enclosing = ""
		}
	case "object_declaration":
		if name := x.textOfKind(n, src, "type_identifier"); name != "" {
			qn := joinDot(c.scope, name)
			x.addSym(fr, name, "Class", qn, x.heritage(n, src), n, src)
			child.container = qn
			child.enclosing = ""
		}
	case "companion_object":
		// A NAMED companion object is its own Class (regex classRE matches
		// `companion object Name`); an anonymous one emits no symbol and its
		// members stay scoped to the enclosing class.
		if name := x.textOfKind(n, src, "type_identifier"); name != "" {
			qn := joinDot(c.scope, name)
			x.addSym(fr, name, "Class", qn, "", n, src)
			child.container = qn
			child.enclosing = ""
		}
	case "function_declaration":
		if name := x.textOfKind(n, src, "simple_identifier"); name != "" {
			var qn, parent, kind string
			if c.container != "" {
				kind, qn, parent = "Method", joinDot(c.container, name), c.container
			} else {
				kind, qn = "Function", joinDot(c.scope, name)
			}
			x.addSym(fr, name, kind, qn, parent, n, src)
			child.enclosing = qn
			child.container = ""
		}
	case "import_header":
		if t := x.textOfKind(n, src, "identifier"); t != "" {
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

// classKind classifies a class_declaration as Interface (the `interface`
// keyword precedes the name), Enum (an `enum_class_body` child), or Class, and
// returns the bare type name.
func (x *kotlinTSExtractor) classKind(n tsbridge.Node, src []byte) (kind, name string) {
	ti, ok := x.childOfKind(n, "type_identifier")
	if !ok {
		return "", ""
	}
	name = x.text(ti, src)
	if _, isEnum := x.childOfKind(n, "enum_class_body"); isEnum {
		return "Enum", name
	}
	// Keyword(s) + modifiers precede the type_identifier; `interface` /
	// `fun interface` / `sealed interface` all carry the word "interface".
	prefix := ""
	if s, e := x.startByte(n), x.startByte(ti); s >= 0 && e <= len(src) && s <= e {
		prefix = string(src[s:e])
	}
	if strings.Contains(prefix, "interface") {
		return "Interface", name
	}
	return "Class", name
}

// heritage returns the bare name of the first supertype in a delegation
// specifier list — the regex `parent` analogue.
func (x *kotlinTSExtractor) heritage(n tsbridge.Node, src []byte) string {
	if spec, ok := x.childOfKind(n, "delegation_specifier"); ok {
		return baseTypeName(x.textOfKind(spec, src, "user_type", "type_identifier", "constructor_invocation"))
	}
	return ""
}

// calleeName returns the called name for a call_expression: a bare
// simple_identifier, or the trailing member of a navigation_expression.
func (x *kotlinTSExtractor) calleeName(n tsbridge.Node, src []byte) string {
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
