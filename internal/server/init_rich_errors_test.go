// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"
)

// init handler had three bare errResult arg-validation paths that
// now use errResultRich so the agent gets explicit recovery shapes
// instead of a wall of text. Mirrors #976/#977/#982/#983/#987 family.

func TestHandleInit_MissingProjectPath_RichEnvelope(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	srv.sessionRoot = "" // simulate no roots configured

	res, err := srv.handleInit(context.Background(), makeReq(map[string]any{
		"target": "claude",
	}))
	if err != nil {
		t.Fatalf("handleInit: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError; got %s", textOf(t, res))
	}
	body := decode(t, res)
	meta, _ := body["_meta"].(map[string]any)
	steps, _ := meta["next_steps"].([]any)
	if len(steps) < 2 {
		t.Fatalf("expected init + list next_steps; got %d (%v)", len(steps), steps)
	}
	// init example with explicit project_path must surface.
	foundInitExample := false
	for _, s := range steps {
		step, _ := s.(map[string]any)
		if tool, _ := step["tool"].(string); tool == "init" {
			if args, _ := step["args"].(string); strings.Contains(args, `"project_path"`) {
				foundInitExample = true
				break
			}
		}
	}
	if !foundInitExample {
		t.Errorf("expected init example with explicit project_path; got %v", steps)
	}
}

func TestHandleInit_TargetContinue_RichEnvelope(t *testing.T) {
	t.Parallel()
	srv, _, root := newTestServer(t)
	srv.sessionRoot = root

	res, err := srv.handleInit(context.Background(), makeReq(map[string]any{
		"target":       "continue",
		"project_path": root,
	}))
	if err != nil {
		t.Fatalf("handleInit: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError; got %s", textOf(t, res))
	}
	body := decode(t, res)
	errStr, _ := body["error"].(string)
	if !strings.Contains(errStr, "continue") {
		t.Errorf("expected mention of continue in error; got %q", errStr)
	}
	meta, _ := body["_meta"].(map[string]any)
	steps, _ := meta["next_steps"].([]any)
	if len(steps) == 0 {
		t.Errorf("expected next_steps suggesting target=detect; got none")
	}
}

func TestHandleInit_UnknownTarget_RichEnvelope(t *testing.T) {
	t.Parallel()
	srv, _, root := newTestServer(t)
	srv.sessionRoot = root

	res, err := srv.handleInit(context.Background(), makeReq(map[string]any{
		"target":       "totally-nonsense-target",
		"project_path": root,
	}))
	if err != nil {
		t.Fatalf("handleInit: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError; got %s", textOf(t, res))
	}
	body := decode(t, res)
	meta, _ := body["_meta"].(map[string]any)
	steps, _ := meta["next_steps"].([]any)
	if len(steps) < 2 {
		t.Fatalf("expected detect + all init next_steps; got %d (%v)", len(steps), steps)
	}
	// Both target=detect and target=all examples must surface.
	wantTargets := map[string]bool{"detect": false, "all": false}
	for _, s := range steps {
		step, _ := s.(map[string]any)
		args, _ := step["args"].(string)
		for k := range wantTargets {
			if strings.Contains(args, `"target":"`+k+`"`) {
				wantTargets[k] = true
			}
		}
	}
	for k, found := range wantTargets {
		if !found {
			t.Errorf("expected next_step with target=%q; got %v", k, steps)
		}
	}
}
