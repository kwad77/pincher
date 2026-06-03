// SPDX-License-Identifier: MIT

package ast

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// tomlExtractor parses TOML files (Cargo.toml, pyproject.toml,
// .cargo/config.toml, etc.) via BurntSushi/toml for parseability
// validation, then walks source line-by-line emitting one Setting
// symbol per section header and per key assignment.
//
// Qualified names use dotted paths matching TOML's own reference
// convention:
//
//	[server]              →  server                  Setting
//	port = 8080           →  server.port             Setting
//	[server.db]           →  server.db               Setting
//	url = "..."           →  server.db.url           Setting
//	[[items]]             →  items.0                 Setting
//	name = "first"        →  items.0.name            Setting
//	[[items]]             →  items.1                 Setting
//	a.b.c = 1             →  a.b.c                   Setting (dotted top-level key)
//
// Confidence is 1.0: BurntSushi/toml gates parseability (a malformed
// file emits zero symbols), and symbol extraction is structural
// source-walking that mirrors how the keys would be referenced.
type tomlExtractor struct{}

func newTOMLExtractor() *tomlExtractor { return &tomlExtractor{} }

func (t *tomlExtractor) Languages() []string { return []string{"TOML"} }
func (t *tomlExtractor) Extensions() map[string]string {
	return map[string]string{".toml": "TOML"}
}
func (t *tomlExtractor) Confidence() float64 { return 1.0 }

