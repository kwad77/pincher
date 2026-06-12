// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// #2036 routing coverage fill (wave B audit). Two genuinely-missing
// proxy-behavior paths the existing router_tools_test.go did not pin:
//
//  1. NON-JSON 200 body — a 200 from something that is not a router
//     (reverse proxy, wrong service on the addr, an HTML login page).
//     routerDo has a dedicated branch for this distinct from the non-200
//     and unreachable cases, with caller-actionable text ("something
//     other than a router may be on this address"); it had no test.
//  2. mode="advise" passthrough — the route tool's headline contract is
//     the mode tag, and the advise branch (spawn a host subagent at the
//     advised tier, envelope verbatim) is half of it. Every prior route
//     test exercised mode=execute or the pre-v2 untagged default; none
//     asserted an advise-tagged plan survives the proxy verbatim.
//
// Both are real router behaviours, not synthetic make-work: a 200/HTML
// impostor is the same false-positive class the detection ladder guards
// (PHASE0 §4), and advise is the documented ADVISE-host path.

// TestRoute_NonJSONRouterBody_StructuredError pins routerDo's non-JSON
// branch: a 200 whose body is not JSON must yield the standard never-
// block structured error naming the address-mismatch hint, never a Go
// error, never a hang, never a swallow.
func TestRoute_NonJSONRouterBody_StructuredError(t *testing.T) {
	// A non-router answering 200 with HTML on the router address — the
	// reverse-proxy / wrong-service-on-the-port shape.
	srv := newRouterToolServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<!DOCTYPE html><html><body>login</body></html>"))
	}))

	// route path.
	req := makeReq(map[string]any{"envelope": map[string]any{"tool_name": "x", "session_id": "s"}})
	req.Params.Name = "route"
	res, err := srv.handleRoute(context.Background(), req)
	if err != nil {
		t.Fatalf("handleRoute must never return a Go error for a non-JSON body: %v", err)
	}
	if !res.IsError {
		t.Fatal("a 200 non-JSON body must surface as a structured tool error, not a success")
	}
	if txt := textOf(t, res); !strings.Contains(txt, "non-JSON") || !strings.Contains(txt, "originating model") {
		t.Errorf("non-JSON error must name the address mismatch and the proceed-at-originating-model fallback; got %q", txt)
	}

	// models path goes through the same branch — pin it too so a future
	// refactor that special-cases one handler can't silently regress.
	req = makeReq(map[string]any{})
	req.Params.Name = "models"
	res, _ = srv.handleModels(context.Background(), req)
	if !res.IsError || !strings.Contains(textOf(t, res), "non-JSON") {
		t.Errorf("models against a non-JSON 200 must surface the same structured error; got %s", textOf(t, res))
	}
}

// TestRoute_AdviseModePlan_PassesThroughVerbatim pins the ADVISE half of
// the route contract: an advise-tagged ExecutionPlan must reach the
// caller verbatim with mode="advise" and its envelope/tier intact (the
// host-subagent path), and the envelope must still POST to the router
// unchanged. The proxy does not rewrite an explicitly-tagged plan — the
// mode-injection in handleRoute only fires when the tag is ABSENT.
func TestRoute_AdviseModePlan_PassesThroughVerbatim(t *testing.T) {
	fake := defaultFakeRouter(t)
	fake.routeBody = `{` +
		`"schema_version":"execution-plan/v2",` +
		`"mode":"advise",` +
		`"advised_tier":"heavy",` +
		`"operative":"architect-heavy",` +
		`"envelope":{"intent":"design the migration","complexity_tier":"heavy"},` +
		`"request_id":"adviseadviseadviseadviseadvise00"` +
		`}`
	srv := newRouterToolServer(t, fake)

	req := makeReq(map[string]any{"envelope": map[string]any{
		"tool_name":       "Task",
		"complexity_tier": "heavy",
		"role":            "frame",
		"session_id":      "advise-test",
	}})
	req.Params.Name = "route"
	res, err := srv.handleRoute(context.Background(), req)
	if err != nil {
		t.Fatalf("handleRoute: %v", err)
	}
	if res.IsError {
		t.Fatalf("advise route errored: %s", textOf(t, res))
	}
	body := decode(t, res)

	// mode survives verbatim — NOT rewritten to execute.
	if mode, _ := body["mode"].(string); mode != "advise" {
		t.Errorf("mode = %q, want \"advise\" — an explicitly-tagged plan must not be rewritten", mode)
	}
	// The advise-only plan fields reach the caller (the host-subagent
	// dispatch reads advised_tier + the envelope it forwards verbatim).
	if tier, _ := body["advised_tier"].(string); tier != "heavy" {
		t.Errorf("advised_tier not passed through: %#v", body["advised_tier"])
	}
	planEnv, _ := body["envelope"].(map[string]any)
	if planEnv == nil || planEnv["intent"] != "design the migration" {
		t.Errorf("advise plan envelope not passed through verbatim: %#v", body["envelope"])
	}
	// No mode-injection warning: the plan was already tagged, so the
	// pre-v2 "not mode-tagged" warning must NOT appear.
	if meta, _ := body["_meta"].(map[string]any); meta != nil {
		if warnings, _ := meta["warnings"].([]any); len(warnings) > 0 {
			for _, w := range warnings {
				if s, _ := w.(string); strings.Contains(s, "not mode-tagged") {
					t.Errorf("advise plan wrongly flagged as untagged: %v", warnings)
				}
			}
		}
	}

	// The request envelope still reached the router verbatim.
	sent, _ := fake.lastBodyJSON.Load().(map[string]any)
	if sent == nil || sent["role"] != "frame" || sent["complexity_tier"] != "heavy" {
		t.Errorf("envelope not POSTed verbatim on an advise route; router saw %v", sent)
	}

	// And the advise request_id was cached for the outcome join — the
	// advise path trains the router exactly like execute does. A minimal
	// outcome card must auto-fill from it.
	out := callRoute(t, srv, map[string]any{
		"action": "outcome",
		"outcome": map[string]any{
			"request_id":    "adviseadviseadviseadviseadvise00",
			"outcome_class": "clean",
			"gate":          "S5",
		},
	})
	if out.IsError {
		t.Fatalf("advise outcome errored: %s", textOf(t, out))
	}
	posted, _ := fake.lastBodyJSON.Load().(map[string]any)
	if posted["session_id"] != "advise-test" {
		t.Errorf("advise route_id not cached for the outcome join; posted %#v", posted)
	}
	if eb := decode(t, out); eb["echo_source"] != "cache" {
		t.Errorf("advise outcome echo_source = %v, want \"cache\"", eb["echo_source"])
	}
}
