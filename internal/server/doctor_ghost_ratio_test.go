// SPDX-License-Identifier: MIT

package server

import (
	"strings"
	"testing"
)

// #1010: extends the #1009 ghost-advisory to flag projects with
// vanishingly few edges (not just zero). Some ghost-extracted projects
// leak a handful of edges before the resolver phase dies; the strict
// `Edges == 0` gate misses them. Empirical anchor: warp_rc indexed at
// 1.4M symbols / 247 edges (ratio 0.000175) — clearly ghost but the
// strict gate let it through.

func TestGhostProjectAdvisory_LowRatioFlagged(t *testing.T) {
	t.Parallel()
	projects := []doctorProjectSummary{
		// 1500 syms / 1 edge = ratio 0.000667, well below 0.001 floor.
		{Name: "leaky-ghost", Symbols: 1500, Files: 40, Edges: 1},
	}
	got := ghostProjectAdvisory(projects)
	if got == "" {
		t.Fatal("expected advisory for low-ratio ghost; got empty")
	}
	if !strings.Contains(got, "leaky-ghost") {
		t.Errorf("advisory must name the ghost project; got %q", got)
	}
	if !strings.Contains(got, "ratio") {
		t.Errorf("advisory must surface the ratio for low-ratio ghosts; got %q", got)
	}
}

func TestGhostProjectAdvisory_HealthyRatioNotFlagged(t *testing.T) {
	t.Parallel()
	// 1500 syms / 100 edges = ratio 0.0667 — comfortably above 0.001 floor.
	projects := []doctorProjectSummary{
		{Name: "healthy-small-edges", Symbols: 1500, Files: 50, Edges: 100},
	}
	if got := ghostProjectAdvisory(projects); got != "" {
		t.Errorf("expected no advisory for healthy ratio; got %q", got)
	}
}

func TestGhostProjectAdvisory_BothZeroAndRatioGhostsReported(t *testing.T) {
	t.Parallel()
	projects := []doctorProjectSummary{
		{Name: "zero-ghost", Symbols: 5000, Files: 100, Edges: 0},
		{Name: "ratio-ghost", Symbols: 4000, Files: 80, Edges: 2}, // ratio 0.0005
		{Name: "healthy", Symbols: 3000, Files: 60, Edges: 900},
	}
	got := ghostProjectAdvisory(projects)
	if !strings.Contains(got, "zero-ghost") {
		t.Errorf("must report 0-edge ghost; got %q", got)
	}
	if !strings.Contains(got, "ratio-ghost") {
		t.Errorf("must report low-ratio ghost; got %q", got)
	}
	if strings.Contains(got, "\"healthy\"") {
		t.Errorf("must not flag healthy project; got %q", got)
	}
	// Zero-edge entry uses "0 edges" form; ratio entry includes "ratio".
	if !strings.Contains(got, "0 edges") {
		t.Errorf("zero-edge formatting missing; got %q", got)
	}
	if !strings.Contains(got, "ratio 0.000") {
		t.Errorf("ratio formatting missing for ratio ghost; got %q", got)
	}
}

func TestGhostProjectAdvisory_RatioFloorIsBoundary(t *testing.T) {
	t.Parallel()
	// Ratio exactly at 0.001 should NOT trip (strict less-than gate).
	// 1000 symbols / 1 edge = 0.001 exact.
	at := []doctorProjectSummary{{Name: "boundary", Symbols: 1000, Files: 30, Edges: 1}}
	if got := ghostProjectAdvisory(at); got != "" {
		t.Errorf("ratio == 0.001 must not trip; got %q", got)
	}
	// Just below — 1001 symbols / 1 edge — should trip.
	below := []doctorProjectSummary{{Name: "below-floor", Symbols: 1001, Files: 30, Edges: 1}}
	if got := ghostProjectAdvisory(below); got == "" {
		t.Error("ratio just below 0.001 must trip; got empty")
	}
}

func TestGhostProjectAdvisory_SmallProjectIgnoredEvenWithBadRatio(t *testing.T) {
	t.Parallel()
	// 800 symbols / 0 edges — bad ratio, but below 1000-symbol threshold.
	// Pure-docs/config repos can land here legitimately.
	projects := []doctorProjectSummary{
		{Name: "tiny-with-no-edges", Symbols: 800, Files: 40, Edges: 0},
	}
	if got := ghostProjectAdvisory(projects); got != "" {
		t.Errorf("expected no advisory below symbol threshold; got %q", got)
	}
}
