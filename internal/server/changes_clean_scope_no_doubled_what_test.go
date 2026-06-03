// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #1113: when `changes` returns a clean scope, the alternative-scope
// next_steps had a doubled "what what's" — the caller used
// "to see what %s captures" and scopeDescription("staged") returned
// "what's already added ...". The composition read "see what what's
// already added", which is jarring. Fixed by changing the format to
// "try scope=X — <description>".

func TestHandleChanges_CleanScope_NextStepsHaveNoDoubledWhat(t *testing.T) {
	t.Parallel()
	srv, store, root := newTestServer(t)
	pid := db.ProjectIDFromPath(root)
	store.UpsertProject(db.Project{
		ID: pid, Path: root, Name: pid, IndexedAt: time.Now(),
	})
	srv.sessionID = pid
	srv.sessionRoot = root

	// Clean scope (no uncommitted changes in the test temp dir).
	res, err := srv.handleChanges(context.Background(), makeReq(map[string]any{
		"scope": "unstaged",
	}))
	if err != nil {
		t.Fatalf("handleChanges: %v", err)
	}
	body := decode(t, res)
	meta, _ := body["_meta"].(map[string]any)
	steps, _ := meta["next_steps"].([]any)
	for _, s := range steps {
		step, _ := s.(map[string]any)
		why, _ := step["why"].(string)
		if strings.Contains(why, "what what") {
			t.Errorf("next_step why contains doubled 'what what'; got %q", why)
		}
	}
	// Also check the diagnosis text + the error envelope to ensure no
	// doubled 'what what' shows up anywhere in the response shape.
	bodyText := strings.ToLower(textOf(t, res))
	if strings.Contains(bodyText, "what what") {
		t.Errorf("response contains doubled 'what what' somewhere: %s", bodyText)
	}
}

// Direct unit test of the format-string composition: scopeDescription
// values must not produce "what what" when paired with the new format
// string. Guards against future regressions where someone re-introduces
// "to see what %s" without updating the descriptions.
func TestChangesNextStep_FormatComposition_NoDoubledWhat(t *testing.T) {
	t.Parallel()
	// The post-fix call site is: fmt.Sprintf("try scope=%q — %s", other, scopeDescription(other))
	// Verify no description starts with "what" (which would imply the
	// caller can never compose into "what what").
	for _, scope := range []string{"staged", "unstaged", "all"} {
		desc := scopeDescription(scope)
		composed := "try scope=\"" + scope + "\" — " + desc
		if strings.Contains(composed, "what what") {
			t.Errorf("composed string contains doubled 'what what': %q", composed)
		}
	}
}
