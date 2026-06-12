// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Router-loop item B10, pincher half: the outcome auto-echo. Measured
// dogfood finding (request_id 1d41c9e4, five 422s): the router's
// OutcomeBody requires the full envelope/plan echo but the dispatch
// verse promises a minimal card {request_id, outcome_class, gate}.
// The proxy saw the route call, so it caches request_id → echo fields
// and completes the card before POSTing /v1/outcomes. Behavior matrix
// under test: auto-fill hit, explicit-override, cache-miss
// passthrough (incl. the honest 422), and the LRU bound.

// callRoute drives the route tool handler with the given args.
func callRoute(t *testing.T, srv *Server, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := makeReq(args)
	req.Params.Name = "route"
	res, err := srv.handleRoute(context.Background(), req)
	if err != nil {
		t.Fatalf("handleRoute: %v", err)
	}
	return res
}

// minimalEnvelope is the envelope-composer shape the verse sends on a
// route consult — the fields whose echo the outcome card needs back.
func minimalEnvelope() map[string]any {
	return map[string]any{
		"tool_name":       "Task",
		"complexity_tier": "lite",
		"role":            "fix",
		"session_id":      "sess-autoecho",
		"tokens_used":     float64(1234),
	}
}

func TestRouteOutcome_AutoFillsEchoFromCachedRouteCall(t *testing.T) {
	fake := defaultFakeRouter(t)
	srv := newRouterToolServer(t, fake)

	// Route consult: the fake answers fakeRouteBodyV2 (request_id
	// deadbeef…, model qwen3.6-35b-a3b, lane fast, mode execute).
	if res := callRoute(t, srv, map[string]any{"envelope": minimalEnvelope()}); res.IsError {
		t.Fatalf("route consult errored: %s", textOf(t, res))
	}

	// The verse's minimal card — exactly what 422'd five times live.
	res := callRoute(t, srv, map[string]any{
		"action": "outcome",
		"outcome": map[string]any{
			"request_id":    "deadbeefdeadbeefdeadbeefdeadbeef",
			"outcome_class": "clean",
			"gate":          "S5",
		},
	})
	if res.IsError {
		t.Fatalf("outcome report errored: %s", textOf(t, res))
	}

	posted, _ := fake.lastBodyJSON.Load().(map[string]any)
	if posted == nil {
		t.Fatal("no outcome body reached the fake router")
	}
	want := map[string]any{
		// Caller-supplied survives untouched.
		"request_id":    "deadbeefdeadbeefdeadbeefdeadbeef",
		"outcome_class": "clean",
		"gate":          "S5",
		// Envelope echo, auto-filled.
		"session_id":      "sess-autoecho",
		"tool_name":       "Task",
		"complexity_tier": "lite",
		"role":            "fix",
		"tokens_used":     float64(1234),
		// Plan echo: routed_model derived plan.model-style (the v2
		// ExecutionPlan fixture carries `model`, not `routed_model`).
		"routed_model": "qwen3.6-35b-a3b",
		"lane":         "fast",
	}
	if !reflect.DeepEqual(posted, want) {
		t.Errorf("POSTed outcome body = %#v\nwant %#v", posted, want)
	}

	// The proxy says what it did: the response names the filled keys.
	body := decode(t, res)
	filled, _ := body["echo_autofilled"].([]any)
	if len(filled) != 7 {
		t.Errorf("echo_autofilled = %v, want the 7 filled echo keys", body["echo_autofilled"])
	}
}

