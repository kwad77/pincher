// SPDX-License-Identifier: MIT

package server

import (
	"fmt"
	"sort"
	"strings"
)

// Skeleton mode — deterministic structural compression of source bodies.
//
// Rationale (measured economics): compute below the envelope is token-free;
// only conclusions pay. Agents skimming code in the orientation/probe phase
// need shape, not bodies — a 200-line function should ship as ~10 lines.
// skeletonize is a line-classifier pass over the already-retrieved source
// bytes (byte-offset read — no re-parse) that keeps:
//
//   - the signature line(s) verbatim (line 0 plus continuation lines while
//     parens stay unbalanced),
//   - top-level control-flow lines verbatim (if/for/switch/select/return/
//     defer/try/except/... — nesting indicated by the original indentation),
//   - the trailing closer line ("}", "end", ...),
//
// and elides every other run of lines into a single marker:
//
//	… <N> lines (calls: A, B)
//
// Call names are harvested from the symbol's outbound CALLS edges (already
// in the DB — free) intersected with textual occurrence ordering inside the
// elided run. Edge-listed callees that text matching can't place (aliased
// imports, method-expression calls, builder chains) are appended in one
// trailing "… calls (from graph): X, Y" line, so every CALLS-edge callee
// name is guaranteed to appear in the skeleton.
//
// Deliberately language-agnostic and tree-sitter-free for v1: the line
// classifier is deterministic, cheap, and works for every indexed language.
// Tree-sitter-precise skeletons (exact block boundaries, expression-level
// elision) are the documented v2 path.

// skeletonFlowKeywords is the language-agnostic superset of control-flow
// line openers kept verbatim. Lowercase only — every indexed language's
// flow keywords are lowercase.
var skeletonFlowKeywords = map[string]bool{
	"if": true, "else": true, "elif": true, "for": true, "while": true,
	"switch": true, "case": true, "default": true, "select": true,
	"return": true, "defer": true, "go": true, "try": true, "except": true,
	"catch": true, "finally": true, "match": true, "when": true,
	"raise": true, "throw": true, "break": true, "continue": true,
	"guard": true, "loop": true, "yield": true,
}

// skeletonize compresses source into a deterministic structural outline.
// calleeNames is the short-name list from the symbol's outbound CALLS
// edges; pass nil when no edge data is available (markers then carry line
// counts only). Identical inputs always produce identical output.
func skeletonize(source string, calleeNames []string) string {
	lines := strings.Split(source, "\n")
	if len(lines) <= 2 {
		return source // nothing to elide
	}

	// De-dup callee names, preserving edge order, dropping empties.
	names := make([]string, 0, len(calleeNames))
	seenName := map[string]bool{}
	for _, n := range calleeNames {
		if n == "" || seenName[n] {
			continue
		}
		seenName[n] = true
		names = append(names, n)
	}

	keep := make([]bool, len(lines))

	// 1. Signature lines: line 0, plus continuations while parens stay
	// unbalanced (multi-line parameter lists). Capped at 8 lines so a
	// pathological unbalanced body can't drag the whole source in.
	depth := 0
	for i := 0; i < len(lines) && i < 8; i++ {
		depth += strings.Count(lines[i], "(") - strings.Count(lines[i], ")")
		keep[i] = true
		if depth <= 0 {
			break
		}
	}

	// 2. Control-flow lines.
	for i := range lines {
		if !keep[i] && isSkeletonFlowLine(lines[i]) {
			keep[i] = true
		}
	}

	// 3. Trailing closer ("}", "end", ...) — skip trailing blanks first.
	for i := len(lines) - 1; i > 0; i-- {
		t := strings.TrimSpace(lines[i])
		if t == "" {
			continue
		}
		if isSkeletonCloserLine(t) {
			keep[i] = true
		}
		break
	}

	// Names already visible in kept lines never need a marker mention.
	var keptText strings.Builder
	for i, ln := range lines {
		if keep[i] {
			keptText.WriteString(ln)
			keptText.WriteByte('\n')
		}
	}
	emitted := map[string]bool{}
	for _, n := range names {
		if containsIdent(keptText.String(), n) {
			emitted[n] = true
		}
	}

	// 4. Assemble: kept lines verbatim, elided runs collapsed to markers.
	out := make([]string, 0, len(lines)/3+4)
	for i := 0; i < len(lines); {
		if keep[i] {
			out = append(out, lines[i])
			i++
			continue
		}
		j := i
		blankOnly := true
		var runText strings.Builder
		for j < len(lines) && !keep[j] {
			if strings.TrimSpace(lines[j]) != "" {
				blankOnly = false
			}
			runText.WriteString(lines[j])
			runText.WriteByte('\n')
			j++
		}
		if !blankOnly {
			indent := skeletonRunIndent(lines[i:j])
			calls := skeletonCallsInRun(runText.String(), names, emitted)
			if len(calls) > 0 {
				out = append(out, fmt.Sprintf("%s… %d lines (calls: %s)", indent, j-i, strings.Join(calls, ", ")))
			} else {
				out = append(out, fmt.Sprintf("%s… %d lines", indent, j-i))
			}
		}
		i = j
	}

	// 5. Graph-callee guarantee: edge-listed callees that text matching
	// couldn't place still appear, in one trailing marker. Sorted for
	// determinism (edge enumeration order is not contractual).
	var missing []string
	for _, n := range names {
		if !emitted[n] {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		out = append(out, fmt.Sprintf("… calls (from graph): %s", strings.Join(missing, ", ")))
	}

	return strings.Join(out, "\n")
}

// isSkeletonFlowLine reports whether the line opens with a control-flow
// keyword, optionally behind closing braces/parens ("} else if x {").
func isSkeletonFlowLine(line string) bool {
	t := strings.TrimSpace(line)
	for len(t) > 0 && (t[0] == '}' || t[0] == ')') {
		t = strings.TrimSpace(t[1:])
	}
	k := 0
	for k < len(t) && t[k] >= 'a' && t[k] <= 'z' {
		k++
	}
	if k == 0 {
		return false
	}
	if k < len(t) && isIdentChar(t[k]) {
		return false // longer identifier, e.g. "iface" / "formatted"
	}
	return skeletonFlowKeywords[t[:k]]
}

// isSkeletonCloserLine reports whether a trimmed line is a pure
// block-closer worth keeping (the function's final brace).
func isSkeletonCloserLine(t string) bool {
	if t == "end" {
		return true
	}
	if len(t) == 0 || len(t) > 4 {
		return false
	}
	for i := 0; i < len(t); i++ {
		switch t[i] {
		case '}', ')', ']', ';', ',':
		default:
			return false
		}
	}
	return true
}

// skeletonRunIndent returns the leading whitespace of the first non-blank
// line in the run, so the marker sits at the elided code's nesting depth.
func skeletonRunIndent(run []string) string {
	for _, ln := range run {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		for i := 0; i < len(ln); i++ {
			if ln[i] != ' ' && ln[i] != '\t' {
				return ln[:i]
			}
		}
		return ln
	}
	return ""
}

// skeletonCallsInRun returns the not-yet-emitted callee names occurring
// (as whole identifiers) in the run, ordered by first occurrence, and
// marks them emitted. Each callee is mentioned in at most one marker —
// the first run it appears in.
func skeletonCallsInRun(run string, names []string, emitted map[string]bool) []string {
	type hit struct {
		pos  int
		name string
	}
	var hits []hit
	for _, n := range names {
		if emitted[n] {
			continue
		}
		if p := identFirstIndex(run, n); p >= 0 {
			hits = append(hits, hit{p, n})
			emitted[n] = true
		}
	}
	sort.Slice(hits, func(a, b int) bool {
		if hits[a].pos != hits[b].pos {
			return hits[a].pos < hits[b].pos
		}
		return hits[a].name < hits[b].name
	})
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.name
	}
	return out
}

