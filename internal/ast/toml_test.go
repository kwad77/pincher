// SPDX-License-Identifier: MIT

package ast

import (
	"strings"
	"testing"
)

// TestExtractTOML_ExtensionAndLanguage pins .toml extension routing
// and IsSourceFile coverage. Adding the language elsewhere without
// registering the extension would silently skip every .toml file.
func TestExtractTOML_ExtensionAndLanguage(t *testing.T) {
	if got := DetectLanguage("Cargo.toml"); got != "TOML" {
		t.Errorf("DetectLanguage(Cargo.toml) = %q, want TOML", got)
	}
	if got := DetectLanguage("pyproject.toml"); got != "TOML" {
		t.Errorf("DetectLanguage(pyproject.toml) = %q, want TOML", got)
	}
	if !IsSourceFile("foo.toml") {
		t.Error("IsSourceFile(foo.toml) = false, want true")
	}
}

// TestExtractTOML_FlatKeys covers the bag-of-attributes shape (no
// section headers). Real-world example: a top-of-file block in
// pyproject.toml or a .env-style TOML.
func TestExtractTOML_FlatKeys(t *testing.T) {
	src := `name = "pincher"
version = "0.9.0"
license = "MIT"
`
	got := mustExtract(t, src)
	wantQNs(t, got, []string{"name", "version", "license"})
}

// TestExtractTOML_SectionAndNested covers single-level [section] +
// nested [section.subsection] headers and their child keys.
func TestExtractTOML_SectionAndNested(t *testing.T) {
	src := `[server]
host = "localhost"
port = 8080

[server.db]
url = "postgres://..."
pool = 10
`
	got := mustExtract(t, src)
	wantQNs(t, got, []string{
		"server",
		"server.host",
		"server.port",
		"server.db",
		"server.db.url",
		"server.db.pool",
	})
}

// TestExtractTOML_ArrayOfTables — [[items]] produces indexed entries
// (items.0, items.1, ...) so every occurrence in the array gets a
// distinct qualified name. Mirrors how YAML sequence indices show up.
func TestExtractTOML_ArrayOfTables(t *testing.T) {
	src := `[[items]]
name = "first"

[[items]]
name = "second"
`
	got := mustExtract(t, src)
	wantQNs(t, got, []string{
		"items.0",
		"items.0.name",
		"items.1",
		"items.1.name",
	})
}

// TestExtractTOML_DottedTopLevelKey — `a.b.c = 1` declares a single
// leaf and does NOT emit synthetic intermediate symbols; only the
// declared key gets a Setting.
func TestExtractTOML_DottedTopLevelKey(t *testing.T) {
	src := `a.b.c = 1
x = 2
`
	got := mustExtract(t, src)
	wantQNs(t, got, []string{"a.b.c", "x"})
}

// TestExtractTOML_QuotedKey — TOML allows quoted keys with characters
// that would otherwise be reserved (`"weird key"`, `'literal'`, dotted
// segments). The qualified name preserves the literal segment.
func TestExtractTOML_QuotedKey(t *testing.T) {
	src := `"weird key" = 1
'literal-key' = 2
host."x.y" = 3
`
	got := mustExtract(t, src)
	wantQNs(t, got, []string{"weird key", "literal-key", "host.x.y"})
}

// TestExtractTOML_CommentsStripped covers two cases that a naive
// comment-stripper gets wrong: end-of-line comments after a value, and
// `#` characters inside string values that must NOT be treated as
// comment starts.
func TestExtractTOML_CommentsStripped(t *testing.T) {
	src := `# top-level comment
key1 = "value1" # trailing comment
key2 = "string with # not a comment"
key3 = 'literal with # also not a comment'
`
	got := mustExtract(t, src)
	wantQNs(t, got, []string{"key1", "key2", "key3"})
}

// TestExtractTOML_MultilineString — the byte range of a key with a
// multi-line value covers through the closing `"""`. Without this,
// retrieval would return only `description = """` which is useless.
func TestExtractTOML_MultilineString(t *testing.T) {
	src := `description = """
A long
multi-line
description.
"""
version = "1.0"
`
	got := mustExtract(t, src)
	wantQNs(t, got, []string{"description", "version"})

	desc := findSymbol(got, "description")
	if desc == nil {
		t.Fatal("expected `description` symbol")
	}
	body := src[desc.StartByte:desc.EndByte]
	if !strings.Contains(body, `"""`) || !strings.HasSuffix(strings.TrimSpace(body), `"""`) {
		t.Errorf("description byte range did not span the closing `\"\"\"`:\n%q", body)
	}
}

