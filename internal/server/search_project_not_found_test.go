// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"
)

// #1063: handleSearch was the sole per-project tool that returned a
// bare errResult when the project arg didn't resolve. Every other
// per-project tool (architecture / query / symbol / dead_code /
// changes / etc.) goes through mustProject and gets the rich envelope
// with `list` + `index` next_steps. Pre-fix, an agent typo'ing the
// project name on search got `{"error":"project \"x\" not found — use
// list..."}` with no _meta, while the same typo on `query` returned a
// fully-structured response. Agents reading via _meta.next_steps
// (the documented contract for failure-as-pedagogy) silently lost the
// recovery affordance only on search.

func TestHandleSearch_ProjectNotFound_RichErrorWithListIndexSteps(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)

	res, err := srv.handleSearch(context.Background(), makeReq(map[string]any{
		"query":   "anything",
		"project": "absolutely-not-a-real-project",
	}))
	if err != nil {
		t.Fatalf("handleSearch: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError on missing project; got %s", textOf(t, res))
	}
	body := decode(t, res)
	errStr, _ := body["error"].(string)
	if !strings.Contains(errStr, "absolutely-not-a-real-project") {
		t.Errorf("expected the bad project name in the error string; got %q", errStr)
	}
	meta, _ := body["_meta"].(map[string]any)
	if meta == nil {
		t.Fatalf("expected _meta envelope on project-not-found; got bare error %q", errStr)
	}
	stepsRaw, _ := meta["next_steps"].([]any)
	if len(stepsRaw) < 2 {
		t.Fatalf("expected at least 2 next_steps (list + index); got %d (%v)", len(stepsRaw), stepsRaw)
	}
	wantTools := map[string]bool{"list": false, "index": false}
	for _, s := range stepsRaw {
		step, _ := s.(map[string]any)
		if tool, _ := step["tool"].(string); tool != "" {
			if _, want := wantTools[tool]; want {
				wantTools[tool] = true
			}
		}
	}
	for tool, found := range wantTools {
		if !found {
			t.Errorf("expected next_step with tool=%q; got steps=%v", tool, stepsRaw)
		}
	}
}

// Control: project="*" must NOT trip the project-not-found path —
// it's the cross-project search sentinel. Verifies the fix doesn't
// regress the existing star branch.
func TestHandleSearch_ProjectStar_BypassesProjectResolution(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)

	res, err := srv.handleSearch(context.Background(), makeReq(map[string]any{
		"query":   "anything",
		"project": "*",
	}))
	if err != nil {
		t.Fatalf("handleSearch: %v", err)
	}
	if res.IsError {
		t.Fatalf("project=\"*\" must NOT error on resolution; got %s", textOf(t, res))
	}
}