func (t *tomlExtractor) Extract(source []byte, _, relPath string, _ ExtractOptions) (result *FileResult) {
	result = &FileResult{Module: tomlModuleName(relPath)}
	if len(source) == 0 {
		return result
	}

	// Defensive recover. BurntSushi shouldn't panic but we'd rather lose
	// a file's symbols than the whole indexer goroutine.
	defer func() {
		if r := recover(); r != nil {
			if result == nil {
				result = &FileResult{Module: tomlModuleName(relPath)}
			}
		}
	}()

	// Parseability gate. An invalid TOML file emits zero symbols rather
	// than partial garbage — caller can re-index after fixing the syntax.
	var decoded map[string]any
	if _, err := toml.Decode(string(source), &decoded); err != nil {
		return result
	}

	lineOffsets := buildLineOffsets(source)
	sourceLen := len(source)

	type entry struct {
		path      []string
		startByte int
		endByte   int
		startLine int
		endLine   int
		signature string
		isHeader  bool
	}
	var entries []entry
	emitted := map[string]bool{}
	arrayCounts := map[string]int{} // [[a.b]] occurrence index, keyed by dotted path

	var section []string

	// Multi-line state. inMSValue tracks the entry-index of the key
	// whose value spans across lines so we can patch its endByte when
	// the value closes.
	inMS := false
	var msTerminator string
	arrayDepth := 0
	multiEntryIdx := -1

	for li := 0; li < len(lineOffsets); li++ {
		lineStart := lineOffsets[li]
		var lineEnd int
		if li+1 < len(lineOffsets) {
			lineEnd = lineOffsets[li+1] - 1 // exclude the trailing \n
		} else {
			// Last logical line. buildLineOffsets does not add an entry
			// for "after the final \n", so li+1 falls off the end here.
			// Strip a trailing \n if present so symbol byte ranges don't
			// include it.
			lineEnd = sourceLen
			if lineEnd > 0 && source[lineEnd-1] == '\n' {
				lineEnd--
			}
		}
		line := source[lineStart:lineEnd]
		lineNum := li + 1

		// Inside a multi-line string: hand the entire line back into the
		// "opaque content" bucket and look for the terminator. When we
		// find it, patch the multi-line entry's endByte to cover through
		// the terminator's end.
		if inMS {
			if idx := bytes.Index(line, []byte(msTerminator)); idx >= 0 {
				inMS = false
				if multiEntryIdx >= 0 {
					entries[multiEntryIdx].endByte = lineStart + idx + len(msTerminator)
					entries[multiEntryIdx].endLine = lineNum
					multiEntryIdx = -1
				}
			}
			continue
		}

		// Inside a multi-line array: bracket-balance. Strings can hold
		// brackets; countOutsideStrings filters those out.
		if arrayDepth > 0 {
			arrayDepth += countOutsideStrings(line, '[') - countOutsideStrings(line, ']')
			if arrayDepth <= 0 {
				arrayDepth = 0
				if multiEntryIdx >= 0 {
					entries[multiEntryIdx].endByte = lineEnd
					entries[multiEntryIdx].endLine = lineNum
					multiEntryIdx = -1
				}
			}
			continue
		}

		clean := stripTOMLComment(line)
		trimmed := bytes.TrimSpace(clean)
		if len(trimmed) == 0 {
			continue
		}

		// Section header: [a.b] or [[a.b]] (array of tables).
		if trimmed[0] == '[' {
			isArrayTable := len(trimmed) >= 2 && trimmed[1] == '['
			var inner []byte
			if isArrayTable {
				if end := bytes.Index(trimmed, []byte("]]")); end >= 0 {
					inner = trimmed[2:end]
				} else {
					continue
				}
			} else {
				if end := bytes.IndexByte(trimmed, ']'); end >= 0 {
					inner = trimmed[1:end]
				} else {
					continue
				}
			}
			parts := splitDottedKey(string(inner))
			if len(parts) == 0 {
				continue
			}

			section = parts
			if isArrayTable {
				key := strings.Join(parts, ".")
				idx := arrayCounts[key]
				arrayCounts[key] = idx + 1
				section = append(section, fmt.Sprintf("%d", idx))
			}

			qn := strings.Join(section, ".")
			if !emitted[qn] {
				emitted[qn] = true
				offset := bytes.Index(line, []byte("[")) // first `[` on the source line
				if offset < 0 {
					offset = 0
				}
				entries = append(entries, entry{
					path:      append([]string(nil), section...),
					startByte: lineStart + offset,
					endByte:   sourceLen, // patched after scan
					startLine: lineNum,
					endLine:   lineNum,
					signature: string(trimmed),
					isHeader:  true,
				})
			}
			continue
		}

		// Key = value.
		eqIdx := findKeyValueSeparator(clean)
		if eqIdx < 0 {
			continue
		}
		keyPart := bytes.TrimSpace(clean[:eqIdx])
		valuePart := bytes.TrimSpace(clean[eqIdx+1:])
		keyParts := splitDottedKey(string(keyPart))
		if len(keyParts) == 0 {
			continue
		}

		full := append(append([]string(nil), section...), keyParts...)
		qn := strings.Join(full, ".")
		if emitted[qn] {
			continue
		}
		emitted[qn] = true

		// Locate the key's start within the source line. Use the first
		// non-whitespace offset rather than searching for keyPart bytes —
		// quoted keys can have escapes that break naive index lookup.
		keyOffset := leadingWhitespace(line)

		ent := entry{
			path:      full,
			startByte: lineStart + keyOffset,
			endByte:   lineEnd,
			startLine: lineNum,
			endLine:   lineNum,
			signature: string(trimmed),
		}
		entries = append(entries, ent)

		// Detect multi-line value openers. A `"""` or `'''` that doesn't
		// also close on the same line opens a multi-line string. An
		// unbalanced `[` opens a multi-line array.
		if openMS, term := openMultilineString(valuePart); openMS {
			inMS = true
			msTerminator = term
			multiEntryIdx = len(entries) - 1
			continue
		}
		opens := countOutsideStrings(valuePart, '[')
		closes := countOutsideStrings(valuePart, ']')
		if opens > closes {
			arrayDepth = opens - closes
			multiEntryIdx = len(entries) - 1
		}
	}

	// Patch header end-bytes: each section header runs through to the
	// start of the next header or EOF. Walk in reverse so each header's
	// successor is already finalised.
	nextHeaderStart := sourceLen
	nextHeaderLine := 0
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].isHeader {
			entries[i].endByte = nextHeaderStart
			if nextHeaderLine > 0 {
				entries[i].endLine = nextHeaderLine - 1
				if entries[i].endLine < entries[i].startLine {
					entries[i].endLine = entries[i].startLine
				}
			} else {
				if len(lineOffsets) > 0 {
					entries[i].endLine = len(lineOffsets)
				}
			}
			nextHeaderStart = entries[i].startByte
			nextHeaderLine = entries[i].startLine
		}
	}

	for _, e := range entries {
		name := e.path[len(e.path)-1]
		result.Symbols = append(result.Symbols, ExtractedSymbol{
			Name:          name,
			QualifiedName: strings.Join(e.path, "."),
			Kind:          "Setting",
			StartByte:     e.startByte,
			EndByte:       e.endByte,
			StartLine:     e.startLine,
			EndLine:       e.endLine,
			Signature:     e.signature,
			IsExported:    true,
		})
	}
	return result
}

