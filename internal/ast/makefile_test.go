// SPDX-License-Identifier: MIT

package ast

import (
	"strings"
	"testing"
)

// TestExtractMakefile_RuleTargets pins the basic case: rule targets at
// column 0 produce one Function symbol each.
func TestExtractMakefile_RuleTargets(t *testing.T) {
	src := []byte(`# Build commands

build:
	go build ./...

test: build
	go test ./...

clean:
	rm -f *.o
`)
	r := extractMakefile(src, "Makefile")
	got := map[string]ExtractedSymbol{}
	for _, s := range r.Symbols {
		got[s.Name] = s
	}
	for _, want := range []string{"build", "test", "clean"} {
		s, ok := got[want]
		if !ok {
			t.Errorf("missing rule %q in extracted symbols: %v", want, keysOf(got))
			continue
		}
		if s.Kind != "Function" {
			t.Errorf("rule %q kind = %q, want Function", want, s.Kind)
		}
		if s.QualifiedName != want {
			t.Errorf("rule %q qualified name = %q, want %q", want, s.QualifiedName, want)
		}
	}
}

// TestExtractMakefile_PhonyTargetsExported pins that targets named in
// `.PHONY:` lines get is_exported=true. This is the closest analogue
// to "public API" in Makefile semantics — the user is explicitly
// declaring "this name is a callable target, not a real file".
func TestExtractMakefile_PhonyTargetsExported(t *testing.T) {
	src := []byte(`.PHONY: build test clean

build:
	go build

test:
	go test

clean:
	rm -rf bin/

main: build
	go run main.go
`)
	r := extractMakefile(src, "Makefile")
	got := map[string]bool{}
	for _, s := range r.Symbols {
		if s.Kind == "Function" {
			got[s.Name] = s.IsExported
		}
	}
	for _, name := range []string{"build", "test", "clean"} {
		if !got[name] {
			t.Errorf("rule %q listed in .PHONY should have IsExported=true, got %v", name, got[name])
		}
	}
	// `main` is not in .PHONY — it builds a real file, so IsExported must be false.
	if got["main"] {
		t.Errorf("rule `main` not listed in .PHONY should have IsExported=false")
	}
}

// TestExtractMakefile_VariableAssignments pins that top-level variable
// definitions emit Setting symbols. Five forms must all be recognised:
//
//   - `=` (recursive)
//   - `:=` (immediate)
//   - `::=` (POSIX immediate)
//   - `?=` (conditional)
//   - `+=` (append)
func TestExtractMakefile_VariableAssignments(t *testing.T) {
	src := []byte(`GO = go
BIN := pincher
PORT ::= 8080
DEBUG ?= false
LDFLAGS += -X main.version=$(VERSION)

build:
	$(GO) build -o $(BIN)
`)
	r := extractMakefile(src, "Makefile")
	got := map[string]ExtractedSymbol{}
	for _, s := range r.Symbols {
		if s.Kind == "Setting" {
			got[s.Name] = s
		}
	}
	for _, name := range []string{"GO", "BIN", "PORT", "DEBUG", "LDFLAGS"} {
		if _, ok := got[name]; !ok {
			t.Errorf("missing variable %q (got Settings: %v)", name, keysOf(got))
		}
	}
}

// TestExtractMakefile_PatternRulesSkipped pins that `%.o: %.c` style
// pattern rules are NOT emitted as symbols — they have no concrete
// name to extract. Same for variable-expanded names like `$(VAR):`.
func TestExtractMakefile_PatternRulesSkipped(t *testing.T) {
	src := []byte(`%.o: %.c
	$(CC) -c $< -o $@

$(BIN): main.o util.o
	$(CC) -o $(BIN) main.o util.o

real_target: deps
	echo done
`)
	r := extractMakefile(src, "Makefile")
	got := map[string]bool{}
	for _, s := range r.Symbols {
		if s.Kind == "Function" {
			got[s.Name] = true
		}
	}
	if got["%.o"] {
		t.Errorf("pattern rule %q should not produce a Function symbol", "%.o")
	}
	if got["$(BIN)"] {
		t.Error("variable-expanded rule `$(BIN)` should not produce a Function symbol")
	}
	if !got["real_target"] {
		t.Error("concrete rule `real_target` should produce a Function symbol")
	}
}

// TestExtractMakefile_RecipeContentNotMistakenForVariables pins that
// recipe lines (TAB-indented under a rule) which contain `=` (e.g.
// `FOO=bar make sub-target`) don't get mis-extracted as variable
// definitions. The column-0 anchor in the regex prevents this; the
// test pins it.
func TestExtractMakefile_RecipeContentNotMistakenForVariables(t *testing.T) {
	src := []byte(`build:
	FOO=bar make subcomponent
	echo "BUILT_AT = $(date)"

VAR = real_top_level
`)
	r := extractMakefile(src, "Makefile")
	settings := map[string]bool{}
	for _, s := range r.Symbols {
		if s.Kind == "Setting" {
			settings[s.Name] = true
		}
	}
	if settings["FOO"] {
		t.Error("recipe-line `FOO=bar` should not be extracted as a top-level Setting")
	}
	if settings["BUILT_AT"] {
		t.Error("recipe-content `BUILT_AT = …` inside a quoted echo should not be extracted as a Setting")
	}
	if !settings["VAR"] {
		t.Error("real top-level `VAR = real_top_level` should be extracted")
	}
}

