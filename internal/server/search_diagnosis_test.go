// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// Tests for verifyEmptySearchCause (#246). The verifier must attribute
// zero-result causes by *running relaxed queries*, not by guessing from
// input shape — the static diagnoseEmptySearch path is preserved as a
// fallback for when no relaxation helps.

// fakeRelaxer returns a deterministic count for any (query,kind,lang,corpus).
// Tests build a map keyed by the relaxation params and the verifier reads
// the count from there. Mirrors what SearchSymbolsByCorpus does without
// needing a real DB.
type fakeRelaxer struct {
	counts map[string]int
}

func (f *fakeRelaxer) at(q, k, l, c string) int { return f.counts[q+"|"+k+"|"+l+"|"+c] }

func (f *fakeRelaxer) relax() emptySearchRelaxer {
	return func(q, k, l, c string) (int, error) {
		return f.at(q, k, l, c), nil
	}
}

// The bug repro: searching `registerTools kind=Function` returns 0
// because registerTools is a Method. The verifier must report that
// dropping the kind filter surfaces 1 result, NOT blame min_confidence.
func TestVerifyEmptySearchCause_KindFilterIsActualCause(t *testing.T) {
	t.Parallel()
	relaxer := &fakeRelaxer{counts: map[string]int{
		"registerTools||Go|": 1, // dropping kind surfaces 1 result
	}}
	cause, steps, ok := verifyEmptySearchCause(
		"registerTools", "Function", "Go", "", 0, 0, relaxer.relax(),
	)
	if !ok {
		t.Fatal("verifier returned ok=false; want a verified cause")
	}
	if !strings.Contains(cause, `kind="Function"`) {
		t.Errorf("cause must name the kind filter as the culprit, got: %q", cause)
	}
	if !strings.Contains(cause, "drop the kind filter") {
		t.Errorf("cause must include actionable suggestion to drop kind, got: %q", cause)
	}
	if len(steps) == 0 {
		t.Fatal("steps empty; want at least one recovery step")
	}
	if !strings.Contains(steps[0]["args"], `"query":"registerTools"`) || strings.Contains(steps[0]["args"], "kind") {
		t.Errorf("first step must drop kind from args, got: %q", steps[0]["args"])
	}
}

// min_confidence is verifiable without an extra query — if rawCount > 0
// but post-filter == 0, the threshold is provably the cause.
func TestVerifyEmptySearchCause_MinConfidenceVerifiedFromRawCount(t *testing.T) {
	t.Parallel()
	relaxer := &fakeRelaxer{}
	cause, steps, ok := verifyEmptySearchCause(
		"foo", "", "", "", 0.71, 5 /* rawPreConfidenceCount */, relaxer.relax(),
	)
	if !ok {
		t.Fatal("verifier returned ok=false; want a verified cause from rawCount")
	}
	if !strings.Contains(cause, "min_confidence ≥ 0.71") {
		t.Errorf("cause must name the confidence threshold, got: %q", cause)
	}
	if !strings.Contains(cause, "5 match(es)") {
		t.Errorf("cause must report the raw count for context, got: %q", cause)
	}
	// json.Marshal collapses 0.0 → "0" so accept either spelling. The
	// load-bearing assertion is that min_confidence is being set to
	// zero, not the trailing .0 cosmetic (#315).
	if !strings.Contains(steps[0]["args"], `"min_confidence":0.0`) &&
		!strings.Contains(steps[0]["args"], `"min_confidence":0`) {
		t.Errorf("recovery step must drop confidence threshold, got: %q", steps[0]["args"])
	}
}

// When no relaxation surfaces results, the verifier returns ok=false so
// the caller falls back to the static diagnosis path. Covers spelling
// errors, wrong-project, and genuinely-absent symbols.
func TestVerifyEmptySearchCause_NoRelaxationHelpsFallsThrough(t *testing.T) {
	t.Parallel()
	relaxer := &fakeRelaxer{counts: map[string]int{}} // every relaxation returns 0
	_, _, ok := verifyEmptySearchCause(
		"misspelt", "Function", "Go", "config", 0.71, 0, relaxer.relax(),
	)
	if ok {
		t.Error("verifier returned ok=true with no relaxation results; want fallthrough")
	}
}

