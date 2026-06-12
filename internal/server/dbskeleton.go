// SPDX-License-Identifier: MIT

package server

import (
	"fmt"
	"strings"

	"github.com/kwad77/pincher/internal/db"
)

// mode=skeleton — render a symbol from ALREADY-INDEXED metadata, no file read.
//
// Headroom's AST code-compressor renders a function as `signature + body
// elided`; this is pincher's in-lane analog, but it ships the property
// headroom can't: it reads ONLY the DB. The byte-offset source reader
// (ReadSymbolSource) and its staleness-validation path are sidestepped
// entirely — a skeleton is reconstructed from the Symbol row's Signature,
// ReturnType, Docstring, Kind, and (for containers) its child symbols'
// signatures discovered via the Parent linkage. So a skeleton is immune to
// the staleness path: it still renders correctly after the source file is
// edited or deleted, because no file is ever opened.
//
// This is distinct from the existing detail="skeleton" (skeleton.go), which
// is a line-classifier over already-READ source bytes — that mode still
// reads the file. mode=skeleton renders structure from indexed fields with
// zero disk I/O. When both are requested, mode=skeleton wins (there is no
// source to classify because none is read).
//
// Rendering, per symbol Kind:
//
//   - Callable (Function/Method): the signature line, the docstring as a
//     leading comment when present, and an elided body in language-
//     appropriate form ({ ... } for brace languages; `...`/`pass` for
//     Python and other indent languages).
//   - Container (Class/Interface/Enum/Module/Type, or any kind that has
//     child rows): the container's declaration line followed by each
//     child's signature (one line, body elided). This makes a class
//     skeleton equal to its method/field signatures — no bodies.
//
// max_tokens: skeletons are small, but a huge container could still exceed.
// The child list is truncated at a line boundary with a `… +N more` note —
// never mid-line.

// modeSkeletonValue is the only non-default mode value today.
const modeSkeletonValue = "skeleton"

// parseModeArg reads the `mode` arg shared by symbol/symbols/context.
// Returns skeleton=true for "skeleton", false for ""/"full". Unknown values
// degrade to full with a warning — same soft-contract family as
// parseDetailArg (#908 fields-projection unknown-key handling).
func parseModeArg(args map[string]any) (skeleton bool, warning string) {
	switch v := str(args, "mode"); v {
	case "", "full":
		return false, ""
	case modeSkeletonValue:
		return true, ""
	default:
		return false, fmt.Sprintf(
			"unknown mode %q — valid values: \"full\" (default), \"skeleton\"; returning full source", v)
	}
}

// skeletonContainerKinds are the kinds rendered as a member-signature list
// rather than an elided callable body. Any symbol with child rows is also
// treated as a container regardless of this set (the set drives the
// declaration-line shape and the "no children" fallback).
var skeletonContainerKinds = map[string]bool{
	"Class": true, "Interface": true, "Enum": true, "Module": true, "Type": true,
}

// commentPrefixForLanguage returns the single-line comment marker for the
// language, used to render a docstring as a leading comment. Defaults to
// "//" (the brace-language family), which is the safe majority.
func commentPrefixForLanguage(lang string) string {
	switch strings.ToLower(lang) {
	case "python", "ruby", "shell", "bash", "yaml", "toml", "r", "perl":
		return "#"
	case "lua", "sql", "haskell", "elm":
		return "--"
	default:
		// Go, Rust, JavaScript, TypeScript, Java, C, C++, C#, Kotlin,
		// Swift, PHP, Scala, ... all use //.
		return "//"
	}
}

// bodyPlaceholderForLanguage returns the elided-body rendering for a
// callable in the given language. Brace languages get `{ ... }`; indent
// languages (Python and friends) get an indented `...`.
func bodyPlaceholderForLanguage(lang string) string {
	switch strings.ToLower(lang) {
	case "python":
		return "    ..."
	case "ruby":
		return "  # ...\nend"
	default:
		return "{ ... }"
	}
}

// renderDocComment renders a (possibly multi-line) docstring as a leading
// comment block using the language's line-comment prefix. Returns "" for an
// empty docstring so the caller can skip a blank line.
func renderDocComment(doc, lang string) string {
	doc = strings.TrimSpace(doc)
	if doc == "" {
		return ""
	}
	prefix := commentPrefixForLanguage(lang)
	lines := strings.Split(doc, "\n")
	out := make([]string, len(lines))
	for i, ln := range lines {
		ln = strings.TrimRight(ln, " \t")
		if ln == "" {
			out[i] = prefix
		} else {
			out[i] = prefix + " " + ln
		}
	}
	return strings.Join(out, "\n")
}

