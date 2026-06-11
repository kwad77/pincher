// SPDX-License-Identifier: MIT

// Real-tree-sitter Java extractor (ADR-0008 / #1958). Mirrors the Rust
// extractor (treesitter_rust.go) but for Java node types and the "."
// module separator. Maps tree-sitter nodes onto pincher's
// ExtractedSymbol/Edge shape, matching the regex tier's QN/Parent/CALLS
// conventions so the AST-tier upgrade keeps symbol IDs stable.

package ast

import (
	"context"
	"strings"

	"github.com/kwad77/pincher/internal/tsbridge"
)

type javaTSExtractor struct {
	ts   tsbridge.TreeSitter
	lang tsbridge.Language
	p    tsbridge.Parser
	ctx  context.Context
	// allocated tracks every node-struct WASM allocation so they can be
	// bulk-freed at the end of each extraction (see treesitter_rust.go).
	allocated []tsbridge.Node
}

func newJavaTSExtractor(ctx context.Context) (*javaTSExtractor, error) {
	ts, err := tsbridge.New(ctx)
	if err != nil {
		return nil, err
	}
	lang, err := ts.LanguageJava(ctx)
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
	return &javaTSExtractor{ts: ts, lang: lang, p: p, ctx: ctx}, nil
}

func (j *javaTSExtractor) kind(n tsbridge.Node) string { k, _ := n.Kind(j.ctx); return k }
func (j *javaTSExtractor) ncount(n tsbridge.Node) int {
	c, _ := n.NamedChildCount(j.ctx)
	return int(c)
}
func (j *javaTSExtractor) nchild(n tsbridge.Node, i int) tsbridge.Node {
	c, _ := n.NamedChild(j.ctx, uint64(i))
	j.allocated = append(j.allocated, c)
	return c
}
func (j *javaTSExtractor) text(n tsbridge.Node, src []byte) string {
	s, _ := n.StartByte(j.ctx)
	e, _ := n.EndByte(j.ctx)
	if int(s) <= int(e) && int(e) <= len(src) {
		return string(src[s:e])
	}
	return ""
}