// TestExtractTOML_MultilineArray — the byte range of a key with an
// unclosed `[` covers through the matching `]` even if separated by
// blank lines. Common in pyproject.toml's `[project] dependencies` block.
func TestExtractTOML_MultilineArray(t *testing.T) {
	src := `dependencies = [
  "click>=8.0",
  "pydantic>=2",
  "rich",
]
name = "pkg"
`
	got := mustExtract(t, src)
	wantQNs(t, got, []string{"dependencies", "name"})

	deps := findSymbol(got, "dependencies")
	if deps == nil {
		t.Fatal("expected `dependencies` symbol")
	}
	body := src[deps.StartByte:deps.EndByte]
	if !strings.Contains(body, "click>=8.0") || !strings.Contains(body, "rich") {
		t.Errorf("dependencies byte range did not span the array body:\n%q", body)
	}
}

// TestExtractTOML_MalformedReturnsEmpty — a malformed TOML file emits
// zero symbols rather than partial garbage. Caller's recovery is to
// fix the syntax and re-index. No panic regardless.
func TestExtractTOML_MalformedReturnsEmpty(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Extract panicked on malformed input: %v", r)
		}
	}()
	src := `[unclosed
key = "value"
`
	got := Extract([]byte(src), "TOML", "broken.toml")
	if got == nil {
		t.Fatal("Extract returned nil")
	}
	if len(got.Symbols) != 0 {
		var qns []string
		for _, s := range got.Symbols {
			qns = append(qns, s.QualifiedName)
		}
		t.Errorf("malformed TOML emitted symbols: %v", qns)
	}
}

// TestExtractTOML_CargoToml — real-world pin: a Cargo.toml-shaped
// fixture should produce the keys a Rust dev would expect, including
// the nested `[dependencies]` and `[dev-dependencies]` blocks.
func TestExtractTOML_CargoToml(t *testing.T) {
	src := `[package]
name = "pincher"
version = "0.1.0"
edition = "2021"

[dependencies]
serde = "1.0"
tokio = { version = "1", features = ["full"] }

[dev-dependencies]
criterion = "0.5"
`
	got := mustExtract(t, src)
	wantQNs(t, got, []string{
		"package",
		"package.name",
		"package.version",
		"package.edition",
		"dependencies",
		"dependencies.serde",
		"dependencies.tokio",
		"dev-dependencies",
		"dev-dependencies.criterion",
	})
}

// TestExtractTOML_HighConfidence pins the extractor's BaseExtractor
// signal at 1.0 (parser-backed). Per #34 Phase 2 (PR #107) the final
// per-symbol `extraction_confidence` is composed from BaseExtractor +
// KindBaseline + path/content penalties and clamped to [0,1] — the
// invariant we care about now is "high confidence" (≥ 0.7, the
// default `min_confidence` threshold), not exactly 1.0. If a future
// change drops TOML to regex, BaseExtractor would slip below 0.85
// and the composed score would fall below 0.7 for typical Settings.
func TestExtractTOML_HighConfidence(t *testing.T) {
	if c := (&tomlExtractor{}).Confidence(); c != 1.0 {
		t.Errorf("BaseExtractor signal = %v, want 1.0 (parser-backed)", c)
	}
}

