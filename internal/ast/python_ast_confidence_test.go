// SPDX-License-Identifier: MIT

package ast

import (
	"testing"
)

// #944: Python AST extractor's symbols used to stamp the langAdapter's
// registered 0.85 confidence (the regex fallback's honest floor), so
// callers had no way to tell AST-extracted Python from regex-extracted.
// The FileResult.ConfidenceOverride field now lets the AST path declare
// its actual extractor tier; ExtractWithModule respects the override
// when stamping per-symbol confidence.

func TestPythonAST_ConfidenceOverride_BoostsSymbols(t *testing.T) {
	if !PythonAvailable() {
		t.Skip("CPython 3 not available — Python AST path can't run")
	}

	src := []byte("def foo():\n    return 1\n\nclass Bar:\n    def method(self):\n        return 2\n")
	result := ExtractWithModule(src, "Python", "pkg/mod.py", "")
	if len(result.Symbols) == 0 {
		t.Fatal("expected Python symbols; got none")
	}
	for _, s := range result.Symbols {
		// AST path stamps 1.0 baseline → Compose may add signal bumps,
		// but it should NOT come back at the regex-tier 0.975 ceiling.
		// Threshold 0.99: AST symbols land at 0.99+, regex symbols cap
		// at 0.975.
		if s.ExtractionConfidence < 0.99 {
			t.Errorf("symbol %q: confidence = %v, want >=0.99 (AST-extracted)",
				s.Name, s.ExtractionConfidence)
		}
	}
}

// FileResult.ConfidenceOverride at 0 (unset) keeps the langAdapter's
// registered confidence — extractors that don't set the override get
// the unchanged baseline. Uses JavaScript with the AST path explicitly
// disabled to exercise the regex fallback (#1328 made JS AST the
// default route, so without the opt-out this fixture would land on
// the AST path and stamp 1.0 instead of the regex-tier 0.85).
func TestExtractWithModule_NoOverride_UsesRegisteredConfidence(t *testing.T) {
	t.Setenv("PINCHER_DISABLE_JS_AST", "1")
	src := []byte("function foo() { return 1; }\nfunction bar() { return 2; }\n")
	result := ExtractWithModule(src, "JavaScript", "pkg/mod.js", "")
	if len(result.Symbols) == 0 {
		t.Skip("JS extractor returned no symbols — adjust fixture")
	}
	for _, s := range result.Symbols {
		// JS regex extractor at 0.85 + Compose signal floor lands at 0.975.
		if s.ExtractionConfidence > 0.99 {
			t.Errorf("regex-tier symbol %q: confidence = %v, expected <=0.99 (no override should apply)",
				s.Name, s.ExtractionConfidence)
		}
	}
}

func TestTypeScriptAST_ConfidenceOverride_BoostsSymbols(t *testing.T) {
	t.Setenv("PINCHER_DISABLE_TS_AST", "")

	src := []byte("export function groundSymbolsForGoal(): number {\n\treturn 1;\n}\n")
	result := ExtractWithModule(src, "TypeScript", "src/self-improve.ts", "")
	if len(result.Symbols) == 0 {
		t.Fatal("expected TypeScript symbols; got none")
	}
	found := false
	for _, s := range result.Symbols {
		if s.Name != "groundSymbolsForGoal" {
			continue
		}
		found = true
		if s.ExtractionConfidence < 0.99 {
			t.Errorf("TypeScript symbol %q confidence = %v, want >=0.99 (tree-sitter AST)",
				s.Name, s.ExtractionConfidence)
		}
	}
	if !found {
		t.Fatalf("groundSymbolsForGoal missing from TypeScript AST extraction; symbols=%+v", result.Symbols)
	}
}