func TestRouteOutcome_ExplicitCallerFieldsAlwaysWin(t *testing.T) {
	fake := defaultFakeRouter(t)
	srv := newRouterToolServer(t, fake)

	if res := callRoute(t, srv, map[string]any{"envelope": minimalEnvelope()}); res.IsError {
		t.Fatalf("route consult errored: %s", textOf(t, res))
	}
	res := callRoute(t, srv, map[string]any{
		"action": "outcome",
		"outcome": map[string]any{
			"request_id":    "deadbeefdeadbeefdeadbeefdeadbeef",
			"outcome_class": "errored",
			"gate":          "S5",
			// Explicit overrides — the caller knows better than the cache.
			"routed_model": "qwen3.6-35b-a3b-int4",
			"tokens_used":  float64(999),
		},
	})
	if res.IsError {
		t.Fatalf("outcome report errored: %s", textOf(t, res))
	}
	posted, _ := fake.lastBodyJSON.Load().(map[string]any)
	if posted["routed_model"] != "qwen3.6-35b-a3b-int4" {
		t.Errorf("explicit routed_model clobbered by cache: %v", posted["routed_model"])
	}
	if posted["tokens_used"] != float64(999) {
		t.Errorf("explicit tokens_used clobbered by cache: %v", posted["tokens_used"])
	}
	// Absent keys still auto-filled alongside the overrides.
	if posted["session_id"] != "sess-autoecho" || posted["lane"] != "fast" {
		t.Errorf("missing keys not filled next to explicit overrides: %#v", posted)
	}
}

func TestRouteOutcome_CacheMiss_PassesThroughUnchanged(t *testing.T) {
	fake := defaultFakeRouter(t)
	srv := newRouterToolServer(t, fake)

	// No prior route call in this process — the fresh-session shape.
	card := map[string]any{
		"request_id":    "unseen-request-id",
		"outcome_class": "clean",
		"gate":          "S5",
	}
	res := callRoute(t, srv, map[string]any{"action": "outcome", "outcome": card})
	if res.IsError {
		t.Fatalf("outcome report errored: %s", textOf(t, res))
	}
	posted, _ := fake.lastBodyJSON.Load().(map[string]any)
	want := map[string]any{
		"request_id":    "unseen-request-id",
		"outcome_class": "clean",
		"gate":          "S5",
	}
	if !reflect.DeepEqual(posted, want) {
		t.Errorf("cache miss must pass the card through unchanged; POSTed %#v", posted)
	}
	if _, has := decode(t, res)["echo_autofilled"]; has {
		t.Error("cache miss must not claim any auto-fill")
	}
}

