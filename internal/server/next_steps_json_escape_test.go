// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #315: next_steps args must be valid JSON regardless of what the
// agent passed in. Pre-fix the empty-search and guide paths used
// fmt.Sprintf("{\"query\":\"%s\"}") which corrupted any query
// containing a literal `"`.

// nextStepArgs is the tiny helper that does the marshalling.
func TestNextStepArgs_EscapesEmbeddedQuotes(t *testing.T) {
	t.Parallel()
	got := nextStepArgs(map[string]any{"query": `"login flow"`})
	// Round-trip — the result must parse as valid JSON.
	var parsed map[string]any
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("nextStepArgs returned invalid JSON: %v\n  got: %s", err, got)
	}
	if parsed["query"].(string) != `"login flow"` {
		t.Errorf("query round-trip wrong: %v", parsed["query"])
	}
}

func TestNextStepArgs_EscapesBackslashes(t *testing.T) {
	t.Parallel()
	got := nextStepArgs(map[string]any{"query": `path\to\thing`})
	var parsed map[string]any
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("backslash query produced invalid JSON: %v\n  got: %s", err, got)
	}
}

// End-to-end: search with a phrase query (containing double-quotes)
// must not produce broken JSON in next_steps.
func TestHandleSearch_PhraseQuery_NextStepsAreValidJSON(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p1"
	store.UpsertProject(db.Project{ID: "p1", Path: "/tmp/p1", Name: "p1", IndexedAt: time.Now()})

	// Empty result so suggestEmptySearchNextSteps fires.
	result, err := srv.handleSearch(context.Background(), makeReq(map[string]any{
		"query":   `"resolve project"`,
		"project": "p1",
	}))
	if err != nil {
		t.Fatalf("handleSearch: %v", err)
	}
	body := decode(t, result)
	meta, _ := body["_meta"].(map[string]any)
	steps, _ := meta["next_steps"].([]any)
	for i, s := range steps {
		step, _ := s.(map[string]any)
		args, ok := step["args"].(string)
		if !ok || args == "" {
			continue
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(args), &parsed); err != nil {
			t.Errorf("next_steps[%d].args is invalid JSON: %v\n  args: %s\n  step: %v",
				i, err, args, step)
		}
	}
}

// Guide returns a search recommendation; the args must be valid JSON
// even when the task hint contains characters that would break naive
// fmt.Sprintf interpolation.
func TestHandleGuide_HintWithBackslash_ProducesValidJSON(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	result, err := srv.handleGuide(context.Background(), makeReq(map[string]any{
		// Backslash-bearing token that would crash fmt-based interpolation.
		"task": `understand internal\path\subsystem`,
	}))
	if err != nil {
		t.Fatalf("handleGuide: %v", err)
	}
	body := decode(t, result)
	recs, _ := body["recommended_next_tools"].([]any)
	for i, r := range recs {
		rec, _ := r.(map[string]any)
		args, ok := rec["args"].(string)
		if !ok || args == "" || args == "{}" {
			continue
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(args), &parsed); err != nil {
			t.Errorf("recommendation[%d].args is invalid JSON: %v\n  args: %s",
				i, err, args)
		}
	}
}
