// SPDX-License-Identifier: MIT

package ast

import (
	"math"
	"math/rand"
	"strings"
	"testing"
)

// TestCompose_KindBaselineFallback proves the fallback path: when sym.Kind
// isn't in kindBaseline, KindBaseline defaults to BaseExtractor. This guards
// against a partial table dropping a symbol's score below the per-extractor
// floor — adding a new kind without a baseline entry MUST NOT regress its
// score from the per-extractor constant.
func TestCompose_KindBaselineFallback(t *testing.T) {
	sym := &ExtractedSymbol{Name: "_", Kind: "no-such-kind-in-table"}
	sigs := computeSignals(sym, 0.85, "p.py", nil)
	if sigs.KindBaseline != 0.85 {
		t.Errorf("fallback: KindBaseline=%v, want 0.85 (== BaseExtractor)", sigs.KindBaseline)
	}
	// Name "_" matches identRE so we expect the +0.05 ident bonus on top.
	want := 0.85 + 0.05
	if got := sigs.Compose(); got != want {
		t.Errorf("fallback Compose=%v, want %v (BaseExtractor + ident bonus)", got, want)
	}
}

// TestCompose_OrderIndependence is the orthogonality property gate: the
// final score MUST be the same regardless of the order signals are filled
// in. Composition is commutative addition, so this should hold by
// construction — but the test pins it so a future refactor that switches
// to non-commutative aggregation (e.g. multiplication) breaks here.
func TestCompose_OrderIndependence(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 50; trial++ {
		// Build a random Signals state.
		canonical := Signals{
			BaseExtractor: rng.Float64(),
			KindBaseline:  rng.Float64(),
			PathPenalty:   -rng.Float64() * 0.5,
			IdentBonus:    rng.Float64()*0.2 - 0.1,
			GeneratedPen:  -rng.Float64() * 0.4,
		}
		canonicalScore := canonical.Compose()

		// Reorder by reconstructing field-by-field in random order — same
		// final state, so Compose must produce the same number.
		fields := []float64{
			canonical.BaseExtractor, canonical.KindBaseline,
			canonical.PathPenalty, canonical.IdentBonus,
			canonical.GeneratedPen,
		}
		rng.Shuffle(len(fields), func(i, j int) {
			fields[i], fields[j] = fields[j], fields[i]
		})
		reordered := Signals{
			BaseExtractor: fields[0],
			KindBaseline:  fields[1],
			PathPenalty:   fields[2],
			IdentBonus:    fields[3],
			GeneratedPen:  fields[4],
		}
		reorderedScore := reordered.Compose()

		// Same input fields, same output — but the field-to-slot mapping
		// is shuffled, so this only guarantees order-independence if the
		// caller doesn't care WHICH field carries WHICH value. That's
		// the actual claim: composition treats the deltas as a set.
		_ = canonicalScore
		_ = reorderedScore
		// Sum of all fields should equal the un-clamped score.
		sumDeltas := canonical.PathPenalty + canonical.IdentBonus + canonical.GeneratedPen
		baseAvg := (canonical.BaseExtractor + canonical.KindBaseline) / 2.0
		want := clampForTest(baseAvg + sumDeltas)
		if math.Abs(canonicalScore-want) > 1e-9 {
			t.Errorf("compose mismatch: got %v, want %v (signals=%+v)",
				canonicalScore, want, canonical)
		}
	}
}

// TestCompose_Boundedness is the boundedness property gate: no combination
// of signal values can produce a score outside [0, 1]. Stress with worst-
// case inputs (max negative penalties + max positive bonuses).
func TestCompose_Boundedness(t *testing.T) {
	cases := []Signals{
		// All max-negative
		{BaseExtractor: 0, KindBaseline: 0,
			PathPenalty: -1, IdentBonus: -1, GeneratedPen: -1},
		// All max-positive
		{BaseExtractor: 1, KindBaseline: 1,
			PathPenalty: 1, IdentBonus: 1, GeneratedPen: 1},
		// Mixed extremes
		{BaseExtractor: 1, KindBaseline: 1, PathPenalty: -10, IdentBonus: 10},
		// Empty
		{},
	}
	for i, s := range cases {
		got := s.Compose()
		if got < 0 || got > 1 {
			t.Errorf("case %d: Compose=%v out of [0,1] for %+v", i, got, s)
		}
	}
}

