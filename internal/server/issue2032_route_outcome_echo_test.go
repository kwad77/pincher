// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Issue #2032: the #2026 outcome auto-echo did not fire on a live
// same-session route→outcome pair — the minimal card was forwarded
// un-echoed and the router 422'd on missing session_id. The in-process
// logic replays green against the exact live payloads (see
// TestIssue2032_SameSessionMinimalCard below, which drives the real
// MCP layer with the production wire shapes), so the production gap is
// environmental: the request_id → echo LRU is in-memory and does not
// survive a process respawn between the two calls (auto-restart-on-
// drift, crash, MCP client reconnect — all invisible to the caller).
// The fix is observability plus join hardening:
//   - every action=outcome response now carries echo_source:
//     cache|caller|none, so an un-echoed forward is diagnosable;
//   - echo_source=none logs a loud pincher.route_outcome.echo_miss;
//   - request_id extraction is normalized on both sides of the join
//     (string or JSON number) instead of a silent .(string) miss.

// liveRouteBody2032 is the EXACT live router response from the unit-10
// dogfood transcript (execution-plan/v2, request_id 358ac63f…).
const liveRouteBody2032 = `{"dry_run":{"launch":false,"ready":true},"executor":"local-vllm","invocation":{"chat_template_kwargs":{"enable_thinking":false},"endpoint":"http://127.0.0.1:8000/v1","max_tokens":2048,"model":"qwen3.6-35b-a3b","url":"http://127.0.0.1:8000/v1/chat/completions"},"lane":"fast-path","mode":"execute","model":"qwen3.6-27b","operative":false,"provider":"local_vllm","reconciliation":{"registry_model":"qwen3.6-27b","runtime_model":"qwen3.6-35b-a3b","served_models":["qwen3.6-35b-a3b"],"status":"resolved"},"request_id":"358ac63f54904cf3bd23f63305d19269","runtime_model":"qwen3.6-35b-a3b","schema_version":"execution-plan/v2","timeout_seconds":1800,"warning":"registry drift","worker":{"ctx_window":null,"kind":"local","model":"qwen3.6-27b","provider":"local_vllm","source":"declared"}}`

// TestIssue2032_SameSessionMinimalCard_ForwardsSessionID replays the
// live unit-10 transcript through the real MCP layer (tools/call wire
// shape, not direct handler invocation): route with the production
// envelope, then the verse's minimal outcome card. The forwarded
// /v1/outcomes body must carry the echoed session_id and the response
// must say the echo came from the cache.
func TestIssue2032_SameSessionMinimalCard_ForwardsSessionID(t *testing.T) {
	fake := defaultFakeRouter(t)
	fake.routeBody = liveRouteBody2032
	srv := newRouterToolServer(t, fake)

	cs, cleanup := connectInMemoryClient(t, srv, nil)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Exact production route call (unit 10).
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "route",
		Arguments: map[string]any{
			"action": "route",
			"envelope": map[string]any{
				"tool_name":       "auto_echo_validation_unit",
				"complexity_tier": "lite",
				"role":            "fix",
				"tokens_used":     300,
				"session_id":      "router-loop-dogfood-unit-10",
				"intent":          "One-sentence answer: state the single biggest operational benefit ...",
				"_meta":           map[string]any{"stage": "Make", "probe": "v1.8.0 auto-echo live validation"},
			},
		},
	})
	if err != nil {
		t.Fatalf("route call: %v", err)
	}
	if res.IsError {
		t.Fatalf("route consult errored: %v", res.Content)
	}

	// Exact production minimal-card outcome (unit 10).
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "route",
		Arguments: map[string]any{
			"action": "outcome",
			"outcome": map[string]any{
				"request_id":    "358ac63f54904cf3bd23f63305d19269",
				"outcome_class": "shallow",
				"gate":          "S5",
				"quality_score": 0.6,
				"notes":         "unit 10: answered bandwidth",
			},
		},
	})
	if err != nil {
		t.Fatalf("outcome call: %v", err)
	}
	if res.IsError {
		t.Fatalf("outcome errored: %v", res.Content[0].(*mcp.TextContent).Text)
	}
	posted, _ := fake.lastBodyJSON.Load().(map[string]any)
	if posted == nil {
		t.Fatal("no outcome body reached the fake router")
	}
	if posted["session_id"] != "router-loop-dogfood-unit-10" {
		t.Errorf("forwarded outcome body missing echoed session_id: %#v", posted)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &body); err != nil {
		t.Fatalf("outcome response not JSON: %v", err)
	}
	if body["echo_source"] != "cache" {
		t.Errorf("echo_source = %v, want \"cache\"", body["echo_source"])
	}
}

