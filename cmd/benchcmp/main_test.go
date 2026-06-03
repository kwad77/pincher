// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeBench dumps a synthetic baseline file into a tempdir for testing.
func writeBench(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

const fakeBaseline = `goos: linux
goarch: amd64
pkg: github.com/kwad77/pincher/internal/index
cpu: Intel(R) Xeon(R) CPU
BenchmarkA-4   100   1000 ns/op   200 B/op   10 allocs/op
BenchmarkB-4   200   2000 ns/op   400 B/op   20 allocs/op
PASS
`

// TestParseFile_StripsGomaxprocs proves the -N suffix matching is
// independent of the runner's GOMAXPROCS. A baseline captured on a 4-core
// runner must still align with an actual captured on an 8-core runner.
func TestParseFile_StripsGomaxprocs(t *testing.T) {
	dir := t.TempDir()
	p := writeBench(t, dir, "b.txt", fakeBaseline)

	got, err := parseFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["BenchmarkA"]; !ok {
		t.Errorf("expected BenchmarkA (sans -N), got keys: %v", keys(got))
	}
	if _, ok := got["BenchmarkB"]; !ok {
		t.Errorf("expected BenchmarkB (sans -N), got keys: %v", keys(got))
	}
}

// TestPercentDelta covers the math gate for regression detection.
func TestPercentDelta(t *testing.T) {
	cases := []struct {
		baseline, actual, want float64
	}{
		{100, 120, 0.20},
		{100, 100, 0.0},
		{100, 80, -0.20},
		{0, 50, 0}, // div-by-zero guard
	}
	for _, c := range cases {
		got := percentDelta(c.baseline, c.actual)
		if got != c.want {
			t.Errorf("percentDelta(%v, %v) = %v, want %v",
				c.baseline, c.actual, got, c.want)
		}
	}
}

// TestRegression_NsOver20Percent — the negative-of-fix gate from the issue:
// "A change that adds 50ms to BenchmarkHandleSymbol MUST fail CI."
// Encoded here as: a 50% ns regression must produce a non-zero exit
// signal from the comparison logic. We test the math gate directly so
// CI behaviour is provable without invoking os.Exit.
func TestRegression_NsOver20Percent(t *testing.T) {
	got := percentDelta(1000, 1500) // +50%
	if got <= defaultNsThreshold {
		t.Errorf("delta %v should exceed defaultNsThreshold %v "+
			"— a 50%% regression must fail CI", got, defaultNsThreshold)
	}
}

// TestRegression_AllocsOver30Percent — the alloc-count gate.
func TestRegression_AllocsOver30Percent(t *testing.T) {
	got := percentDelta(10, 14) // +40%
	if got <= defaultAllocsThreshold {
		t.Errorf("delta %v should exceed defaultAllocsThreshold %v "+
			"— a 40%% alloc regression must fail CI", got, defaultAllocsThreshold)
	}
}

// TestNoiseFloor_15PercentDoesNotFail — the suppression-direction gate.
// 15% wall-clock noise on a fast op MUST NOT trip the regression flag.
// This pins the noise-floor design choice from the issue: "We accept
// noise floor of ~20% on wall-clock to absorb runner variation."
func TestNoiseFloor_15PercentDoesNotFail(t *testing.T) {
	got := percentDelta(1000, 1150) // +15%
	if got > defaultNsThreshold {
		t.Errorf("delta %v should NOT exceed defaultNsThreshold %v "+
			"— 15%% must be within noise floor", got, defaultNsThreshold)
	}
}

// TestCompare_NoRegression — happy path: identical sets produce zero
// regressions, zero missing entries, and a populated table.
func TestCompare_NoRegression(t *testing.T) {
	set := map[string]benchResult{
		"BenchmarkA": {NsPerOp: 1000, AllocsPerOp: 10},
		"BenchmarkB": {NsPerOp: 2000, AllocsPerOp: 20},
	}
	var buf bytes.Buffer
	regressions, missingActual, missingBaseline := compare(set, set, &buf, defaultNsThreshold, defaultAllocsThreshold, nil)
	if regressions != 0 || missingActual != 0 || missingBaseline != 0 {
		t.Errorf("identical sets: regressions=%d, missingActual=%d, missingBaseline=%d, want all 0",
			regressions, missingActual, missingBaseline)
	}
	if !strings.Contains(buf.String(), "BenchmarkA") {
		t.Errorf("expected output to include BenchmarkA row, got:\n%s", buf.String())
	}
}

// TestCompare_NsRegressionFlagged — the wall-clock gate fires when ns/op
// jumps beyond the 20% threshold.
func TestCompare_NsRegressionFlagged(t *testing.T) {
	baseline := map[string]benchResult{
		"BenchmarkSlow": {NsPerOp: 1000, AllocsPerOp: 10},
	}
	actual := map[string]benchResult{
		"BenchmarkSlow": {NsPerOp: 1500, AllocsPerOp: 10}, // +50%
	}
	var buf bytes.Buffer
	regressions, _, _ := compare(baseline, actual, &buf, defaultNsThreshold, defaultAllocsThreshold, nil)
	if regressions != 1 {
		t.Errorf("expected 1 ns regression, got %d", regressions)
	}
	if !strings.Contains(buf.String(), "[NS-REGRESSION]") {
		t.Errorf("expected NS-REGRESSION flag in output, got:\n%s", buf.String())
	}
}

// TestCompare_AllocsRegressionFlagged — the alloc-count gate fires
// independently of ns/op.
func TestCompare_AllocsRegressionFlagged(t *testing.T) {
	baseline := map[string]benchResult{
		"BenchmarkAlloc": {NsPerOp: 1000, AllocsPerOp: 10},
	}
	actual := map[string]benchResult{
		"BenchmarkAlloc": {NsPerOp: 1000, AllocsPerOp: 14}, // +40%
	}
	var buf bytes.Buffer
	regressions, _, _ := compare(baseline, actual, &buf, defaultNsThreshold, defaultAllocsThreshold, nil)
	if regressions != 1 {
		t.Errorf("expected 1 allocs regression, got %d", regressions)
	}
	if !strings.Contains(buf.String(), "[ALLOCS-REGRESSION]") {
		t.Errorf("expected ALLOCS-REGRESSION flag in output, got:\n%s", buf.String())
	}
}

// TestCompare_MissingInActual — a benchmark that disappeared between
// baseline and actual must be surfaced.
func TestCompare_MissingInActual(t *testing.T) {
	baseline := map[string]benchResult{
		"BenchmarkGone": {NsPerOp: 1000, AllocsPerOp: 10},
	}
	actual := map[string]benchResult{}
	var buf bytes.Buffer
	_, missingActual, _ := compare(baseline, actual, &buf, defaultNsThreshold, defaultAllocsThreshold, nil)
	if missingActual != 1 {
		t.Errorf("expected 1 missing-in-actual, got %d", missingActual)
	}
	if !strings.Contains(buf.String(), "MISSING IN ACTUAL") {
		t.Errorf("expected MISSING IN ACTUAL line, got:\n%s", buf.String())
	}
}

// TestCompare_NewWithoutBaseline — a benchmark in actual that has no
// baseline forces an explicit baseline-update step.
func TestCompare_NewWithoutBaseline(t *testing.T) {
	baseline := map[string]benchResult{}
	actual := map[string]benchResult{
		"BenchmarkNew": {NsPerOp: 1000, AllocsPerOp: 10},
	}
	var buf bytes.Buffer
	_, _, missingBaseline := compare(baseline, actual, &buf, defaultNsThreshold, defaultAllocsThreshold, nil)
	if missingBaseline != 1 {
		t.Errorf("expected 1 new-without-baseline, got %d", missingBaseline)
	}
	if !strings.Contains(buf.String(), "NEW IN ACTUAL") {
		t.Errorf("expected NEW IN ACTUAL line, got:\n%s", buf.String())
	}
}

// TestCompare_CustomThresholds pins that the gate respects caller-supplied
// thresholds rather than always using the 20%/30% defaults. This is the
// path CI takes — the wider thresholds absorb dev-vs-CI hardware mismatch
// (the committed baselines are dev-machine numbers; runner cores differ).
func TestCompare_CustomThresholds(t *testing.T) {
	baseline := map[string]benchResult{
		"BenchmarkA": {NsPerOp: 1000, AllocsPerOp: 10},
	}
	// +25% ns, +25% allocs — would fail at default 20%/30% for ns, but
	// pass under the wider 0.40/0.40 thresholds CI uses.
	actual := map[string]benchResult{
		"BenchmarkA": {NsPerOp: 1250, AllocsPerOp: 12},
	}

	// Default thresholds: ns flagged.
	var bufDefault bytes.Buffer
	regsDefault, _, _ := compare(baseline, actual, &bufDefault, defaultNsThreshold, defaultAllocsThreshold, nil)
	if regsDefault != 1 {
		t.Errorf("default thresholds: regressions=%d, want 1", regsDefault)
	}

	// Wider thresholds: nothing flagged.
	var bufWide bytes.Buffer
	regsWide, _, _ := compare(baseline, actual, &bufWide, 0.40, 0.40, nil)
	if regsWide != 0 {
		t.Errorf("wider thresholds: regressions=%d, want 0", regsWide)
	}
	if strings.Contains(bufWide.String(), "[NS-REGRESSION]") {
		t.Errorf("wider thresholds: should not flag NS-REGRESSION, got:\n%s", bufWide.String())
	}
}

// TestCompare_ExcludedBenchmarkDoesNotCountAsRegression pins the
// surgical-skip semantics: a benchmark in the `excluded` set is still
// printed (with [EXCLUDED] marker) but doesn't increment the
// regressions counter, even when its delta would normally fail. Used
// for benchmarks with documented high CV that would flap the gate
// (per testdata/bench/variance-ci-2026-05-09.md).
func TestCompare_ExcludedBenchmarkDoesNotCountAsRegression(t *testing.T) {
	baseline := map[string]benchResult{
		"BenchmarkA": {NsPerOp: 1000, AllocsPerOp: 10},
		"BenchmarkB": {NsPerOp: 1000, AllocsPerOp: 10},
	}
	actual := map[string]benchResult{
		"BenchmarkA": {NsPerOp: 1500, AllocsPerOp: 10}, // +50% — would normally fail
		"BenchmarkB": {NsPerOp: 1500, AllocsPerOp: 10}, // +50% — would normally fail
	}
	excluded := map[string]bool{"BenchmarkA": true}

	var buf bytes.Buffer
	regs, _, _ := compare(baseline, actual, &buf, defaultNsThreshold, defaultAllocsThreshold, excluded)
	if regs != 1 {
		t.Errorf("expected 1 regression (BenchmarkB only; BenchmarkA excluded), got %d", regs)
	}
	out := buf.String()
	if !strings.Contains(out, "[EXCLUDED]") {
		t.Errorf("expected [EXCLUDED] marker in output for BenchmarkA, got:\n%s", out)
	}
	if !strings.Contains(out, "BenchmarkA") {
		t.Errorf("excluded benchmark should still appear in the output table, got:\n%s", out)
	}
	if !strings.Contains(out, "[NS-REGRESSION]") {
		t.Errorf("non-excluded BenchmarkB should still flag NS-REGRESSION, got:\n%s", out)
	}
}

func keys(m map[string]benchResult) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestStripGomaxprocs_Cases(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		// Standard go-test bench output suffix.
		{"BenchmarkFoo-32", "BenchmarkFoo"},
		{"BenchmarkSearch_BM25-8", "BenchmarkSearch_BM25"},
		// No suffix — return as-is.
		{"BenchmarkFoo", "BenchmarkFoo"},
		// Trailing dash but non-numeric — keep, this isn't a GOMAXPROCS suffix.
		{"BenchmarkFoo-bar", "BenchmarkFoo-bar"},
		// Empty.
		{"", ""},
		// Just the suffix.
		{"-32", ""},
	}
	for _, c := range cases {
		if got := stripGomaxprocs(c.in); got != c.want {
			t.Errorf("stripGomaxprocs(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