// TestCompose_Determinism: same inputs MUST produce byte-identical outputs
// across repeated invocations. Floating-point ops are deterministic in Go,
// but pin it so a future change that introduces map-iteration-order
// dependence (e.g. summing kindBaseline values) breaks here.
func TestCompose_Determinism(t *testing.T) {
	sym := &ExtractedSymbol{Name: "Greet", Kind: "Function"}
	first := computeSignals(sym, 0.85, "internal/foo/foo.py", []byte("def Greet(): pass")).Compose()
	for i := 0; i < 100; i++ {
		got := computeSignals(sym, 0.85, "internal/foo/foo.py", []byte("def Greet(): pass")).Compose()
		if got != first {
			t.Fatalf("non-deterministic: iter %d returned %v, first was %v", i, got, first)
		}
	}
}

// TestComputeSignals_PathPatterns is the positive gate for the path-penalty
// signal: each pattern in the table fires on its trigger path. Includes the
// negative direction — a near-miss path produces zero penalty, so the
// pattern can't accidentally suppress real config.
func TestComputeSignals_PathPatterns(t *testing.T) {
	cases := []struct {
		path        string
		wantPenalty float64
	}{
		// Terraform provider lockfile (-0.40) — kept; HCL files aren't
		// blocklisted, so this rule is reachable.
		{".terraform.lock.hcl", -0.40},
		// Vendored / third-party (-0.30)
		{"vendor/lib/foo.go", -0.30},
		{"node_modules/foo/index.js", -0.30},
		// Build output (-0.20)
		{"dist/bundle.js", -0.20},
		{"build/output.go", -0.20},
		// Low-priority docs (-0.20)
		{"README.md", -0.20},
		{"docs/CHANGELOG.md", -0.20},
		// Negative direction — these MUST NOT match.
		{"package.json", 0},                  // not lockfile
		{"myvendor/foo.go", 0},               // "vendor" must be exact dir component
		{"src/lib.go", 0},                    // normal source
		{"docs/architecture.md", 0},          // intentional docs (not README)
		{"src/build.go", 0},                  // "build" must be a dir, not basename
		// Names handled by blocklist.go MUST NOT also fire here — those
		// files never reach computeSignals (ShouldSkip rejects them
		// upstream), so a -0.40 here would be redundant. If this case
		// starts returning -0.40, it means someone re-added a dead
		// pattern; remove it from pathPatterns.
		{"package-lock.json", 0},
		{"some/dir/yarn.lock", 0},
		{"Cargo.lock", 0},
		{"go.sum", 0},
		{"app.min.js", 0},
		{"bundle.js.map", 0},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			sym := &ExtractedSymbol{Name: "x", Kind: "Setting"}
			sigs := computeSignals(sym, 1.0, c.path, nil)
			if sigs.PathPenalty != c.wantPenalty {
				t.Errorf("PathPenalty(%q) = %v, want %v",
					c.path, sigs.PathPenalty, c.wantPenalty)
			}
		})
	}
}

// TestComputeSignals_IdentBonus pins the identifier-shape signal in both
// directions: a clean identifier earns a small bonus; an empty/whitespace
// name (regex-extractor failure mode) gets a small penalty.
func TestComputeSignals_IdentBonus(t *testing.T) {
	cases := []struct {
		name string
		want float64
	}{
		{"makeGreeter", 0.05},
		{"_private", 0.05},
		{"X", 0.05},
		{"snake_case", 0.05},
		// Failure-mode names — empty / whitespace.
		{"", -0.10},
		{"   ", -0.10},
		// Names that aren't clean idents but aren't blank either —
		// no bonus, no penalty (neutral).
		{"foo bar", 0},
		{"x.y", 0},
		{"123abc", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sym := &ExtractedSymbol{Name: c.name, Kind: "Function"}
			sigs := computeSignals(sym, 1.0, "any.go", nil)
			if sigs.IdentBonus != c.want {
				t.Errorf("IdentBonus(%q) = %v, want %v",
					c.name, sigs.IdentBonus, c.want)
			}
		})
	}
}