func TestRouteOutcome_CacheMiss_RouterRejectionSurfacesHonestly(t *testing.T) {
	// A validating router 422s the un-completed card — the proxy must
	// surface that as the standard structured error, never swallow it
	// or invent echo values it didn't see.
	srv := newRouterToolServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"detail":[{"loc":["body","routed_model"],"msg":"Field required"}]}`))
	}))
	res := callRoute(t, srv, map[string]any{
		"action": "outcome",
		"outcome": map[string]any{
			"request_id":    "unseen-request-id",
			"outcome_class": "clean",
			"gate":          "S5",
		},
	})
	if !res.IsError {
		t.Fatal("router 422 must surface as a structured error")
	}
	if txt := textOf(t, res); !strings.Contains(txt, "422") || !strings.Contains(txt, "routed_model") {
		t.Errorf("error should carry the router's 422 and the missing field: %s", txt)
	}
}

func TestRouteOutcome_RoutedModelDerivation(t *testing.T) {
	// routed_model derives the way the router's own telemetry does
	// (plan.runtime_model or plan.model), with an explicit
	// routed_model field winning outright.
	cases := []struct {
		name string
		plan map[string]any
		want string
	}{
		{"explicit routed_model wins", map[string]any{"routed_model": "rm", "runtime_model": "run", "model": "m"}, "rm"},
		{"runtime_model over model", map[string]any{"runtime_model": "run", "model": "m"}, "run"},
		{"model as fallback", map[string]any{"model": "m"}, "m"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fields := routeEchoFromCall(minimalEnvelope(), tc.plan)
			if fields["routed_model"] != tc.want {
				t.Errorf("routed_model = %v, want %q", fields["routed_model"], tc.want)
			}
		})
	}
	t.Run("no model fields caches no routed_model", func(t *testing.T) {
		fields := routeEchoFromCall(minimalEnvelope(), map[string]any{"lane": "fast"})
		if _, has := fields["routed_model"]; has {
			t.Error("routed_model invented from nothing")
		}
	})
}

func TestRouteEchoCache_LRUBound(t *testing.T) {
	fake := defaultFakeRouter(t)
	srv := newRouterToolServer(t, fake)

	// Fill the cache one past the cap: request-0 must evict, the rest
	// must survive. The fake's route body is swapped per call so each
	// consult pins a distinct request_id (sequential calls — no race).
	for i := 0; i <= routeEchoCacheCap; i++ {
		fake.routeBody = fmt.Sprintf(
			`{"schema_version":"execution-plan/v1","model":"m-%d","lane":"fast","mode":"execute","request_id":"req-%d"}`, i, i)
		if res := callRoute(t, srv, map[string]any{"envelope": minimalEnvelope()}); res.IsError {
			t.Fatalf("route consult %d errored: %s", i, textOf(t, res))
		}
	}

	// Oldest entry: evicted ⇒ cache-miss passthrough.
	res := callRoute(t, srv, map[string]any{
		"action":  "outcome",
		"outcome": map[string]any{"request_id": "req-0", "outcome_class": "clean", "gate": "S5"},
	})
	if res.IsError {
		t.Fatalf("outcome for evicted id errored: %s", textOf(t, res))
	}
	posted, _ := fake.lastBodyJSON.Load().(map[string]any)
	if _, has := posted["routed_model"]; has {
		t.Errorf("evicted entry still auto-filled: %#v", posted)
	}

	// Newest entry: present ⇒ auto-filled with ITS plan fields.
	res = callRoute(t, srv, map[string]any{
		"action": "outcome",
		"outcome": map[string]any{
			"request_id":    fmt.Sprintf("req-%d", routeEchoCacheCap),
			"outcome_class": "clean", "gate": "S5",
		},
	})
	if res.IsError {
		t.Fatalf("outcome for cached id errored: %s", textOf(t, res))
	}
	posted, _ = fake.lastBodyJSON.Load().(map[string]any)
	if posted["routed_model"] != fmt.Sprintf("m-%d", routeEchoCacheCap) {
		t.Errorf("cached entry not auto-filled with its own plan: %#v", posted)
	}
}

func TestRouteEchoCache_GetRefreshesRecency(t *testing.T) {
	var c routeEchoCache
	for i := 0; i < routeEchoCacheCap; i++ {
		c.put(fmt.Sprintf("id-%d", i), map[string]any{"lane": "fast"})
	}
	// Touch the oldest, then insert one more: the SECOND-oldest must
	// evict, not the touched one — true LRU, not FIFO.
	if _, ok := c.get("id-0"); !ok {
		t.Fatal("id-0 should still be cached")
	}
	c.put("id-new", map[string]any{"lane": "fast"})
	if _, ok := c.get("id-0"); !ok {
		t.Error("recently-used entry evicted (FIFO, want LRU)")
	}
	if _, ok := c.get("id-1"); ok {
		t.Error("least-recently-used entry survived past the cap")
	}
}

// TestRouteOutcome_PreV2RouteResponse_CachesNothing pins the skew
// posture: a pre-contract-v2 route response has no request_id, so the
// proxy caches nothing and never fabricates a join key.
func TestRouteOutcome_PreV2RouteResponse_CachesNothing(t *testing.T) {
	fake := defaultFakeRouter(t)
	fake.routeBody = `{"schema_version":"execution-plan/v1","model":"m","lane":"fast"}`
	srv := newRouterToolServer(t, fake)
	if res := callRoute(t, srv, map[string]any{"envelope": minimalEnvelope()}); res.IsError {
		t.Fatalf("route consult errored: %s", textOf(t, res))
	}
	srv.routeEcho.mu.Lock()
	n := len(srv.routeEcho.entries)
	srv.routeEcho.mu.Unlock()
	if n != 0 {
		t.Errorf("pre-v2 response cached %d entries, want 0", n)
	}
}