// TestExtractTOML_ByteRangeRetrieval — for a single-line key, the
// byte range starts at the key (not at the start-of-line) and ends at
// end-of-line. Retrieving the symbol returns the declaration as written.
func TestExtractTOML_ByteRangeRetrieval(t *testing.T) {
	src := `[server]
  port = 8080
`
	got := mustExtract(t, src)
	port := findSymbol(got, "server.port")
	if port == nil {
		t.Fatal("expected `server.port` symbol")
	}
	body := src[port.StartByte:port.EndByte]
	if body != "port = 8080" {
		t.Errorf("byte range body = %q, want %q", body, "port = 8080")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Spec edge cases — TOML 1.0 surface that's not exercised by the basic tests.
// ─────────────────────────────────────────────────────────────────────────────

// TestExtractTOML_AllScalarTypes — TOML supports booleans, integers
// (with underscore separators, hex/oct/bin), floats (incl. exponential),
// dates, and times. None of these should confuse the scanner.
func TestExtractTOML_AllScalarTypes(t *testing.T) {
	src := `bool_true = true
bool_false = false
int_plain = 42
int_neg = -17
int_underscored = 1_000_000
int_hex = 0xDEADBEEF
int_oct = 0o755
int_bin = 0b11010110
float_plain = 3.14
float_neg = -0.001
float_exp = 6.022e23
float_inf = inf
date_full = 1979-05-27T07:32:00Z
time_local = 07:32:00
`
	got := mustExtract(t, src)
	for _, qn := range []string{
		"bool_true", "bool_false", "int_plain", "int_neg",
		"int_underscored", "int_hex", "int_oct", "int_bin",
		"float_plain", "float_neg", "float_exp", "float_inf",
		"date_full", "time_local",
	} {
		if findSymbol(got, qn) == nil {
			t.Errorf("missing %q in scalar-types fixture", qn)
		}
	}
}

// TestExtractTOML_InlineTableNoRecursion documents a deliberate
// limitation: inline tables (`{ a = 1, b = 2 }`) are emitted as a
// single Setting on the LHS key — we do NOT recurse into the inline
// table to emit synthetic child Settings for `a`/`b`. The yaml
// extractor recurses because yaml.v3 hands us a Node tree; our TOML
// path is structural source-walking and stopping at the LHS keeps the
// scanner small. This test pins the behaviour so a future "now we
// recurse" change surfaces as a snapshot diff.
func TestExtractTOML_InlineTableNoRecursion(t *testing.T) {
	src := `[dependencies]
tokio = { version = "1", features = ["full"] }
`
	got := mustExtract(t, src)
	if findSymbol(got, "dependencies.tokio") == nil {
		t.Error("expected `dependencies.tokio` symbol")
	}
	if findSymbol(got, "dependencies.tokio.version") != nil {
		t.Error("inline-table member emitted as Setting; documented behaviour is single-Setting-per-LHS")
	}
	if findSymbol(got, "dependencies.tokio.features") != nil {
		t.Error("inline-table member emitted as Setting; documented behaviour is single-Setting-per-LHS")
	}
}

// TestExtractTOML_EmptyFile — an empty file must produce zero symbols
// and not panic. Hits the early-return in Extract.
func TestExtractTOML_EmptyFile(t *testing.T) {
	got := mustExtract(t, "")
	if len(got.Symbols) != 0 {
		t.Errorf("empty file emitted %d symbols, want 0", len(got.Symbols))
	}
}

// TestExtractTOML_OnlyComments — a file containing only comments and
// blank lines parses successfully (TOML allows it) and emits zero
// symbols. Tests that comment-stripping and blank-line skipping
// don't accidentally emit ghost symbols.
func TestExtractTOML_OnlyComments(t *testing.T) {
	src := `# top comment
# another

# trailing
`
	got := mustExtract(t, src)
	if len(got.Symbols) != 0 {
		t.Errorf("comments-only file emitted %d symbols, want 0", len(got.Symbols))
	}
}

// TestExtractTOML_NoTrailingNewline — a file whose last byte is the
// last char of a key=value (no terminating \n) MUST still emit the
// final symbol with a correct byte range. Catches off-by-one errors
// in line-ending handling.
func TestExtractTOML_NoTrailingNewline(t *testing.T) {
	src := `[server]
port = 8080`
	got := mustExtract(t, src)
	wantQNs(t, got, []string{"server", "server.port"})

	port := findSymbol(got, "server.port")
	if port == nil {
		t.Fatal("expected `server.port` symbol")
	}
	body := src[port.StartByte:port.EndByte]
	if body != "port = 8080" {
		t.Errorf("byte range body = %q, want %q", body, "port = 8080")
	}
}

// TestExtractTOML_CRLFLineEndings — Windows-encoded TOML uses \r\n.
// The scanner's line iteration MUST treat \r\n the same as \n;
// without normalisation, the trailing \r would end up as part of the
// final value byte range and break BurntSushi's parseability gate too.
func TestExtractTOML_CRLFLineEndings(t *testing.T) {
	src := "[server]\r\nport = 8080\r\nhost = \"localhost\"\r\n"
	got := mustExtract(t, src)
	wantQNs(t, got, []string{"server", "server.port", "server.host"})
}

// TestExtractTOML_BareKeysWithHyphensAndUnderscores — TOML allows
// bare keys to contain ASCII letters, digits, `-` and `_`. The
// dotted-key splitter MUST treat these as a single segment, not
// break on `-`.
func TestExtractTOML_BareKeysWithHyphensAndUnderscores(t *testing.T) {
	src := `my-key = 1
my_other_key = 2
mixed-_-name = 3

[dev-dependencies]
some-crate = "1.0"
`
	got := mustExtract(t, src)
	wantQNs(t, got, []string{
		"my-key",
		"my_other_key",
		"mixed-_-name",
		"dev-dependencies",
		"dev-dependencies.some-crate",
	})
}

// TestExtractTOML_SameKeyDifferentSections — `port` under both
// `[server]` and `[client]` must produce two distinct qualified
// names (`server.port`, `client.port`), not collide on dedup.
func TestExtractTOML_SameKeyDifferentSections(t *testing.T) {
	src := `[server]
port = 8080

[client]
port = 9000
`
	got := mustExtract(t, src)
	wantQNs(t, got, []string{
		"server", "server.port",
		"client", "client.port",
	})
}

// TestExtractTOML_LeadingWhitespaceOnHeader — `  [section]` (with
// leading whitespace) is valid TOML. The scanner MUST trim before
// the `[` check and start the symbol's byte range at the `[`, not
// at column 0.
func TestExtractTOML_LeadingWhitespaceOnHeader(t *testing.T) {
	src := "  [server]\n  port = 8080\n"
	got := mustExtract(t, src)
	wantQNs(t, got, []string{"server", "server.port"})

	hdr := findSymbol(got, "server")
	if hdr == nil {
		t.Fatal("expected `server` symbol")
	}
	if src[hdr.StartByte] != '[' {
		t.Errorf("header startByte points at %q, want `[`", src[hdr.StartByte])
	}
}

// TestExtractTOML_SectionEndByteSpansBlock — for `[a]` followed by
// `[b]`, the byte range for `a` ends at the start of `[b]`, not at
// the end of its first line. This makes retrieval of `a` return its
// whole block (header + all child keys).
func TestExtractTOML_SectionEndByteSpansBlock(t *testing.T) {
	src := `[a]
x = 1
y = 2

[b]
z = 3
`
	got := mustExtract(t, src)
	a := findSymbol(got, "a")
	if a == nil {
		t.Fatal("expected `a` symbol")
	}
	body := src[a.StartByte:a.EndByte]
	if !strings.Contains(body, "x = 1") || !strings.Contains(body, "y = 2") {
		t.Errorf("section `a` byte range did not span its child keys:\n%q", body)
	}
	if strings.Contains(body, "[b]") || strings.Contains(body, "z = 3") {
		t.Errorf("section `a` byte range bled into the `[b]` block:\n%q", body)
	}
}

// TestExtractTOML_StringWithEscapedQuote — a basic string can hold
// escaped quotes (`"with \"quotes\" inside"`). The comment-stripper
// and findKeyValueSeparator MUST handle the escaped quote and not
// leave string-context early.
func TestExtractTOML_StringWithEscapedQuote(t *testing.T) {
	src := `key = "with \"quotes\" inside" # trailing
other = 1
`
	got := mustExtract(t, src)
	wantQNs(t, got, []string{"key", "other"})
}

// TestExtractTOML_MultilineStringClosingOnSameLine — `key = """foo"""`
// is a valid TOML single-line use of triple-quotes (rare but legal).
// The scanner MUST NOT treat this as opening a multi-line string.
func TestExtractTOML_MultilineStringClosingOnSameLine(t *testing.T) {
	src := `description = """foo"""
next_key = 1
`
	got := mustExtract(t, src)
	wantQNs(t, got, []string{"description", "next_key"})
}

// TestExtractTOML_RealPyprojectFixture — a representative slice of a
// real pyproject.toml. The keys a Python tooling user would type in
// `pip show` / `poetry show` MUST all surface as Settings.
func TestExtractTOML_RealPyprojectFixture(t *testing.T) {
	src := `[build-system]
requires = ["hatchling"]
build-backend = "hatchling.build"

[project]
name = "demo-cli"
version = "0.4.2"
description = "CLI for demoing things"
readme = "README.md"
requires-python = ">=3.10"
authors = [
  { name = "Nick", email = "n@example.com" },
]
dependencies = [
  "click>=8.0",
  "pydantic>=2",
  "rich",
]

[project.optional-dependencies]
dev = ["pytest>=7", "ruff>=0.4"]

[tool.ruff]
line-length = 100
select = ["E", "F", "W"]

[tool.ruff.per-file-ignores]
"tests/*" = ["E501"]
`
	got := mustExtract(t, src)
	for _, qn := range []string{
		"build-system",
		"build-system.requires",
		"build-system.build-backend",
		"project",
		"project.name",
		"project.version",
		"project.description",
		"project.dependencies",
		"project.authors",
		"project.optional-dependencies",
		"project.optional-dependencies.dev",
		"tool.ruff",
		"tool.ruff.line-length",
		"tool.ruff.select",
		"tool.ruff.per-file-ignores",
	} {
		if findSymbol(got, qn) == nil {
			var have []string
			for _, s := range got.Symbols {
				have = append(have, s.QualifiedName)
			}
			t.Errorf("missing %q in pyproject fixture; got %v", qn, have)
		}
	}
}

// TestExtractTOML_PanicResistance feeds a small set of pathological
// inputs that have caused issues in similar parser-backed extractors
// (#69 qualified_name_collision, #74 byte_range_negative, #79/#80
// truncated multi-line state). None should panic; all should return
// a non-nil FileResult.
func TestExtractTOML_PanicResistance(t *testing.T) {
	cases := map[string]string{
		"truncated_section":       `[un`,
		"truncated_array_table":   `[[arr`,
		"truncated_multiline_str": `key = """unterminated`,
		"truncated_multiline_lit": `key = '''unterminated`,
		"truncated_multiline_arr": `key = [1, 2,`,
		"only_equals":             `=`,
		"only_brackets":           `[]`,
		"only_double_brackets":    `[[]]`,
		"key_no_value":            `key =`,
		"value_no_key":            `= "value"`,
		"deeply_nested_inline":    `k = { a = { b = { c = { d = 1 } } } }`,
		"random_garbage":          "\x00\x01\x02\xff\xfe garbage \x7f",
		"unicode_keys":            `日本語 = "hello"` + "\n",
		"only_whitespace":         "    \t\n\n\t   \n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic on %s: %v", name, r)
				}
			}()
			got := Extract([]byte(src), "TOML", name+".toml")
			if got == nil {
				t.Fatalf("Extract returned nil on %s", name)
			}
		})
	}
}