// TestMakefile_DetectedByFilename pins the registry's filename-based
// detection branch — `Makefile`, `GNUmakefile`, and the lowercase
// variant should all resolve to the Makefile language without an
// extension.
func TestMakefile_DetectedByFilename(t *testing.T) {
	cases := []struct {
		filename string
		want     string
	}{
		{"Makefile", "Makefile"},
		{"GNUmakefile", "Makefile"},
		{"makefile", "Makefile"},
		{"build.mk", "Makefile"},
		{"rules.mak", "Makefile"},
		// Path-shaped filenames must still resolve via basename.
		{"subdir/Makefile", "Makefile"},
		// Confirm non-matches return empty.
		{"main.c", ""}, // C extractor handles
		{"NotAMakefile.txt", ""},
	}
	for _, c := range cases {
		got := DetectLanguage(c.filename)
		if c.want == "" {
			if got == "Makefile" {
				t.Errorf("DetectLanguage(%q) = %q, should not match Makefile", c.filename, got)
			}
			continue
		}
		if got != c.want {
			t.Errorf("DetectLanguage(%q) = %q, want %q", c.filename, got, c.want)
		}
	}
}

// TestMakefile_RegisteredConfidenceIs0_85 pins the confidence tier:
// regex-based extractors at the stable tier are 0.85; this is the
// commitment the per-symbol scorer relies on.
func TestMakefile_RegisteredConfidenceIs0_85(t *testing.T) {
	if got := RegisteredConfidence("Makefile"); got != 0.85 {
		t.Errorf("RegisteredConfidence(Makefile) = %v, want 0.85", got)
	}
}

// TestExtractMakefile_RuleByteRangeIncludesRecipe pins #1745: a rule
// symbol's [StartByte, EndByte) spans the rule line plus the contiguous
// TAB-prefixed recipe beneath it, so `symbol`/`context` return what the
// target actually does instead of just `build:`. The first non-TAB line
// (blank line or column-0 comment) ends the recipe.
func TestExtractMakefile_RuleByteRangeIncludesRecipe(t *testing.T) {
	src := []byte(`build:
	go build ./...
	go vet ./...

test: build
	go test ./...
# column-0 comment after a blank-less recipe end? no — comment ends it
clean:
	rm -f *.o`)
	r := extractMakefile(src, "Makefile")
	got := map[string]ExtractedSymbol{}
	for _, s := range r.Symbols {
		got[s.Name] = s
	}

	cases := []struct {
		name     string
		contains []string
		excludes []string
		endLine  int
	}{
		// Recipe with two commands, ended by a blank line.
		{"build", []string{"go build ./...", "go vet ./..."}, []string{"test:"}, 3},
		// Recipe ended by a column-0 comment line.
		{"test", []string{"go test ./..."}, []string{"# column-0"}, 6},
		// Recipe at EOF with no trailing newline.
		{"clean", []string{"rm -f *.o"}, nil, 9},
	}
	for _, tc := range cases {
		s, ok := got[tc.name]
		if !ok {
			t.Fatalf("missing rule %q in extracted symbols: %v", tc.name, keysOf(got))
		}
		body := string(src[s.StartByte:s.EndByte])
		for _, want := range tc.contains {
			if !strings.Contains(body, want) {
				t.Errorf("rule %q range %q should contain %q", tc.name, body, want)
			}
		}
		for _, not := range tc.excludes {
			if strings.Contains(body, not) {
				t.Errorf("rule %q range %q should NOT contain %q", tc.name, body, not)
			}
		}
		if s.EndLine != tc.endLine {
			t.Errorf("rule %q EndLine = %d, want %d", tc.name, s.EndLine, tc.endLine)
		}
	}
}

// TestExtractMakefile_VariableByteRangeStaysLineOnly pins the other half
// of the #1745 invariant: Setting symbols (variable definitions) keep
// their definition-line-only range — recipe extension applies to rules.
func TestExtractMakefile_VariableByteRangeStaysLineOnly(t *testing.T) {
	src := []byte(`GO ?= go
BIN := ./pincher

build:
	$(GO) build
`)
	r := extractMakefile(src, "Makefile")
	for _, s := range r.Symbols {
		if s.Kind != "Setting" {
			continue
		}
		body := string(src[s.StartByte:s.EndByte])
		if strings.Contains(body, "\n") {
			t.Errorf("setting %q range %q spans multiple lines, want single line", s.Name, body)
		}
	}
}
