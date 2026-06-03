// SPDX-License-Identifier: MIT

package ast

import (
	"bytes"
	"regexp"
	"strings"
)

// #1177 v0.72: TS receiver-type stamping on CALLS edges. Pre-fix the
// TS extractor emitted ToName=<bare method name> for every call site,
// including `this.X()` and `varname.X()` shapes — the resolver then
// fell back to a name-only Method lookup that picks the wrong target
// whenever two classes both define a method named `X`. This file
// builds the typed-receiver hints the resolver needs to bind
// `cart.add()` to `Cart.add` precisely.
//
// Scope of this piece (regex-tier — see #1182/#1183 for AST-tier
// pathways):
//   - Typed-variable tracking: `const|let|var name: TypeName = ...`
//     and `name: TypeName` in parameter lists.
//   - Three call-site shapes stamped:
//       this.X()          → ToName="this.X"      ReceiverType=<enclosing class>
//       varname.X()       → ToName="varname.X"   ReceiverType=varTypes[varname]
//       bareName()        → ToName="bareName"    ReceiverType="" (free call)
//   - Out of scope here (future): class-field type tracking
//     (`private cart: Cart` → `this.cart.X()`), multi-segment chains
//     (`a.b.X()`), and TS type inference via `new` expressions.

// tsCallChainRE captures call sites of the form `[chain.]name(`.
// The chain group captures zero or more `name.` segments preceding the
// callee; chain is "" for bare calls, "this." for this-methods, and
// "name." for single-receiver dotted calls. Multi-segment chains
// ("a.b.") are captured but stay unhandled at the resolver — see the
// receiver-type-aware emit logic below.
var tsCallChainRE = regexp.MustCompile(
	`(?P<chain>(?:[A-Za-z_$][A-Za-z0-9_$]*\.)*)(?P<name>[A-Za-z_$][A-Za-z0-9_$]*)\s*\(`)

// tsTypedParamRE matches `name: TypeName` inside a parameter list.
// TypeName is the leading identifier of the annotation (`Cart` from
// `Cart<string>`, `Cart | null`, `readonly Cart`) — sufficient for
// the resolver's class-name lookup. Generics, unions, and modifiers
// past the first identifier are discarded by design; the resolver only
// needs the head type to find the Class symbol.
var tsTypedParamRE = regexp.MustCompile(
	`(?P<name>[A-Za-z_$][A-Za-z0-9_$]*)\s*:\s*(?P<type>[A-Za-z_$][A-Za-z0-9_$]*)`)

// tsTypedLocalRE matches `(const|let|var) name: TypeName` at any
// indentation inside a function body. Mirrors tsTypedParamRE's
// type-head-only capture.
var tsTypedLocalRE = regexp.MustCompile(
	`(?m)(?:const|let|var)\s+(?P<name>[A-Za-z_$][A-Za-z0-9_$]*)\s*:\s*(?P<type>[A-Za-z_$][A-Za-z0-9_$]*)`)