func (j *javaTSExtractor) span(n tsbridge.Node, src []byte) (sb, eb, sl, el int) {
	s, _ := n.StartByte(j.ctx)
	e, _ := n.EndByte(j.ctx)
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

func (j *javaTSExtractor) addSym(fr *FileResult, name, kind, qn, parent string, n tsbridge.Node, src []byte) {
	sb, eb, sl, el := j.span(n, src)
	fr.Symbols = append(fr.Symbols, ExtractedSymbol{
		Name: name, Kind: kind, QualifiedName: qn, Parent: parent,
		StartByte: sb, EndByte: eb, StartLine: sl, EndLine: el,
		ExtractionConfidence: 1.0,
		IsExported:           true, // #820: Java visibility is keyword-based; the regex tier treats every symbol as exported, so match it.
	})
}

// nameOfType returns the text of the first named child whose kind is one
// of the given types — how we read a declaration's identifier without a
// field-name accessor on the binding.
func (j *javaTSExtractor) nameOfType(n tsbridge.Node, src []byte, types ...string) string {
	for i := 0; i < j.ncount(n); i++ {
		c := j.nchild(n, i)
		k := j.kind(c)
		for _, t := range types {
			if k == t {
				return j.text(c, src)
			}
		}
	}
	return ""
}

// joinDot mirrors joinQN but for Java's "." module separator.
func joinDot(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return a + "." + b
}

func (j *javaTSExtractor) extract(source []byte, relPath string) *FileResult {
	fr, _ := j.extractChecked(source, relPath)
	return fr
}

// extractChecked runs the tree-sitter pass and reports whether the parse
// was clean (no ERROR node). The dispatcher uses the bool for
// all-or-nothing semantics: a clean parse yields AST-tier symbols
// (ConfidenceOverride 1.0); any parse error returns ok=false so the caller
// falls back to the regex tier rather than emitting a partial tree.
func (j *javaTSExtractor) extractChecked(source []byte, relPath string) (*FileResult, bool) {
	// Match the regex tier's module convention exactly (moduleQN: strip
	// extension, path separators → ".") so AST-tier QNs/IDs stay identical
	// to the regex tier and the upgrade re-index doesn't churn symbol IDs.
	mod := moduleQN(relPath, ".")
	tree, err := j.p.ParseString(j.ctx, string(source))
	if err != nil {
		return &FileResult{Module: mod}, false
	}
	j.allocated = j.allocated[:0]
	defer func() {
		for i := range j.allocated {
			_ = j.allocated[i].Free(j.ctx)
		}
		j.allocated = j.allocated[:0]
		_ = tree.Close(j.ctx)
	}()
	root, err := tree.RootNode(j.ctx)
	if err != nil {
		return &FileResult{Module: mod}, false
	}
	j.allocated = append(j.allocated, root)
	fr := &FileResult{Module: mod}
	hadErr := j.walk(root, source, javaWalkCtx{scope: mod}, fr)
	if hadErr {
		return &FileResult{Module: mod}, false
	}
	fr.ConfidenceOverride = 1.0
	return fr, true
}

// javaWalkCtx threads the scope state through the walk, matching the regex
// tier's QN/Parent/CALLS conventions so AST-tier symbol IDs and edges stay
// stable.
type javaWalkCtx struct {
	scope      string // module path (moduleQN of the file)
	typeScope  string // scope.Type — Parent + QN prefix for members
	caller     string // enclosing method/function QN — FromQN for CALLS edges
	insideType bool   // true once inside a class/interface/enum/record body
}

func (j *javaTSExtractor) walk(n tsbridge.Node, src []byte, c javaWalkCtx, fr *FileResult) bool {
	hadErr := false
	if e, _ := n.IsError(j.ctx); e {
		hadErr = true
	}
	child := c
	switch j.kind(n) {
	case "class_declaration", "record_declaration":
		if name := j.nameOfType(n, src, "identifier"); name != "" {
			qn := joinDot(c.scope, name)
			parent := j.superclassName(n, src)
			j.addSym(fr, name, "Class", qn, parent, n, src)
			child.typeScope = qn
			child.insideType = true
		}
	case "interface_declaration":
		if name := j.nameOfType(n, src, "identifier"); name != "" {
			qn := joinDot(c.scope, name)
			j.addSym(fr, name, "Interface", qn, "", n, src)
			child.typeScope = qn
			child.insideType = true
		}
	case "enum_declaration":
		if name := j.nameOfType(n, src, "identifier"); name != "" {
			qn := joinDot(c.scope, name)
			j.addSym(fr, name, "Enum", qn, "", n, src)
			child.typeScope = qn
			child.insideType = true
		}
	case "method_declaration", "constructor_declaration":
		if name := j.nameOfType(n, src, "identifier"); name != "" {
			var qn, parent, kind string
			if c.typeScope != "" {
				kind = "Method"
				qn = joinDot(c.typeScope, name)
				parent = c.typeScope
			} else {
				kind = "Function"
				qn = joinDot(c.scope, name)
			}
			j.addSym(fr, name, kind, qn, parent, n, src)
			child.caller = qn
			// Nested types declared inside a method body are scoped to the
			// file/module, not as methods of the enclosing type.
			child.typeScope, child.insideType = "", false
		}
	case "import_declaration":
		if t := j.importTarget(n, src); t != "" {
			fr.Edges = append(fr.Edges, ExtractedEdge{FromQN: c.scope, ToName: t, Kind: "IMPORTS", Confidence: 1.0})
		}
	case "method_invocation":
		if callee := j.calleeName(n, src); callee != "" {
			from := c.caller
			if from == "" {
				from = c.scope
			}
			// Per-file CALLS candidates are regex-tier confidence (0.6) — the
			// resolver upgrades them; the AST gain is in the symbols (1.0).
			fr.Edges = append(fr.Edges, ExtractedEdge{FromQN: from, ToName: callee, Kind: "CALLS", Confidence: 0.6})
		}
	}
	for i := 0; i < j.ncount(n); i++ {
		if j.walk(j.nchild(n, i), src, child, fr) {
			hadErr = true
		}
	}
	return hadErr
}

// superclassName returns the bare name of a class's `extends` target, or ""
// — matching the regex classRE `parent` capture (single inheritance only;
// `implements` interfaces are not parents in the regex tier).
func (j *javaTSExtractor) superclassName(n tsbridge.Node, src []byte) string {
	for i := 0; i < j.ncount(n); i++ {
		c := j.nchild(n, i)
		if j.kind(c) == "superclass" {
			// superclass wraps the type expression; take its base name.
			for k := 0; k < j.ncount(c); k++ {
				return baseTypeName(j.text(j.nchild(c, k), src))
			}
		}
	}
	return ""
}

// importTarget returns the dotted path of an import_declaration, matching
// the regex importRE which captures the full `[a-zA-Z0-9_.]+` path.
func (j *javaTSExtractor) importTarget(n tsbridge.Node, src []byte) string {
	for i := 0; i < j.ncount(n); i++ {
		c := j.nchild(n, i)
		switch j.kind(c) {
		case "scoped_identifier", "identifier":
			return j.text(c, src)
		}
	}
	return ""
}

// calleeName returns the invoked method's name for a method_invocation:
// the `identifier` immediately preceding the argument_list (skipping any
// receiver object/field expression).
func (j *javaTSExtractor) calleeName(n tsbridge.Node, src []byte) string {
	argIdx := -1
	for i := 0; i < j.ncount(n); i++ {
		if j.kind(j.nchild(n, i)) == "argument_list" {
			argIdx = i
			break
		}
	}
	limit := argIdx
	if limit < 0 {
		limit = j.ncount(n)
	}
	name := ""
	for i := 0; i < limit; i++ {
		c := j.nchild(n, i)
		if j.kind(c) == "identifier" {
			name = j.text(c, src)
		}
	}
	return name
}
