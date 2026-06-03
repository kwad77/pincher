// SPDX-License-Identifier: MIT

package server

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestMaybeFormatCelebration_DefaultOff_Silent pins #863: celebrations
// are opt-in. Without PINCHER_CELEBRATIONS=1, a threshold crossing
// produces no line — the default path stays quiet.
func TestMaybeFormatCelebration_DefaultOff_Silent(t *testing.T) {
	srv, _, _ := newTestServer(t)
	atomic.StoreInt64(&srv.statsTokensSaved, 150_000)

	if got := srv.maybeFormatCelebration(); got != "" {
		t.Errorf("celebrations must be silent without PINCHER_CELEBRATIONS=1; got %q", got)
	}
}

// TestMaybeFormatCelebration_FiresOnThresholdCrossing pins #494: with
// PINCHER_CELEBRATIONS=1, when the in-flight session pushes cumulative
// tokens_saved past a tier, maybeFormatCelebration produces a one-line
// signal — and only once per installation.
func TestMaybeFormatCelebration_FiresOnThresholdCrossing(t *testing.T) {
	t.Setenv("PINCHER_CELEBRATIONS", "1")
	srv, _, _ := newTestServer(t)
	atomic.StoreInt64(&srv.statsTokensSaved, 150_000)

	got := srv.maybeFormatCelebration()
	if got == "" {
		t.Fatalf("expected celebration line at 150k cumulative; got empty")
	}
	if !strings.Contains(got, "100,000") {
		t.Errorf("expected threshold marker '100,000' in celebration; got %q", got)
	}
	if !strings.Contains(got, "🎯") {
		t.Errorf("expected 🎯 emoji marker in celebration; got %q", got)
	}
	if strings.Contains(got, "$") {
		t.Errorf("celebration must not contain $-figures (#476); got %q", got)
	}

	// Second call at same cumulative — already fired, should be silent.
	if again := srv.maybeFormatCelebration(); again != "" {
		t.Errorf("expected silence on already-fired threshold; got %q", again)
	}
}

func TestMaybeFormatCelebration_BelowFirstTier_Silent(t *testing.T) {
	t.Setenv("PINCHER_CELEBRATIONS", "1")
	srv, _, _ := newTestServer(t)
	atomic.StoreInt64(&srv.statsTokensSaved, 50_000)

	if got := srv.maybeFormatCelebration(); got != "" {
		t.Fatalf("expected silence below 100k; got %q", got)
	}
}

func TestMaybeFormatCelebration_PersistedCounted(t *testing.T) {
	t.Setenv("PINCHER_CELEBRATIONS", "1")
	srv, store, _ := newTestServer(t)

	// Persisted session contributes 80k; in-flight contributes 30k.
	// Together = 110k, crosses the 100k tier.
	if err := store.RecordSession("prior", time.Unix(1, 0), 1, 10, 80_000, 0, "", 0, ""); err != nil {
		t.Fatalf("RecordSession: %v", err)
	}
	atomic.StoreInt64(&srv.statsTokensSaved, 30_000)

	got := srv.maybeFormatCelebration()
	if got == "" {
		t.Fatalf("expected fire when persisted+inflight crosses threshold; got empty")
	}
	if !strings.Contains(got, "100,000") {
		t.Errorf("want '100,000' marker; got %q", got)
	}
}

func TestFormatCelebration_PureShape(t *testing.T) {
	t.Parallel()
	got := formatCelebration(1_000_000, 1_234_567)
	if !strings.Contains(got, "1,234,567") {
		t.Errorf("want cumulative '1,234,567'; got %q", got)
	}
	if !strings.Contains(got, "1,000,000") {
		t.Errorf("want threshold '1,000,000'; got %q", got)
	}
}