func isIdentChar(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// identFirstIndex returns the byte offset of the first whole-identifier
// occurrence of name in s, or -1.
func identFirstIndex(s, name string) int {
	if name == "" {
		return -1
	}
	for idx := 0; idx <= len(s)-len(name); {
		p := strings.Index(s[idx:], name)
		if p < 0 {
			return -1
		}
		p += idx
		end := p + len(name)
		if (p == 0 || !isIdentChar(s[p-1])) && (end >= len(s) || !isIdentChar(s[end])) {
			return p
		}
		idx = p + 1
	}
	return -1
}

// containsIdent reports whether name occurs in s as a whole identifier.
func containsIdent(s, name string) bool {
	return identFirstIndex(s, name) >= 0
}

// parseDetailArg reads the `detail` arg shared by symbol/symbols/context.
// Returns skeleton=true for "skeleton", false for ""/"full". Unknown
// values degrade to full with a warning (soft contract — same family as
// the fields-projection unknown-key handling, #908).
func parseDetailArg(args map[string]any) (skeleton bool, warning string) {
	switch v := str(args, "detail"); v {
	case "", "full":
		return false, ""
	case "skeleton":
		return true, ""
	default:
		return false, fmt.Sprintf(
			"unknown detail %q — valid values: \"full\" (default), \"skeleton\"; returning full source", v)
	}
}

// calleeShortNames returns the short names of the symbol's outbound
// CALLS-edge targets, in edge order, de-duplicated. Edge data is already
// in the DB — harvesting it costs one indexed query, no re-parse.
func (s *Server) calleeShortNames(projectID, symbolID string) []string {
	edges, err := s.store.EdgesFromScoped(projectID, symbolID, []string{"CALLS"})
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(edges))
	seen := map[string]bool{}
	for _, e := range edges {
		n := shortNameFromID(e.ToID)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		names = append(names, n)
	}
	return names
}

// attachSkeletonMeta stamps one top-level `_meta.skeleton: true` marker.
// One top-level boolean was chosen over per-entry markers deliberately:
// detail=skeleton applies to the whole call, so N per-entry booleans add
// payload weight without adding information (the cheaper option wins —
// same economics that motivate skeleton mode itself).
func attachSkeletonMeta(data map[string]any) {
	meta, _ := data["_meta"].(map[string]any)
	if meta == nil {
		meta = map[string]any{}
		data["_meta"] = meta
	}
	meta["skeleton"] = true
}