// Language filter as the cause: drop language to surface results.
func TestVerifyEmptySearchCause_LanguageFilterIsActualCause(t *testing.T) {
	t.Parallel()
	relaxer := &fakeRelaxer{counts: map[string]int{
		"foo|||": 3, // dropping language surfaces 3 results (with no kind set either)
	}}
	cause, _, ok := verifyEmptySearchCause(
		"foo", "", "Python", "", 0, 0, relaxer.relax(),
	)
	if !ok {
		t.Fatal("verifier returned ok=false")
	}
	if !strings.Contains(cause, `language="Python"`) {
		t.Errorf("cause must name language filter, got: %q", cause)
	}
}

// Non-default corpus as the cause.
func TestVerifyEmptySearchCause_CorpusFilterIsActualCause(t *testing.T) {
	t.Parallel()
	relaxer := &fakeRelaxer{counts: map[string]int{
		"foo|||code": 2,
	}}
	cause, _, ok := verifyEmptySearchCause(
		"foo", "", "", "config", 0, 0, relaxer.relax(),
	)
	if !ok {
		t.Fatal("verifier returned ok=false")
	}
	if !strings.Contains(cause, `corpus="config"`) {
		t.Errorf("cause must name corpus filter, got: %q", cause)
	}
}

// Pair-drop fallback: two filters together hide results that single-
// drop probes don't surface. Verify the verifier escalates to dropping
// both kind and language together.
func TestVerifyEmptySearchCause_PairDropFallback(t *testing.T) {
	t.Parallel()
	relaxer := &fakeRelaxer{counts: map[string]int{
		// Single-drop probes: dropping kind alone (still has language) → 0.
		// Dropping language alone (still has kind) → 0. Both together → 4.
		"foo||Go|":      0,
		"foo|Function||": 0,
		"foo|||":        4,
	}}
	cause, _, ok := verifyEmptySearchCause(
		"foo", "Function", "Go", "", 0, 0, relaxer.relax(),
	)
	if !ok {
		t.Fatal("verifier returned ok=false despite pair-drop helping")
	}
	if !strings.Contains(cause, "AND") {
		t.Errorf("cause must indicate both filters together are the issue, got: %q", cause)
	}
}

// Integration test: the actual bug from #246 reproduced through
// handleSearch end-to-end. Seeds a Method symbol; searches with
// kind=Function (which excludes Methods); expects the diagnosis to
// blame kind, NOT min_confidence.
func TestHandleSearch_DiagnosisBlamesActualCulpritKindFilter(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	srv.sessionID = "diagnosis-bug"
	store.UpsertProject(db.Project{
		ID: "diagnosis-bug", Path: "/tmp/diagnosis-bug", Name: "diagnosis-bug",
		IndexedAt: time.Now(),
	})
	// Seed a Method symbol so kind=Function excludes it but the bare
	// query surfaces it. Confidence well above the 0.71 default so
	// confidence is not in play.
	if err := store.BulkUpsertSymbols([]db.Symbol{{
		ID: "diagnosis-bug::server.*Server.registerTools#Method", ProjectID: "diagnosis-bug",
		FilePath: "internal/server/server.go", Name: "registerTools",
		QualifiedName: "server.*Server.registerTools",
		Kind:          "Method",
		Language:      "Go",
		ExtractionConfidence: 1.0,
	}}); err != nil {
		t.Fatalf("BulkUpsertSymbols: %v", err)
	}

	result, err := srv.handleSearch(context.Background(), makeReq(map[string]any{
		"query":   "registerTools",
		"kind":    "Function",
		"project": "diagnosis-bug",
	}))
	if err != nil {
		t.Fatalf("handleSearch: %v", err)
	}
	body := decode(t, result)
	meta, _ := body["_meta"].(map[string]any)
	diagnosis, _ := meta["diagnosis"].(string)

	if diagnosis == "" {
		t.Fatalf("diagnosis missing; body: %v", body)
	}
	if strings.Contains(diagnosis, "min_confidence") {
		t.Errorf("diagnosis blames min_confidence when kind filter is the actual cause:\n  %s", diagnosis)
	}
	if !strings.Contains(diagnosis, `kind="Function"`) {
		t.Errorf("diagnosis must name kind filter as the culprit:\n  %s", diagnosis)
	}
}
