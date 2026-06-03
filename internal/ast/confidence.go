// SPDX-License-Identifier: MIT

package ast

import (
	"path/filepath"
	"regexp"
	"strings"
)

// Per-symbol confidence scoring (#34).
//
// Phase 1 (#105 ✅): introduced the Signals + Compose() machinery with all
// signals at zero — net behavior identical to per-language constants.
// Phase 2 (this PR): populates kindBaseline + pathPatterns; wires identifier-
// shape and generated-marker signals. The pinned-corpus snapshot diff in
// this PR IS the rationale — every confidence shift traces to one signal.
//
// Future:
// - Phase 3 — tool surface (min_confidence + _meta.confidence_distribution)
// - Phase 4 — default min_confidence flip from 0.0 → 0.7
//
// See design/per-symbol-confidence.md for the full plan.

// Signals carries the per-symbol score components. Each field is a pure
// function of (extractor output, file path, kind, source). Compose() reduces
// them to a single confidence in [0, 1]. Phase 1 leaves every contributor
// equal to the no-op value, so Compose() == BaseExtractor for every symbol.
type Signals struct {
	// BaseExtractor is the existing per-language constant from Extractor.Confidence().
	// 1.0 for AST-backed extractors, 0.85 for stable regex, 0.70 for approximate.
	BaseExtractor float64

	// KindBaseline reflects the symbol kind's structural informativeness.
	// Set by computeSignals from the kindBaseline lookup, or falls back to
	// BaseExtractor when the kind isn't in the table. Phase 1 always falls
	// back, so KindBaseline == BaseExtractor for every symbol.
	KindBaseline float64

	// PathPenalty is a negative contribution from the file path (lockfile,
	// vendor/, generated dist/, README, etc.). Always non-positive.
	PathPenalty float64

	// IdentBonus is +0.05 for clean identifiers, -0.10 for empty/whitespace
	// names.
	IdentBonus float64

	// GeneratedPen fires on `// Code generated` markers in the file head.
	// Phase 1 leaves this 0.
	GeneratedPen float64
}

// Compose reduces the signals to a single confidence score.
//
// Composition: average(BaseExtractor, KindBaseline) + sum-of-deltas, then clamp
// to [0, 1]. Averaging the two baselines (rather than summing) keeps the
// expected range bounded regardless of how the lookups evolve. Penalties and
// bonuses then push the score within [0, 1].
//
// Order-independent by construction (commutative addition of the deltas).
// Pure function — same inputs produce the same output, byte-identical, on
// any platform. These properties are pinned by tests in confidence_test.go.
func (s Signals) Compose() float64 {
	base := (s.BaseExtractor + s.KindBaseline) / 2.0
	score := base + s.PathPenalty + s.IdentBonus + s.GeneratedPen
	if score < 0 {
		return 0
	}
	if score > 1 {
		return 1
	}
	return score
}

// kindBaseline maps symbol kinds to their structural-informativeness
// baseline. Symbols with kinds not in the table fall back to BaseExtractor
// in computeSignals, so unknown kinds keep today's per-language behavior.
//
// Values reflect "how much load-bearing structure does this kind carry?":
//   - First-class code constructs (Function/Method/Class/Interface) score 1.0
//   - Configuration symbols (Setting/Variable/Resource) score 0.95
//   - Structural blocks (Block) score 0.85
//   - Documentation kinds (Section/Heading) score 0.80
//   - Loose-prose kinds (Document/CodeSnippet) score 0.70
var kindBaseline = map[string]float64{
	"Function":    1.00,
	"Method":      1.00,
	"Class":       1.00,
	"Interface":   1.00,
	"Type":        1.00,
	"Enum":        1.00,
	"Module":      0.95,
	"Variable":    0.95,
	"Setting":     0.95,
	"Resource":    0.95,
	"DataSource":  0.95,
	"Output":      0.95,
	"Provider":    0.95,
	"Local":       0.95,
	"Block":       0.85,
	"Section":     0.80,
	"Heading":     0.80,
	"Document":    0.70,
	"CodeSnippet": 0.70,
}

// pathPatterns lists path-shape penalties. Each rule is reviewable by intent
// (Reason field surfaces in tests + future diagnostics). The list is
// deliberately conservative — only patterns where the wrong-direction signal
// is unambiguous (lockfile = generated; vendor/ = third-party; README =
// low-priority docs). Project-specific patterns belong in user config, not
// this global table.
//
// First match wins; order matters only when two patterns could both fire on
// the same path (vendor/ vs. node_modules/ are mutually exclusive in practice).
// Invariant: every entry here must be reachable. Files rejected by
// ast.ShouldSkip never enter the confidence pipeline, so adding (e.g.)
// `package-lock.json` here is dead code — those names belong in
// blocklist.go's lockfileNames map. `TestPathPatterns_AllReachable`
// pins this invariant; if you add a new entry that ShouldSkip would
// reject, that test will fail with the redundant pattern named.
var pathPatterns = []pathPattern{
	// Terraform provider lockfile — the only lockfile-shaped file that's
	// NOT in blocklist.go (HCL is a real source language, the basename
	// disambiguates it from regular .hcl). Kept here so its symbols rank
	// below first-party HCL.
	{Glob: ".terraform.lock.hcl", Penalty: -0.40, Reason: "terraform provider lockfile"},
	// Vendored third-party — real, but not first-party signal.
	{Glob: "vendor", Penalty: -0.30, IsDir: true, Reason: "vendored third-party code"},
	{Glob: "node_modules", Penalty: -0.30, IsDir: true, Reason: "node third-party"},
	{Glob: "third_party", Penalty: -0.30, IsDir: true, Reason: "third-party deps"},
	// Build artefacts.
	{Glob: "dist", Penalty: -0.20, IsDir: true, Reason: "build output"},
	{Glob: "build", Penalty: -0.20, IsDir: true, Reason: "build output"},
	{Glob: "out", Penalty: -0.20, IsDir: true, Reason: "build output"},
	// Low-priority docs.
	{Glob: "README.md", Penalty: -0.20, Reason: "project README"},
	{Glob: "CHANGELOG.md", Penalty: -0.20, Reason: "changelog"},
	{Glob: "CONTRIBUTING.md", Penalty: -0.20, Reason: "contributing guide"},
}

