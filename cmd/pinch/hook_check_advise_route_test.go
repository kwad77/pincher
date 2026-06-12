// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// Router-loop §A2 / item B8: the advise_route recruitment advisory —
// the #2016 advise_index mechanism on the Task trigger tool. Guardrails
// under test (mirroring hook_check_advise_index_2014_test.go):
// threshold N>=3 Task events per session, once per session, never when
// no router install is detected (rungs 1–2), never without a session
// id, always advisory (Continue=true on every branch), and the
// telemetry row shape the take-rate join depends on (decision =
// advise_route, file_path = session key).

// withHookRouter substitutes the rungs-1–2 detection seam for the
// duration of the test. The seam (hookRouterInstalled) exists exactly
// so these tests don't depend on the machine's PATH/home directory;
// the env contract of the real function is covered in
// internal/server/router_detect_test.go and the end-to-end test below.
func withHookRouter(t *testing.T, installed bool) {
	t.Helper()
	prev := hookRouterInstalled
	hookRouterInstalled = func() bool { return installed }
	t.Cleanup(func() { hookRouterInstalled = prev })
}

// fireTaskHook runs one full hook cycle the way runHookCheckCLI does:
// decide, then log. Logging matters — the §A2 threshold counter IS the
// hook_invocations rows prior invocations wrote.
func fireTaskHook(t *testing.T, store *db.Store, sessionID string) hookDecision {
	t.Helper()
	in := hookCheckInput{
		ToolName: "Task",
		ToolInput: map[string]any{
			"description":   "implement the thing",
			"subagent_type": "general-purpose",
		},
		SessionID: sessionID,
	}
	d := decideHook(store, in, false)
	logHookDecision(store, in, d)
	return d
}

func TestDecideHook_Task_ThirdEventAdvises_FourthSilent(t *testing.T) {
	withHookRouter(t, true)
	store := newHookTestStore(t)

	// Events 1 and 2: below threshold, silent pass-through.
	for i := 1; i <= 2; i++ {
		d := fireTaskHook(t, store, "sess-route")
		if !d.Continue || d.Decision != "pass_through" || d.SystemMessage != "" {
			t.Fatalf("event %d: want silent pass_through below threshold, got %+v", i, d)
		}
	}

	// Event 3: threshold met — one-time advisory, still non-blocking.
	d := fireTaskHook(t, store, "sess-route")
	if !d.Continue {
		t.Fatalf("advisory must never block (Continue=false): %+v", d)
	}
	if d.Decision != "advise_route" {
		t.Fatalf("third event decision = %q, want advise_route", d.Decision)
	}
	// Plan §A2: the advisory names the route tool and points at the
	// pincher-loop dispatch verse.
	if !strings.Contains(d.SystemMessage, "`route`") {
		t.Errorf("advisory should name the route tool; got %q", d.SystemMessage)
	}
	if !strings.Contains(d.SystemMessage, "dispatch verse") {
		t.Errorf("advisory should point at the dispatch verse; got %q", d.SystemMessage)
	}
	if !strings.Contains(d.SystemMessage, "Task passes through") {
		t.Errorf("advisory should say Task passes through; got %q", d.SystemMessage)
	}
	if d.SuggestedTool != "route" {
		t.Errorf("suggested tool = %q, want route", d.SuggestedTool)
	}
	// Telemetry contract: the advisory row's file_path is the SESSION
	// key — the once-per-session suppression key and the take-rate
	// join key (plan §A2).
	if d.FilePathParsed != "sess-route" {
		t.Errorf("advisory file_path = %q, want session key", d.FilePathParsed)
	}

	// Event 4 (and beyond): once per session — silent forever.
	for i := 0; i < 3; i++ {
		d := fireTaskHook(t, store, "sess-route")
		if d.Decision != "pass_through" || d.SystemMessage != "" {
			t.Fatalf("post-advisory event %d: want silent pass_through, got %+v", i+4, d)
		}
	}
}

func TestDecideHook_Task_AbsentRouter_NeverAdvises(t *testing.T) {
	withHookRouter(t, false)
	store := newHookTestStore(t)
	for i := 1; i <= 6; i++ {
		d := fireTaskHook(t, store, "sess-norouter")
		if !d.Continue {
			t.Fatalf("event %d: hook must never block (Continue=false): %+v", i, d)
		}
		if d.Decision != "pass_through" || d.SystemMessage != "" {
			t.Fatalf("event %d: absent router must stay silent pass_through, got %+v", i, d)
		}
	}
}

func TestDecideHook_Task_NoSessionID_NeverAdvises(t *testing.T) {
	withHookRouter(t, true)
	store := newHookTestStore(t)
	for i := 1; i <= 6; i++ {
		d := fireTaskHook(t, store, "")
		if d.Decision != "pass_through" || d.SystemMessage != "" {
			t.Fatalf("event %d without session id: got %+v", i, d)
		}
	}
}