// tsCallScanReceiverAware is the receiver-type-aware replacement for
// regexCallScan used by TS/TSX. body is the symbol's full source
// (signature + braces + body); enclosingClass is the bare class name
// when the body is a method (e.g. "Service"), empty otherwise.
//
// Confidence stays at 0.6 (regex-tier) for parity with regexCallScan
// — over-emission is bounded by the resolver dropping unresolved
// targets.
func tsCallScanReceiverAware(body []byte, fromQN, enclosingClass string) []ExtractedEdge {
	// Skip past the signature line so the function's own name doesn't
	// self-match. Same `{` anchor as regexCallScan; falls back to
	// "first newline" for arrow-function-without-braces shapes (which
	// won't have call sites anyway, so the empty fallback is harmless).
	open := bytes.IndexByte(body, '{')
	if open < 0 {
		nl := bytes.IndexByte(body, '\n')
		if nl < 0 {
			return nil
		}
		open = nl
	}

	// Phase 1: build the varName → TypeName map from typed params
	// (signature) and typed locals (body). Param captures from the
	// signature are kept separate from local captures only for
	// readability — both feed the same map.
	varTypes := map[string]string{}
	sig := body[:open]
	bodyOnly := body[open:]
	tsCollectTypedParams(sig, varTypes)
	tsCollectTypedLocals(bodyOnly, varTypes)

	// Phase 2: emit CALLS edges with receiver-type stamping. Per-edge
	// dedup is keyed on (toName, receiverType) so repeated identical
	// calls collapse but distinct shapes survive (e.g. `this.X()` and
	// `cart.X()` in the same body emit two edges).
	var edges []ExtractedEdge
	seen := map[string]bool{}
	for _, m := range tsCallChainRE.FindAllSubmatch(bodyOnly, -1) {
		chain := strings.TrimSuffix(string(m[1]), ".")
		name := string(m[2])

		if chain == "" {
			// Bare call: same shape as regexCallScan, plus the same
			// control-flow keyword filter. ReceiverType stays empty —
			// in TS a bare `helper()` inside a method is a module-
			// level function, NOT an implicit `this.helper()`.
			if regexCallKeywords[name] {
				continue
			}
			key := name + "::"
			if seen[key] {
				continue
			}
			seen[key] = true
			edges = append(edges, ExtractedEdge{
				FromQN:     fromQN,
				ToName:     name,
				Kind:       "CALLS",
				Confidence: 0.6,
			})
			continue
		}

		// Dotted call. Single-segment receivers get receiver-type
		// stamping; multi-segment chains (`a.b.X()`) emit the edge
		// with empty ReceiverType so the existing #285 receiver-
		// method fallback can still try to bind the trailing method
		// name (best-effort behaviour parity with pre-#1177).
		recvType := ""
		segments := strings.Split(chain, ".")
		if len(segments) == 1 {
			recv := segments[0]
			switch recv {
			case "this":
				recvType = enclosingClass
			default:
				recvType = varTypes[recv]
			}
		}

		toName := chain + "." + name
		key := toName + "::" + recvType
		if seen[key] {
			continue
		}
		seen[key] = true
		edges = append(edges, ExtractedEdge{
			FromQN:       fromQN,
			ToName:       toName,
			Kind:         "CALLS",
			Confidence:   0.6,
			ReceiverType: recvType,
		})
	}
	return edges
}

// tsCollectTypedParams walks the function-signature bytes (everything
// before the body's opening `{`) and records each `name: TypeName`
// binding. Skips the function-name-followed-by-colon shape that TS
// uses for object-method shorthand by requiring the colon be followed
// by something that looks like a type identifier (start with letter
// or `_`/`$`).
func tsCollectTypedParams(sig []byte, out map[string]string) {
	for _, m := range tsTypedParamRE.FindAllSubmatch(sig, -1) {
		name := string(m[1])
		typ := string(m[2])
		// Drop `void`, `undefined`, `null`, `any`, `unknown`, `never`
		// — these are TS bottom/top types, not classes the resolver
		// can bind against. Keeping them in the map would just
		// produce dead receiver-type stamps.
		if tsIsBottomOrTopType(typ) {
			continue
		}
		out[name] = typ
	}
}

// tsCollectTypedLocals scans the body for `const|let|var name: T`
// declarations. Multi-line union/intersection annotations are not
// supported by the head-only regex — those bindings stay un-typed,
// which is preferable to a malformed type capture.
func tsCollectTypedLocals(body []byte, out map[string]string) {
	for _, m := range tsTypedLocalRE.FindAllSubmatch(body, -1) {
		name := string(m[1])
		typ := string(m[2])
		if tsIsBottomOrTopType(typ) {
			continue
		}
		out[name] = typ
	}
}

func tsIsBottomOrTopType(t string) bool {
	switch t {
	case "void", "undefined", "null", "any", "unknown", "never":
		return true
	}
	return false
}