// identRE matches a clean programming-style identifier — letter/underscore
// followed by letters, digits, or underscores. A name that matches gets a
// small bonus; a name that's empty or whitespace gets a penalty.
var identRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// generatedHeadLimit caps how much of the file head we scan for the
// `// Code generated` marker. The marker lives in the first 5 lines of any
// reasonable generator output; 1KB is generous and bounds the cost.
const generatedHeadLimit = 1024

// pathPattern is a single path-shape rule. Phase 2 will populate the global
// list; defining the type now lets composition tests construct synthetic
// signal sets.
type pathPattern struct {
	// Glob matches against either the file basename (default) or a directory
	// component (when IsDir is true). Behaves like filepath.Match.
	Glob    string
	Penalty float64
	IsDir   bool
	// Reason is surfaced on diagnostics so a snapshot diff that adds a penalty
	// can be reviewed by intent.
	Reason string
}

// computeSignals builds the Signals struct for one symbol.
//
// Signals computable from (sym, relPath, source) alone: kindBaseline lookup,
// pathPatterns iteration, identifier-shape bonus, and generated-marker
// penalty. The earlier-design BreadthPenalty / LeafPenalty signals were
// removed in #119 — they would have needed structural info (parent fan-out,
// scalar-vs-mapping) wired through every extractor for marginal benefit
// given the current calibration; the existing four signals carry the
// quality gradient on real corpora.
//
// Pure function: same inputs always produce the same Signals. The caller
// chains Compose() to get the final score. Identifier-bonus and generated-
// marker scans are bounded (generatedHeadLimit clamps the source scan), so
// per-symbol cost stays roughly constant regardless of file size.
func computeSignals(sym *ExtractedSymbol, baseExtractor float64, relPath string, source []byte) Signals {
	s := Signals{BaseExtractor: baseExtractor}

	if k, ok := kindBaseline[sym.Kind]; ok {
		s.KindBaseline = k
	} else {
		s.KindBaseline = baseExtractor
	}

	// First-match path penalty.
	for _, p := range pathPatterns {
		if penalty := matchPathPattern(relPath, p); penalty != 0 {
			s.PathPenalty = penalty
			break
		}
	}

	// Identifier-shape bonus / penalty. Empty or whitespace-only names are a
	// regex-extractor failure mode (capture group matched but extracted blank).
	switch {
	case identRE.MatchString(sym.Name):
		s.IdentBonus = 0.05
	case strings.TrimSpace(sym.Name) == "":
		s.IdentBonus = -0.10
	}

	// Generated-file penalty fires once for every symbol in a generated file.
	if isGeneratedFile(source) {
		s.GeneratedPen = -0.30
	}

	return s
}

// matchPathPattern returns the pattern's Penalty when relPath matches, else 0.
//
// Two match modes:
//   - IsDir: returns Penalty if any directory component of relPath equals Glob
//     exactly (e.g. "vendor" matches "vendor/x/y.go" but not "myvendor/x.go")
//   - default: returns Penalty if filepath.Base(relPath) matches Glob via
//     filepath.Match (POSIX glob; supports `*`, `?`, `[...]`)
func matchPathPattern(relPath string, p pathPattern) float64 {
	if p.IsDir {
		if pathContainsDir(relPath, p.Glob) {
			return p.Penalty
		}
		return 0
	}
	matched, err := filepath.Match(p.Glob, filepath.Base(relPath))
	if err == nil && matched {
		return p.Penalty
	}
	return 0
}

// pathContainsDir checks whether dir is exactly one of the slash-separated
// components of relPath. Path separators are normalised to `/` so Windows
// callers work the same as POSIX.
func pathContainsDir(relPath, dir string) bool {
	for _, part := range strings.Split(filepath.ToSlash(relPath), "/") {
		if part == dir {
			return true
		}
	}
	return false
}

// isGeneratedFile inspects the head of source for the canonical
// `// Code generated` marker emitted by go generate, protoc, swagger, etc.
// Bounded to generatedHeadLimit bytes so cost is fixed regardless of file size.
func isGeneratedFile(source []byte) bool {
	head := source
	if len(head) > generatedHeadLimit {
		head = head[:generatedHeadLimit]
	}
	if len(head) == 0 {
		return false
	}
	// strings.Contains on the byte slice is allocation-free in Go's stdlib
	// because the string conversion of a byte slice is the same memory.
	return strings.Contains(string(head), "Code generated")
}
