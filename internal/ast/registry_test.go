// SPDX-License-Identifier: MIT

package ast

import "testing"

// TestRegisteredConfidence_KnownLanguagesReturnRegisteredValue pins the
// parser-quality contract that HealthCheck depends on: averaging per-symbol
// confidence after path penalties is NOT the right signal for AST vs Regex
// because penalties drag AST scores below 0.99. The registered value is the
// extractor's self-declared quality at registration time and never moves.
func TestRegisteredConfidence_KnownLanguagesReturnRegisteredValue(t *testing.T) {
	cases := []struct {
		lang    string
		wantMin float64
	}{
		{"Go", 0.99},        // AST extractor → 1.0
		{"YAML", 0.99},      // AST extractor → 1.0
		{"Markdown", 0.99},  // AST extractor → 1.0
		{"Python", 0.80},    // stable regex → 0.85
		{"Ruby", 0.65},      // approximate regex → 0.70
	}
	for _, tc := range cases {
		got := RegisteredConfidence(tc.lang)
		if got < tc.wantMin {
			t.Errorf("RegisteredConfidence(%q) = %v, want >= %v", tc.lang, got, tc.wantMin)
		}
	}
}

func TestRegisteredConfidence_UnknownLanguageReturnsNegative(t *testing.T) {
	if got := RegisteredConfidence("Klingon"); got >= 0 {
		t.Errorf("RegisteredConfidence(Klingon) = %v, want < 0 (unknown sentinel)", got)
	}
}
