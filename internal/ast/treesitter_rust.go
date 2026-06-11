// SPDX-License-Identifier: MIT

// Experimental real-tree-sitter Rust extractor (ADR-0008 / #1957). NOT wired
// into the registry/dispatcher yet — this is the Phase-1 extractor under
// construction, exercised by the gated differential test against the regex
// tier. Maps tree-sitter nodes onto pincher's ExtractedSymbol/Edge shape.

package ast

import (
	"context"
	"strings"

	"github.com/kwad77/pincher/internal/tsbridge"
)

type rustTSExtractor struct {
	ts   tsbridge.TreeSitter
	lang tsbridge.Language
	p    tsbridge.Parser
	ctx  context.Context
	// allocated tracks every node-struct WASM allocation made during the
	// current extraction so they can be bulk-freed at the end. Reused
	// (reset, not reallocated) across extractions to keep Go allocation
	// flat. The instance is single-threaded (pool checkout), so no lock.
	allocated []tsbridge.Node
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
	r.allocated = append(r.allocated, c)
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

// span returns the byte and 1-indexed line range of a node, clamped to src.
func (r *rustTSExtractor) span(n tsbridge.Node, src []byte) (sb, eb, sl, el int) {
	s, _ := n.StartByte(r.ctx)
	e, _ := n.EndByte(r.ctx)
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

// addSym appends a symbol with its node's byte/line span and AST-tier
// confidence.
func (r *rustTSExtractor) addSym(fr *FileResult, name, kind, qn, parent string, n tsbridge.Node, src []byte) {
	sb, eb, sl, el := r.span(n, src)
	fr.Symbols = append(fr.Symbols, ExtractedSymbol{
		Name: name, Kind: kind, QualifiedName: qn, Parent: parent,
		StartByte: sb, EndByte: eb, StartLine: sl, EndLine: el,
		ExtractionConfidence: 1.0,
	})
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
	fr, _ := r.extractChecked(source, relPath)
	return fr
}

// extractChecked runs the tree-sitter pass and reports whether the parse was
// clean (no ERROR node anywhere in the tree). The dispatcher uses the bool
// for all-or-nothing semantics: a clean parse yields AST-tier symbols
// (ConfidenceOverride 1.0); any parse error returns ok=false so the caller
// falls back to the regex tier rather than emitting a partial/degraded tree.
func (r *rustTSExtractor) extractChecked(source []byte, relPath string) (*FileResult, bool) {
	// Match the regex tier's module convention exactly (moduleQN: strip
	// extension, path separators → "::") so AST-tier QNs/IDs stay identical
	// to the regex tier for the common cases — minimizing the symbol-ID churn
	// on the upgrade re-index. (Rust `mod`-block-aware qualification is a
	// separate, deliberately-churning enhancement for a later cycle.)
	mod := moduleQN(relPath, "::")
	tree, err := r.p.ParseString(r.ctx, string(source))
	if err != nil {
		return &FileResult{Module: mod}, false
	}
	// Free everything allocated for this parse: the tree (ts_tree_delete) and
	// every node-struct scratch allocation. Without this, reusing a pooled
	// instance across many files grows the WASM heap unbounded (#1957).
	r.allocated = r.allocated[:0]
	defer func() {
		for i := range r.allocated {
			_ = r.allocated[i].Free(r.ctx)
		}
		r.allocated = r.allocated[:0]
		_ = tree.Close(r.ctx)
	}()
	root, err := tree.RootNode(r.ctx)
	if err != nil {
		return &FileResult{Module: mod}, false
	}
	r.allocated = append(r.allocated, root)
	fr := &FileResult{Module: mod}
	hadErr := r.walk(root, source, rustWalkCtx{scope: mod}, fr)
	if hadErr {
		// Discard the partial tree-sitter result; the dispatcher falls back
		// to the regex tier for an all-or-nothing-per-file guarantee.
		return &FileResult{Module: mod}, false
	}
	fr.ConfidenceOverride = 1.0
	return fr, true
}

// walk descends the tree emitting symbols/edges and returns whether any node
// in the subtree is an ERROR node. scope is the qualified module path of the
// current context (file directory + nested `mod` blocks); typeParent is the
// fully-qualified enclosing type for methods (e.g. "geo::Point"), or "" when
// not inside an impl/trait — matching the regex tier's QN/Parent convention.
// rustWalkCtx carries the scope state threaded through the walk, matching the
// regex tier's QN/Parent/CALLS conventions so AST-tier symbol IDs and edges
// stay stable.
type rustWalkCtx struct {
	scope      string // module path (moduleQN of the file)
	typeParent string // scope::Type — the Parent field for methods (no trait)
	qnPrefix   string // scope::Type[::Trait] — QN prefix for methods (#1783)
	caller     string // enclosing function/method QN — FromQN for CALLS edges
}

func (r *rustTSExtractor) walk(n tsbridge.Node, src []byte, c rustWalkCtx, fr *FileResult) bool {
	hadErr := false
	if e, _ := n.IsError(r.ctx); e {
		hadErr = true
	}
	child := c
	switch r.kind(n) {
	case "function_item":
		if name := r.nameOfType(n, src, "identifier"); name != "" {
			var qn string
			if c.typeParent != "" {
				qn = joinQN(c.qnPrefix, name)
				r.addSym(fr, name, "Method", qn, c.typeParent, n, src)
			} else {
				qn = joinQN(c.scope, name)
				r.addSym(fr, name, "Function", qn, "", n, src)
			}
			// Calls in this body attribute to this function; nested items are
			// not methods of the enclosing type.
			child.caller = qn
			child.typeParent, child.qnPrefix = "", ""
		}
	case "struct_item":
		if name := r.nameOfType(n, src, "type_identifier"); name != "" {
			r.addSym(fr, name, "Class", joinQN(c.scope, name), "", n, src)
		}
	case "enum_item":
		if name := r.nameOfType(n, src, "type_identifier"); name != "" {
			r.addSym(fr, name, "Enum", joinQN(c.scope, name), "", n, src)
		}
	case "trait_item":
		if name := r.nameOfType(n, src, "type_identifier"); name != "" {
			r.addSym(fr, name, "Interface", joinQN(c.scope, name), "", n, src)
			child.typeParent = joinQN(c.scope, name)
			child.qnPrefix = child.typeParent
		}
	case "impl_item":
		// impl [Trait for] Type — one type name (inherent) or two (trait first,
		// receiver after `for`). Parent = scope::Type; method QN prefix =
		// scope::Type[::Trait] so Debug/Display fmt methods don't collide (#1783).
		var names []string
		for i := 0; i < r.ncount(n); i++ {
			cc := r.nchild(n, i)
			if r.kind(cc) == "declaration_list" {
				break
			}
			if k := r.kind(cc); k == "type_identifier" || k == "generic_type" || k == "scoped_type_identifier" {
				names = append(names, baseTypeName(r.text(cc, src)))
			}
		}
		switch {
		case len(names) >= 2:
			child.typeParent = joinQN(c.scope, names[len(names)-1])
			child.qnPrefix = joinQN(child.typeParent, names[0])
		case len(names) == 1:
			child.typeParent = joinQN(c.scope, names[0])
			child.qnPrefix = child.typeParent
		}
	case "use_declaration":
		for _, t := range r.useTargets(n, src) {
			fr.Edges = append(fr.Edges, ExtractedEdge{FromQN: c.scope, ToName: t, Kind: "IMPORTS", Confidence: 1.0})
		}
	case "call_expression":
		if callee := r.calleeName(n, src); callee != "" {
			from := c.caller
			if from == "" {
				from = c.scope
			}
			// Per-file CALLS candidates are regex-tier confidence (0.6) — the
			// resolver upgrades them; the AST gain is in the symbols (1.0).
			fr.Edges = append(fr.Edges, ExtractedEdge{FromQN: from, ToName: callee, Kind: "CALLS", Confidence: 0.6})
		}
	}
	for i := 0; i < r.ncount(n); i++ {
		if r.walk(r.nchild(n, i), src, child, fr) {
			hadErr = true
		}
	}
	return hadErr
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

// baseTypeName strips generic args and the path prefix from a type expression
// so it matches the regex tier's bare-identifier capture: "Vec<T>" → "Vec",
// "fmt::Debug" → "Debug".
func baseTypeName(s string) string {
	if i := strings.IndexByte(s, '<'); i >= 0 {
		s = s[:i]
	}
	return lastSeg(strings.TrimSpace(s))
}

func lastSeg(s string) string {
	for i := len(s) - 1; i >= 1; i-- {
		if s[i] == ':' && s[i-1] == ':' {
			return s[i+1:]
		}
	}
	return s
}