func TestDecideHook_Task_ThresholdIsPerSession(t *testing.T) {
	withHookRouter(t, true)
	store := newHookTestStore(t)

	// Two events in session A, two in session B: neither crosses N=3.
	for _, sess := range []string{"sess-a", "sess-b"} {
		for i := 0; i < 2; i++ {
			if d := fireTaskHook(t, store, sess); d.Decision != "pass_through" {
				t.Fatalf("session %s below threshold: got %+v", sess, d)
			}
		}
	}
	// Third event in A advises; B's counter is untouched by A's rows.
	if d := fireTaskHook(t, store, "sess-a"); d.Decision != "advise_route" {
		t.Fatalf("third event in sess-a should advise, got %+v", d)
	}
	// A's advisory does not suppress B: B's own third event advises.
	if d := fireTaskHook(t, store, "sess-b"); d.Decision != "advise_route" {
		t.Fatalf("third event in sess-b should advise independently, got %+v", d)
	}
	// A new session starts from zero (threshold reset per session).
	if d := fireTaskHook(t, store, "sess-c"); d.Decision != "pass_through" {
		t.Fatalf("fresh session must start below threshold, got %+v", d)
	}
}

func TestDecideHook_Task_CounterCountsTaskEventsOnly(t *testing.T) {
	withHookRouter(t, true)
	store := newHookTestStore(t)

	// Two Read events + one Task event = one Task event, not three.
	// (The Reads target no indexed project and no git repo, so they log
	// pass_through rows — exactly the mixed-traffic shape a real
	// session produces under the Read|Grep|Glob|Task matcher.)
	dir := t.TempDir()
	for i := 0; i < 2; i++ {
		in := hookCheckInput{
			ToolName:  "Read",
			ToolInput: map[string]any{"file_path": dir + "/x.go"},
			SessionID: "sess-mixed",
		}
		d := decideHook(store, in, false)
		logHookDecision(store, in, d)
	}
	if d := fireTaskHook(t, store, "sess-mixed"); d.Decision != "pass_through" {
		t.Fatalf("non-Task rows must not count toward the threshold: %+v", d)
	}
}

// TestDecideHook_Task_AdviseRouteRowLandsInTelemetry pins the
// measurability contract: the advisory writes a hook_invocations row
// with decision='advise_route' whose file_path is the session key —
// the shape the advise_route → route-consult take-rate join (plan §A2
// adoption signal) queries against.
func TestDecideHook_Task_AdviseRouteRowLandsInTelemetry(t *testing.T) {
	withHookRouter(t, true)
	store := newHookTestStore(t)
	for i := 0; i < 3; i++ {
		fireTaskHook(t, store, "sess-telemetry")
	}
	if !store.HookRouteAdvisedInSession("sess-telemetry") {
		t.Fatal("advise_route row not found for session after threshold crossing")
	}
	if store.HookRouteAdvisedInSession("sess-other") {
		t.Error("advise_route row leaked across sessions")
	}
}

// TestHookCheckCLI_AdviseRoute_EndToEnd drives the real stdin/stdout
// shim (not just decideHook) through the REAL detection seam, pinning
// the wire shape and the PINCHER_ROUTER env contract: under =on the
// third Task event emits continue:true plus a systemMessage naming
// `route`; under =off the hook stays silent forever (off means zero
// routing activity — the rollback story covers the advisory too).
func TestHookCheckCLI_AdviseRoute_EndToEnd(t *testing.T) {
	run := func(dataDir, sess string) map[string]any {
		t.Helper()
		input := fmt.Sprintf(
			`{"hook_event_name":"PreToolUse","tool_name":"Task","tool_input":{"description":"do the thing"},"session_id":%q}`,
			sess,
		)
		return runHookCheckForTest(t, dataDir, input)
	}

	t.Run("router forced on: third event advises", func(t *testing.T) {
		t.Setenv("PINCHER_ROUTER", "on")
		dataDir := t.TempDir()
		for i := 1; i <= 2; i++ {
			resp := run(dataDir, "sess-e2e-route")
			if resp["continue"] != true {
				t.Fatalf("event %d: continue = %v, want true", i, resp["continue"])
			}
			if _, has := resp["systemMessage"]; has {
				t.Fatalf("event %d: unexpected systemMessage below threshold: %v", i, resp)
			}
		}
		resp := run(dataDir, "sess-e2e-route")
		if resp["continue"] != true {
			t.Fatalf("advisory must not block: %v", resp)
		}
		msg, _ := resp["systemMessage"].(string)
		if !strings.Contains(msg, "route") {
			t.Fatalf("third event systemMessage should name the route tool; got %v", resp)
		}
		resp = run(dataDir, "sess-e2e-route")
		if _, has := resp["systemMessage"]; has {
			t.Fatalf("fourth event must be silent (once per session): %v", resp)
		}
	})

	t.Run("router off: never advises", func(t *testing.T) {
		t.Setenv("PINCHER_ROUTER", "off")
		dataDir := t.TempDir()
		for i := 1; i <= 4; i++ {
			resp := run(dataDir, "sess-e2e-off")
			if resp["continue"] != true {
				t.Fatalf("event %d: continue = %v, want true", i, resp["continue"])
			}
			if _, has := resp["systemMessage"]; has {
				t.Fatalf("event %d: PINCHER_ROUTER=off must keep the hook silent: %v", i, resp)
			}
		}
	})
}