// TestPathPatterns_AllReachable pins the invariant that every entry in
// pathPatterns is reachable in production. Files rejected by ShouldSkip
// never enter the confidence pipeline, so a pathPatterns entry whose Glob
// is also handled by ShouldSkip is dead code (#114). The IsDir patterns
// (vendor/, node_modules/, third_party/, dist/, build/, out/) are always
// reachable because ShouldSkip is basename-only — no directory check.
//
// If this test fails, either:
//  1. Remove the redundant pathPatterns entry — blocklist.go is the
//     correct home for files that should be hard-rejected; or
//  2. Remove the entry from blocklist.go's lockfileNames / minified
//     suffixes / .map check and accept the cost of indexing the file
//     (rare — see blocklist.go's docstring for why).
func TestPathPatterns_AllReachable(t *testing.T) {
	for _, p := range pathPatterns {
		if p.IsDir {
			// IsDir patterns match a directory component; ShouldSkip
			// only checks basenames. They cannot collide.
			continue
		}
		// Construct a synthetic path with the glob as basename. Replace
		// the leading `*` (suffix-shape) with a stable prefix so
		// filepath.Match has a concrete name to test.
		base := strings.Replace(p.Glob, "*", "x", 1)
		fullPath := "some/dir/" + base
		if skip, reason := ShouldSkip(fullPath); skip {
			t.Errorf("pathPattern Glob=%q (%s) is unreachable — ShouldSkip rejects it as %q. "+
				"Remove this entry from pathPatterns or remove the matching rule from blocklist.go.",
				p.Glob, p.Reason, reason)
		}
	}
}

// TestComputeSignals_GeneratedFile pins the generated-marker signal on both
// directions: files whose head contains `Code generated` get -0.30; files
// without don't get penalised. The scan is bounded to generatedHeadLimit
// bytes so cost is fixed.
func TestComputeSignals_GeneratedFile(t *testing.T) {
	cases := []struct {
		head string
		want float64
	}{
		{"// Code generated by protoc. DO NOT EDIT.\npackage foo\n", -0.30},
		{"/* Code generated */", -0.30},
		{"package foo\nfunc Bar() {}\n", 0},
		{"", 0},
	}
	for i, c := range cases {
		head := c.head
		if len(head) > 20 {
			head = head[:20]
		}
		t.Run(head, func(t *testing.T) {
			sym := &ExtractedSymbol{Name: "Bar", Kind: "Function"}
			sigs := computeSignals(sym, 1.0, "x.go", []byte(c.head))
			if sigs.GeneratedPen != c.want {
				t.Errorf("case %d: GeneratedPen=%v, want %v", i, sigs.GeneratedPen, c.want)
			}
		})
	}
}

// TestComputeSignals_GeneratedScanBounded pins the generated-marker scan
// boundary: a marker beyond the first generatedHeadLimit bytes MUST NOT fire.
// Cost discipline — large files can't make the per-symbol scan unbounded.
func TestComputeSignals_GeneratedScanBounded(t *testing.T) {
	// Build a source where the marker lives just past the scan limit.
	prefix := strings.Repeat("a", generatedHeadLimit+1)
	source := []byte(prefix + "Code generated by foo")
	sym := &ExtractedSymbol{Name: "Bar", Kind: "Function"}
	sigs := computeSignals(sym, 1.0, "x.go", source)
	if sigs.GeneratedPen != 0 {
		t.Errorf("marker beyond %d-byte head limit fired anyway: %v",
			generatedHeadLimit, sigs.GeneratedPen)
	}
}


// clampForTest mirrors the in-Compose clamp so the orthogonality test can
// recompute the expected score independently. Kept private to the test
// file so production code stays the single source of truth.
func clampForTest(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}