// TestIssue2032_EchoSource_CallerWhenCardCarriesEcho: cold cache (no
// prior route call — the post-respawn shape) but the caller echoed the
// FULL required envelope itself, i.e. the live workaround. Observable as
// "caller" because the body the router receives is complete and will not
// 422. #2036 MED-2 narrowed "caller" to a COMPLETE card (or cache hit):
// the card therefore carries every required echo key here.
func TestIssue2032_EchoSource_CallerWhenCardCarriesEcho(t *testing.T) {
	fake := defaultFakeRouter(t)
	srv := newRouterToolServer(t, fake)

	res := callRoute(t, srv, map[string]any{
		"action": "outcome",
		"outcome": map[string]any{
			"request_id":      "358ac63f54904cf3bd23f63305d19269",
			"outcome_class":   "clean",
			"gate":            "S5",
			"session_id":      "router-loop-dogfood-unit-10",
			"tool_name":       "auto_echo_validation_unit",
			"complexity_tier": "lite",
			"role":            "fix",
			"routed_model":    "qwen3.6-35b-a3b",
			"lane":            "fast",
		},
	})
	if res.IsError {
		t.Fatalf("outcome errored: %s", textOf(t, res))
	}
	posted, _ := fake.lastBodyJSON.Load().(map[string]any)
	if posted["session_id"] != "router-loop-dogfood-unit-10" {
		t.Errorf("caller-echoed session_id not forwarded: %#v", posted)
	}
	if body := decode(t, res); body["echo_source"] != "caller" {
		t.Errorf("echo_source = %v, want \"caller\"", body["echo_source"])
	}
}

// TestIssue2032_EchoSource_CallerWhenCacheHitFillsNothing: a cache hit
// where the caller already supplied every echo field is "caller", not
// "cache" — the response never claims work it didn't do.
func TestIssue2032_EchoSource_CallerWhenCacheHitFillsNothing(t *testing.T) {
	fake := defaultFakeRouter(t)
	srv := newRouterToolServer(t, fake)

	if res := callRoute(t, srv, map[string]any{"envelope": minimalEnvelope()}); res.IsError {
		t.Fatalf("route consult errored: %s", textOf(t, res))
	}
	res := callRoute(t, srv, map[string]any{
		"action": "outcome",
		"outcome": map[string]any{
			"request_id":    "deadbeefdeadbeefdeadbeefdeadbeef",
			"outcome_class": "clean",
			"gate":          "S5",
			// Full explicit echo — nothing left to fill.
			"session_id":      "sess-autoecho",
			"tool_name":       "Task",
			"complexity_tier": "lite",
			"role":            "fix",
			"tokens_used":     float64(1234),
			"routed_model":    "qwen3.6-35b-a3b",
			"lane":            "fast",
		},
	})
	if res.IsError {
		t.Fatalf("outcome errored: %s", textOf(t, res))
	}
	body := decode(t, res)
	if body["echo_source"] != "caller" {
		t.Errorf("echo_source = %v, want \"caller\"", body["echo_source"])
	}
	if _, has := body["echo_autofilled"]; has {
		t.Errorf("echo_autofilled claimed on a fill-nothing hit: %v", body["echo_autofilled"])
	}
}

// TestIssue2032_EchoSource_NoneOnColdCacheMinimalCard pins the exact
// production failure shape from the live transcript: minimal card,
// cold cache (post-respawn), no caller echo. The card still passes
// through unchanged (the honest-422 posture from #2026 holds) but the
// response now SAYS so via echo_source=none — the field whose absence
// made #2032 undiagnosable.
func TestIssue2032_EchoSource_NoneOnColdCacheMinimalCard(t *testing.T) {
	fake := defaultFakeRouter(t)
	srv := newRouterToolServer(t, fake)

	res := callRoute(t, srv, map[string]any{
		"action": "outcome",
		"outcome": map[string]any{
			"request_id":    "358ac63f54904cf3bd23f63305d19269",
			"outcome_class": "shallow",
			"gate":          "S5",
		},
	})
	if res.IsError {
		t.Fatalf("outcome errored: %s", textOf(t, res))
	}
	posted, _ := fake.lastBodyJSON.Load().(map[string]any)
	if _, has := posted["session_id"]; has {
		t.Errorf("cold cache must not invent a session_id: %#v", posted)
	}
	if body := decode(t, res); body["echo_source"] != "none" {
		t.Errorf("echo_source = %v, want \"none\"", body["echo_source"])
	}
}

// TestIssue2032_NumericRequestID_StillJoins: a router emitting
// request_id as a JSON number must still join — the old .(string)
// assertion silently disabled the cache write with no trace.
func TestIssue2032_NumericRequestID_StillJoins(t *testing.T) {
	fake := defaultFakeRouter(t)
	fake.routeBody = `{"schema_version":"execution-plan/v2","mode":"execute","model":"m","lane":"fast","request_id":12345}`
	srv := newRouterToolServer(t, fake)

	if res := callRoute(t, srv, map[string]any{"envelope": minimalEnvelope()}); res.IsError {
		t.Fatalf("route consult errored: %s", textOf(t, res))
	}
	// Card carries the id back as a number too (the symmetric shape).
	res := callRoute(t, srv, map[string]any{
		"action": "outcome",
		"outcome": map[string]any{
			"request_id":    float64(12345),
			"outcome_class": "clean",
			"gate":          "S5",
		},
	})
	if res.IsError {
		t.Fatalf("outcome errored: %s", textOf(t, res))
	}
	posted, _ := fake.lastBodyJSON.Load().(map[string]any)
	if posted["session_id"] != "sess-autoecho" {
		t.Errorf("numeric request_id did not join: %#v", posted)
	}
	if body := decode(t, res); body["echo_source"] != "cache" {
		t.Errorf("echo_source = %v, want \"cache\"", body["echo_source"])
	}
}