// TestExtractTOML_ByteRangeMonotonic — within a file, every symbol's
// startByte MUST be strictly less than its endByte (no zero-length
// ranges that confuse retrieval), and StartLine/EndLine MUST be
// consistent (EndLine >= StartLine). This is a generic invariant
// that catches bookkeeping bugs across all the byte-range paths.
func TestExtractTOML_ByteRangeMonotonic(t *testing.T) {
	src := `[a]
x = 1
y = "two"

[b.c]
z = """multi
line
string"""

[[items]]
name = "first"
tags = [
  "alpha",
  "beta",
]

[[items]]
name = "second"
`
	got := mustExtract(t, src)
	if len(got.Symbols) == 0 {
		t.Fatal("expected symbols, got none")
	}
	for _, s := range got.Symbols {
		if s.StartByte >= s.EndByte {
			t.Errorf("symbol %q: startByte %d >= endByte %d", s.QualifiedName, s.StartByte, s.EndByte)
		}
		if s.EndLine < s.StartLine {
			t.Errorf("symbol %q: endLine %d < startLine %d", s.QualifiedName, s.EndLine, s.StartLine)
		}
		if s.EndByte > len(src) {
			t.Errorf("symbol %q: endByte %d > source length %d", s.QualifiedName, s.EndByte, len(src))
		}
	}
}