// callableSignatureLine reconstructs the symbol's signature line for the
// skeleton. Prefers the indexed Signature verbatim; when Signature lacks the
// return type but ReturnType is indexed separately, appends it (Go shape:
// `func Name(params) ReturnType`). Falls back to a name-only stub when no
// signature was extracted (regex-tier symbols).
func callableSignatureLine(sym *db.Symbol) string {
	sig := strings.TrimSpace(sym.Signature)
	ret := strings.TrimSpace(sym.ReturnType)
	if sig == "" {
		// No extracted signature — reconstruct the minimal shape.
		if ret != "" {
			return fmt.Sprintf("%s(...) %s", sym.Name, ret)
		}
		return sym.Name + "(...)"
	}
	// Append the return type only when the signature doesn't already carry
	// it (avoid `func F() T T`). Heuristic: if the trimmed return type isn't
	// already a substring of the signature tail, append it.
	if ret != "" && !strings.Contains(sig, ret) {
		return sig + " " + ret
	}
	return sig
}

// renderDBSkeleton builds the skeleton source string for sym from indexed
// fields only. children is the symbol's Parent-linked child rows (nil for a
// callable, or when the caller chose not to fetch them). maxTokens caps the
// child list with a `… +N more` note; 0 = unlimited. It returns the rendered
// string and the number of child rows actually shown (for _meta accounting).
//
// No file is read. No byte-offset seek. The render is a pure function of the
// already-indexed Symbol fields — that is the staleness-immunity guarantee.
func renderDBSkeleton(sym *db.Symbol, children []db.Symbol, maxTokens int) (rendered string, childrenShown int) {
	lang := sym.Language
	var b strings.Builder

	if doc := renderDocComment(sym.Docstring, lang); doc != "" {
		b.WriteString(doc)
		b.WriteByte('\n')
	}

	isContainer := skeletonContainerKinds[sym.Kind] || len(children) > 0
	if !isContainer {
		// Callable: signature + elided body.
		b.WriteString(callableSignatureLine(sym))
		b.WriteByte('\n')
		b.WriteString(bodyPlaceholderForLanguage(lang))
		return b.String(), 0
	}

	// Container: declaration line + each child's signature, body elided.
	decl := strings.TrimSpace(sym.Signature)
	if decl == "" {
		// No extracted container signature — synthesize a kind-led header.
		kindWord := strings.ToLower(sym.Kind)
		decl = fmt.Sprintf("%s %s", kindWord, sym.Name)
	}
	b.WriteString(decl)
	b.WriteString(" {\n")

	// Budget the child list. Reserve the already-built header against the
	// budget so a tight cap still truncates honestly.
	used := 0
	if maxTokens > 0 {
		used = db.ApproxTokens(b.String())
	}
	shown := 0
	for i := range children {
		c := &children[i]
		line := "    " + childSignatureLine(c) + "\n"
		if maxTokens > 0 {
			lineTok := db.ApproxTokens(line)
			// Always leave room for the closing brace + a possible
			// "+N more" note; cut before exceeding the budget.
			if used+lineTok > maxTokens && shown > 0 {
				remaining := len(children) - shown
				b.WriteString(fmt.Sprintf("    … +%d more\n", remaining))
				break
			}
			used += lineTok
		}
		b.WriteString(line)
		shown++
	}
	b.WriteString("}")
	return b.String(), shown
}

// attachModeSkeletonMeta stamps `_meta.mode: "skeleton"` and, when the
// savings are cheap to compute, `_meta.skeleton_children_shown` /
// `_meta.skeleton_children_truncated`. fullBytes is the symbol's indexed
// span (EndByte-StartByte) — the file-read baseline a full serve would have
// shipped — and skeletonStr is what mode=skeleton shipped instead, so the
// token saving is an honest before/after on the same symbol.
func attachModeSkeletonMeta(data map[string]any, fullBytes int, skeletonStr string) {
	meta, _ := data["_meta"].(map[string]any)
	if meta == nil {
		meta = map[string]any{}
		data["_meta"] = meta
	}
	meta["mode"] = modeSkeletonValue
	if fullBytes > 0 {
		fullTok := fullBytes / charsPerToken
		skelTok := db.ApproxTokens(skeletonStr)
		meta["skeleton_tokens"] = skelTok
		meta["full_tokens_est"] = fullTok
		if saved := fullTok - skelTok; saved > 0 {
			meta["tokens_saved_vs_full"] = saved
		}
	}
}

// childSignatureLine renders one container member as a single line: its
// signature (body elided), or a name+kind stub when no signature was
// extracted. Methods/functions get a trailing brace-elision marker; fields
// and variables render as a bare declaration.
func childSignatureLine(c *db.Symbol) string {
	sig := strings.TrimSpace(c.Signature)
	switch c.Kind {
	case "Method", "Function":
		line := callableSignatureLine(c)
		return line + " { ... }"
	default:
		// Field / Variable / nested Type / Enum member.
		if sig != "" {
			return sig
		}
		if ret := strings.TrimSpace(c.ReturnType); ret != "" {
			return fmt.Sprintf("%s %s", c.Name, ret)
		}
		return c.Name
	}
}