func tomlModuleName(relPath string) string {
	base := filepath.Base(relPath)
	if ext := filepath.Ext(base); ext != "" {
		base = base[:len(base)-len(ext)]
	}
	return base
}

// splitDottedKey splits a TOML key (table header inner content or a
// dotted key on the LHS of `=`) into its component segments,
// respecting quoted segments. `a.b."c.d".e` → ["a", "b", "c.d", "e"].
func splitDottedKey(s string) []string {
	var out []string
	i := 0
	for i < len(s) {
		// Skip leading whitespace.
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		if i >= len(s) {
			break
		}
		var seg strings.Builder
		switch s[i] {
		case '"':
			i++
			for i < len(s) && s[i] != '"' {
				if s[i] == '\\' && i+1 < len(s) {
					seg.WriteByte(s[i+1])
					i += 2
					continue
				}
				seg.WriteByte(s[i])
				i++
			}
			if i < len(s) {
				i++ // consume closing "
			}
		case '\'':
			i++
			for i < len(s) && s[i] != '\'' {
				seg.WriteByte(s[i])
				i++
			}
			if i < len(s) {
				i++
			}
		default:
			for i < len(s) && s[i] != '.' && s[i] != ' ' && s[i] != '\t' {
				seg.WriteByte(s[i])
				i++
			}
		}
		if seg.Len() > 0 {
			out = append(out, seg.String())
		}
		// Skip trailing whitespace + the dot separator.
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		if i < len(s) && s[i] == '.' {
			i++
		}
	}
	return out
}

// findKeyValueSeparator returns the index of the `=` that separates a
// TOML key from its value, ignoring `=` inside quoted key segments.
// Returns -1 if no separator is found.
func findKeyValueSeparator(line []byte) int {
	inDouble := false
	inSingle := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case inDouble:
			if c == '\\' && i+1 < len(line) {
				i++
				continue
			}
			if c == '"' {
				inDouble = false
			}
		case inSingle:
			if c == '\'' {
				inSingle = false
			}
		default:
			if c == '"' {
				inDouble = true
			} else if c == '\'' {
				inSingle = true
			} else if c == '=' {
				return i
			}
		}
	}
	return -1
}

// stripTOMLComment removes a trailing `# comment` from a line, but
// only when the `#` falls outside string context. TOML comments run
// to end-of-line and are not allowed inside strings.
func stripTOMLComment(line []byte) []byte {
	inDouble := false
	inSingle := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case inDouble:
			if c == '\\' && i+1 < len(line) {
				i++
				continue
			}
			if c == '"' {
				inDouble = false
			}
		case inSingle:
			if c == '\'' {
				inSingle = false
			}
		default:
			if c == '"' {
				inDouble = true
			} else if c == '\'' {
				inSingle = true
			} else if c == '#' {
				return line[:i]
			}
		}
	}
	return line
}

// countOutsideStrings counts `target` occurrences in `line` that fall
// outside double- or single-quoted string contexts. Used for
// bracket-balancing multi-line arrays without false-counting brackets
// embedded in string values.
func countOutsideStrings(line []byte, target byte) int {
	count := 0
	inDouble := false
	inSingle := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case inDouble:
			if c == '\\' && i+1 < len(line) {
				i++
				continue
			}
			if c == '"' {
				inDouble = false
			}
		case inSingle:
			if c == '\'' {
				inSingle = false
			}
		default:
			if c == '"' {
				inDouble = true
			} else if c == '\'' {
				inSingle = true
			} else if c == target {
				count++
			}
		}
	}
	return count
}

// openMultilineString reports whether `value` opens a TOML multi-line
// string (`"""` or `'''`) without also closing it on the same line.
// Returns the matching terminator when it does.
func openMultilineString(value []byte) (bool, string) {
	for _, term := range []string{`"""`, `'''`} {
		t := []byte(term)
		if bytes.HasPrefix(value, t) {
			// Closes on the same line iff the terminator appears after
			// the opening triple. Search the suffix for another instance.
			rest := value[3:]
			if bytes.Contains(rest, t) {
				return false, ""
			}
			return true, term
		}
	}
	return false, ""
}

// leadingWhitespace returns the byte index of the first non-whitespace
// character in `line`. Used to anchor a key symbol's startByte at the
// key itself rather than the start of the line.
func leadingWhitespace(line []byte) int {
	for i, c := range line {
		if c != ' ' && c != '\t' {
			return i
		}
	}
	return len(line)
}

func init() {
	Register(newTOMLExtractor())
}