// TestExtractTOML_NotRoutedToOtherLanguages negative-side: a `.go`
// file MUST NOT be routed to the TOML extractor because of a typo or
// extension-table collision. Mirror gate from TestClassifyCorpus_*
// drift detection.
func TestExtractTOML_NotRoutedToOtherLanguages(t *testing.T) {
	if got := DetectLanguage("foo.go"); got == "TOML" {
		t.Errorf("DetectLanguage(foo.go) = TOML, want Go")
	}
	if got := DetectLanguage("foo.yaml"); got == "TOML" {
		t.Errorf("DetectLanguage(foo.yaml) = TOML, want YAML")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// helpers
// ─────────────────────────────────────────────────────────────────────────────

func mustExtract(t *testing.T, src string) *FileResult {
	t.Helper()
	r := Extract([]byte(src), "TOML", "fixture.toml")
	if r == nil {
		t.Fatal("Extract returned nil")
	}
	return r
}

func wantQNs(t *testing.T, r *FileResult, want []string) {
	t.Helper()
	got := map[string]bool{}
	for _, s := range r.Symbols {
		got[s.QualifiedName] = true
	}
	for _, qn := range want {
		if !got[qn] {
			var have []string
			for _, s := range r.Symbols {
				have = append(have, s.QualifiedName)
			}
			t.Errorf("missing qualified name %q; got %v", qn, have)
		}
	}
}

func findSymbol(r *FileResult, qn string) *ExtractedSymbol {
	for i := range r.Symbols {
		if r.Symbols[i].QualifiedName == qn {
			return &r.Symbols[i]
		}
	}
	return nil
}
