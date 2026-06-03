// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #1028: handleGuide's schema declares a `project` arg but the handler
// used to ignore it entirely. A typo'd project name returned
// recommendations as if the caller hadn't passed a project — no signal
// the scope hint was dropped. Same contract-drift shape as #1024
// (stats). Closes the silent-fallback family across every per-project
// tool that takes a documented `project` arg.

func TestHandleGuide_UnknownProject_WarnsAndFallsBack(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	srv.sessionID = "guide-sess"
	store.UpsertProject(db.Project{
		ID: "guide-sess", Path: "/tmp/guide-sess", Name: "guide-sess",
		IndexedAt: time.Now(),
	})

	res, err := srv.handleGuide(context.Background(), makeReq(map[string]any{
		"task":    "find a function",
		"project": "totally-bogus-project",
	}))
	if err != nil {
		t.Fatalf("handleGuide: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success (advisory tool); got error: %s", textOf(t, res))
	}

	body := decode(t, res)
	meta, _ := body["_meta"].(map[string]any)
	if meta == nil {
		t.Fatal("_meta missing")
	}
	warnings, _ := meta["warnings"].([]any)
	foundWarn := false
	for _, w := range warnings {
		if s, _ := w.(string); strings.Contains(s, "totally-bogus-project") && strings.Contains(s, "did not resolve") {
			foundWarn = true
			break
		}
	}
	if !foundWarn {
		t.Errorf("expected project-resolution warning naming the failed lookup; got warnings=%v", warnings)
	}

	// Recommendations should still be present — guide is advisory, not
	// project-scoped. The warning is the only behavior change.
	if recs, _ := body["recommended_next_tools"].([]any); len(recs) == 0 {
		t.Errorf("expected non-empty recommended_next_tools even with bogus project; got empty")
	}
}

// Companion: a valid project arg (matching the session project) should
// produce no warning. This pins the "valid project = silent pass"
// behavior so future refactors don't accidentally warn on every call.
func TestHandleGuide_ValidProject_NoWarning(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	srv.sessionID = "guide-valid-sess"
	store.UpsertProject(db.Project{
		ID: "guide-valid-sess", Path: "/tmp/guide-valid-sess", Name: "guide-valid-sess",
		IndexedAt: time.Now(),
	})

	res, err := srv.handleGuide(context.Background(), makeReq(map[string]any{
		"task":    "find a function",
		"project": "guide-valid-sess",
	}))
	if err != nil {
		t.Fatalf("handleGuide: %v", err)
	}

	body := decode(t, res)
	meta, _ := body["_meta"].(map[string]any)
	warnings, _ := meta["warnings"].([]any)
	for _, w := range warnings {
		if s, _ := w.(string); strings.Contains(s, "did not resolve") {
			t.Errorf("unexpected resolve warning for valid project: %s", s)
		}
	}
}
