// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Router-loop item B5: proxy-behavior tests for the conditional
// models/route tools (router_tools.go), driven against an httptest
// fake router speaking contract v2 (pincher-router PR #26). The
// advertisement halves of the dual-state contract live in
// tool_contract_test.go; this file covers the wire behavior:
// handshake render + skew hints, route round-trip, outcomes post,
// unreachable ⇒ clean structured error, hanging router ⇒ bounded
// timeout, and the PINCHER_ROUTER=off zero-activity short-circuit.

// fakeRouterV2 is a minimal contract-v2 router: GET /v1/models,
// POST /v1/route, POST /v1/outcomes. Each handler body is
// substitutable; requests are recorded for assertions.
type fakeRouterV2 struct {
	t *testing.T

	modelsBody string // served by GET /v1/models (200)
	routeBody  string // served by POST /v1/route (200)

	hits         int64
	lastPath     atomic.Value // string
	lastBodyJSON atomic.Value // map[string]any
	outcomes     int64
}

func (f *fakeRouterV2) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt64(&f.hits, 1)
	f.lastPath.Store(r.URL.Path)
	if r.Body != nil {
		raw, _ := io.ReadAll(r.Body)
		var m map[string]any
		if len(raw) > 0 && json.Unmarshal(raw, &m) == nil {
			f.lastBodyJSON.Store(m)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
		_, _ = w.Write([]byte(f.modelsBody))
	case r.Method == http.MethodPost && r.URL.Path == "/v1/route":
		_, _ = w.Write([]byte(f.routeBody))
	case r.Method == http.MethodPost && r.URL.Path == "/v1/outcomes":
		atomic.AddInt64(&f.outcomes, 1)
		_, _ = w.Write([]byte(`{"ok":true}`))
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"detail":"not found"}`))
	}
}

const fakeModelsBodyV2 = `{
	"handshake": {"contract_version": 2, "weights_version": "v2-test", "registry_version": 2, "capabilities": ["advise", "execute", "discovery"]},
	"providers": {"local_vllm": {"endpoint": "http://localhost:8000/v1", "auth": "none", "models": {"qwen3.6-35b-a3b": {"tier": ["lite"], "kind": "local", "ctx_window": 131072, "source": "discovered", "enabled": true}}}},
	"policy": {"prefer_local": true, "local_only": false},
	"originating_model_passthrough": true
}`

const fakeRouteBodyV2 = `{
	"schema_version": "execution-plan/v1",
	"operative": "scout-lite",
	"executor": "local-vllm",
	"provider": "local_vllm",
	"model": "qwen3.6-35b-a3b",
	"lane": "fast",
	"mode": "execute",
	"request_id": "deadbeefdeadbeefdeadbeefdeadbeef"
}`

// newRouterToolServer wires a pincher Server at a fake router:
// PINCHER_ROUTER=on (forced detection, zero startup network — the
// item-B4 override) + PINCHER_ROUTER_ADDR at the httptest listener.
func newRouterToolServer(t *testing.T, handler http.Handler) *Server {
	t.Helper()
	rt := httptest.NewServer(handler)
	t.Cleanup(rt.Close)
	t.Setenv("PINCHER_ROUTER", "on")
	t.Setenv("PINCHER_ROUTER_ADDR", strings.TrimPrefix(rt.URL, "http://"))
	srv, _, _ := newTestServer(t)
	return srv
}

func defaultFakeRouter(t *testing.T) *fakeRouterV2 {
	return &fakeRouterV2{t: t, modelsBody: fakeModelsBodyV2, routeBody: fakeRouteBodyV2}
}

// ── models ────────────────────────────────────────────────────────────────

func TestModels_List_ProxiesHandshakeAndRegistry(t *testing.T) {
	fake := defaultFakeRouter(t)
	srv := newRouterToolServer(t, fake)

	req := makeReq(map[string]any{})
	req.Params.Name = "models"
	res, err := srv.handleModels(context.Background(), req)
	if err != nil {
		t.Fatalf("handleModels: %v", err)
	}
	if res.IsError {
		t.Fatalf("models list errored: %s", textOf(t, res))
	}
	body := decode(t, res)
	hs, _ := body["handshake"].(map[string]any)
	if hs == nil {
		t.Fatalf("models response missing handshake: %v", body)
	}
	if v, _ := hs["contract_version"].(float64); int(v) != 2 {
		t.Errorf("handshake contract_version = %v, want 2", hs["contract_version"])
	}
	if _, ok := body["providers"].(map[string]any); !ok {
		t.Errorf("models response missing providers render")
	}
	if _, ok := body["hint"]; ok {
		t.Errorf("v2 handshake must not carry an upgrade hint; got %v", body["hint"])
	}
	if _, ok := body["_meta"]; !ok {
		t.Errorf("models response missing _meta envelope")
	}
	if p, _ := fake.lastPath.Load().(string); p != "/v1/models" {
		t.Errorf("proxied path = %q, want /v1/models", p)
	}
}

func TestModels_List_OldContract_UpgradeHint(t *testing.T) {
	fake := defaultFakeRouter(t)
	fake.modelsBody = `{"handshake": {"contract_version": 1, "weights_version": "v1", "registry_version": 1, "capabilities": ["execute"]}, "providers": {}}`
	srv := newRouterToolServer(t, fake)

	req := makeReq(map[string]any{"action": "list"})
	req.Params.Name = "models"
	res, _ := srv.handleModels(context.Background(), req)
	if res.IsError {
		t.Fatalf("installed-but-old is a state, not an error: %s", textOf(t, res))
	}
	body := decode(t, res)
	hint, _ := body["hint"].(string)
	if !strings.Contains(hint, "upgrade pincher-router") {
		t.Errorf("contract_version=1 must render an upgrade hint; got %q", hint)
	}
}

func TestModels_List_FutureContract_TreatedAsV2Subset(t *testing.T) {
	fake := defaultFakeRouter(t)
	fake.modelsBody = `{"handshake": {"contract_version": 3, "weights_version": "v3", "registry_version": 3, "capabilities": ["execute"]}, "providers": {}}`
	srv := newRouterToolServer(t, fake)

	req := makeReq(map[string]any{})
	req.Params.Name = "models"
	res, _ := srv.handleModels(context.Background(), req)
	if res.IsError {
		t.Fatalf("contract_version above ours must be treated as a v2 superset, not an error: %s", textOf(t, res))
	}
	body := decode(t, res)
	if _, ok := body["hint"]; ok {
		t.Errorf("v3 handshake must not carry the installed-but-old hint; got %v", body["hint"])
	}
}

func TestModels_MutationActions_StructuredError(t *testing.T) {
	fake := defaultFakeRouter(t)
	srv := newRouterToolServer(t, fake)

	for _, action := range []string{"enable", "disable", "test"} {
		req := makeReq(map[string]any{"action": action, "model": "local_vllm/qwen3.6-35b-a3b"})
		req.Params.Name = "models"
		res, err := srv.handleModels(context.Background(), req)
		if err != nil {
			t.Fatalf("handleModels(%s): %v", action, err)
		}
		if !res.IsError {
			t.Errorf("action %q must answer with a structured error until the contract exposes registry mutation", action)
		}
		body := decode(t, res)
		msg, _ := body["error"].(string)
		if !strings.Contains(msg, "never writes workers.yaml") {
			t.Errorf("action %q error must state the registry-ownership rule; got %q", action, msg)
		}
		if _, ok := body["_meta"].(map[string]any); !ok {
			t.Errorf("action %q error missing the standard _meta envelope", action)
		}
	}
	// Reserved actions must not generate router traffic — there is no
	// endpoint to call.
	if n := atomic.LoadInt64(&fake.hits); n != 0 {
		t.Errorf("mutation actions dialed the router %d time(s); contract v2 has no mutation surface", n)
	}
}

func TestModels_UnknownAction_StructuredError(t *testing.T) {
	srv := newRouterToolServer(t, defaultFakeRouter(t))
	req := makeReq(map[string]any{"action": "discover"})
	req.Params.Name = "models"
	res, _ := srv.handleModels(context.Background(), req)
	if !res.IsError {
		t.Error("unknown action must error")
	}
	if msg := textOf(t, res); !strings.Contains(msg, "discover") {
		t.Errorf("error should echo the unknown action; got %q", msg)
	}
}

// ── route ─────────────────────────────────────────────────────────────────

func TestRoute_RoundTrip_ModeTaggedPlanAndRequestID(t *testing.T) {
	fake := defaultFakeRouter(t)
	srv := newRouterToolServer(t, fake)

	envelope := map[string]any{
		"tool_name":       "mcp_pincher_search",
		"complexity_tier": "lite",
		"role":            "explore",
		"session_id":      "item5-test",
	}
	req := makeReq(map[string]any{"envelope": envelope})
	req.Params.Name = "route"
	res, err := srv.handleRoute(context.Background(), req)
	if err != nil {
		t.Fatalf("handleRoute: %v", err)
	}
	if res.IsError {
		t.Fatalf("route errored: %s", textOf(t, res))
	}
	body := decode(t, res)
	if mode, _ := body["mode"].(string); mode != "execute" {
		t.Errorf("mode = %q, want execute (fake plan is execute-tagged)", mode)
	}
	if rid, _ := body["request_id"].(string); rid != "deadbeefdeadbeefdeadbeefdeadbeef" {
		t.Errorf("request_id not passed through: %q", rid)
	}
	if sv, _ := body["schema_version"].(string); sv != "execution-plan/v1" {
		t.Errorf("ExecutionPlan fields must pass through verbatim; schema_version=%q", sv)
	}
	// The envelope must reach the router verbatim — the composer's
	// output is the only thing on the wire.
	sent, _ := fake.lastBodyJSON.Load().(map[string]any)
	if sent == nil || sent["tool_name"] != "mcp_pincher_search" || sent["session_id"] != "item5-test" {
		t.Errorf("envelope not POSTed verbatim; router saw %v", sent)
	}
	if p, _ := fake.lastPath.Load().(string); p != "/v1/route" {
		t.Errorf("proxied path = %q, want /v1/route", p)
	}
}

func TestRoute_PreV2UntaggedPlan_DefaultsToExecuteWithWarning(t *testing.T) {
	fake := defaultFakeRouter(t)
	// A v1 router's /v1/route answer: plan only, no mode, no request_id.
	fake.routeBody = `{"schema_version": "execution-plan/v1", "operative": "scout-lite", "lane": "fast"}`
	srv := newRouterToolServer(t, fake)

	req := makeReq(map[string]any{"envelope": map[string]any{"tool_name": "x", "session_id": "s"}})
	req.Params.Name = "route"
	res, _ := srv.handleRoute(context.Background(), req)
	if res.IsError {
		t.Fatalf("pre-v2 plan must degrade, not error: %s", textOf(t, res))
	}
	body := decode(t, res)
	if mode, _ := body["mode"].(string); mode != "execute" {
		t.Errorf("untagged plan must default to mode=execute; got %q", mode)
	}
	meta, _ := body["_meta"].(map[string]any)
	warnings, _ := meta["warnings"].([]any)
	found := false
	for _, w := range warnings {
		if s, _ := w.(string); strings.Contains(s, "not mode-tagged") {
			found = true
		}
	}
	if !found {
		t.Errorf("pre-v2 degradation must carry a warning; _meta=%v", meta)
	}
}

func TestRoute_Outcome_PostsToPluralOutcomes(t *testing.T) {
	fake := defaultFakeRouter(t)
	srv := newRouterToolServer(t, fake)

	card := map[string]any{
		"request_id":    "deadbeefdeadbeefdeadbeefdeadbeef",
		"outcome_class": "clean",
		"gate":          "S5",
	}
	req := makeReq(map[string]any{"action": "outcome", "outcome": card})
	req.Params.Name = "route"
	res, _ := srv.handleRoute(context.Background(), req)
	if res.IsError {
		t.Fatalf("outcome report errored: %s", textOf(t, res))
	}
	body := decode(t, res)
	if ok, _ := body["ok"].(bool); !ok {
		t.Errorf("outcomes ack not passed through: %v", body)
	}
	if p, _ := fake.lastPath.Load().(string); p != "/v1/outcomes" {
		t.Errorf("outcome must POST the PLURAL /v1/outcomes (contract v2); got %q", p)
	}
	sent, _ := fake.lastBodyJSON.Load().(map[string]any)
	if sent == nil || sent["request_id"] != "deadbeefdeadbeefdeadbeefdeadbeef" || sent["outcome_class"] != "clean" {
		t.Errorf("OutcomeCard not POSTed verbatim; router saw %v", sent)
	}
	if n := atomic.LoadInt64(&fake.outcomes); n != 1 {
		t.Errorf("expected exactly 1 outcomes post, got %d", n)
	}
}

func TestRoute_MissingEnvelopeOrOutcome_StructuredError(t *testing.T) {
	fake := defaultFakeRouter(t)
	srv := newRouterToolServer(t, fake)

	req := makeReq(map[string]any{})
	req.Params.Name = "route"
	res, _ := srv.handleRoute(context.Background(), req)
	if !res.IsError || !strings.Contains(textOf(t, res), "envelope") {
		t.Errorf("action=route without an envelope must error naming the missing arg; got %s", textOf(t, res))
	}

	req = makeReq(map[string]any{"action": "outcome"})
	req.Params.Name = "route"
	res, _ = srv.handleRoute(context.Background(), req)
	if !res.IsError || !strings.Contains(textOf(t, res), "outcome") {
		t.Errorf("action=outcome without a card must error naming the missing arg; got %s", textOf(t, res))
	}

	// Neither malformed call may reach the router.
	if n := atomic.LoadInt64(&fake.hits); n != 0 {
		t.Errorf("malformed calls dialed the router %d time(s)", n)
	}
}

// ── never-block semantics ─────────────────────────────────────────────────

func TestRouterTools_Unreachable_CleanStructuredError(t *testing.T) {
	// Grab a loopback port that is closed: start a server, note the
	// addr, close it (same trick as router_detect_test.go).
	dead := httptest.NewServer(http.NotFoundHandler())
	addr := strings.TrimPrefix(dead.URL, "http://")
	dead.Close()
	t.Setenv("PINCHER_ROUTER", "on")
	t.Setenv("PINCHER_ROUTER_ADDR", addr)
	srv, _, _ := newTestServer(t)

	start := time.Now()
	req := makeReq(map[string]any{})
	req.Params.Name = "models"
	res, err := srv.handleModels(context.Background(), req)
	if err != nil {
		t.Fatalf("handleModels must never return a Go error for a dead router: %v", err)
	}
	if !res.IsError {
		t.Fatal("dead router must produce a structured tool error")
	}
	body := decode(t, res)
	msg, _ := body["error"].(string)
	if !strings.Contains(msg, "unreachable") || !strings.Contains(msg, "originating model") {
		t.Errorf("unreachable error must name the miss and the proceed-at-originating-model fallback; got %q", msg)
	}
	if _, ok := body["_meta"].(map[string]any); !ok {
		t.Errorf("unreachable error missing the standard _meta envelope")
	}

	req = makeReq(map[string]any{"envelope": map[string]any{"tool_name": "x"}})
	req.Params.Name = "route"
	res, _ = srv.handleRoute(context.Background(), req)
	if !res.IsError || !strings.Contains(textOf(t, res), "unreachable") {
		t.Errorf("route against a dead router must produce the same structured miss; got %s", textOf(t, res))
	}

	// Closed loopback ports refuse immediately; both calls together
	// must be far inside the never-block envelope.
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("unreachable handling took %v — must be a sub-second structured miss, never a hang", elapsed)
	}
}

func TestRouterTools_SlowRouter_TimeoutHonored(t *testing.T) {
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
	}))
	t.Cleanup(hang.Close)
	t.Setenv("PINCHER_ROUTER", "on")
	t.Setenv("PINCHER_ROUTER_ADDR", strings.TrimPrefix(hang.URL, "http://"))
	srv, _, _ := newTestServer(t)

	start := time.Now()
	req := makeReq(map[string]any{"envelope": map[string]any{"tool_name": "x"}})
	req.Params.Name = "route"
	res, err := srv.handleRoute(context.Background(), req)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("handleRoute must never return a Go error for a hanging router: %v", err)
	}
	if !res.IsError {
		t.Fatal("hanging router must produce a structured tool error, not a success")
	}
	// routerCallTimeout is 250ms; allow generous CI margin but assert
	// decisively under the 2s hang — the timeout, not the router,
	// ended the call.
	if elapsed > time.Second {
		t.Errorf("route against a hanging router took %v — routerCallTimeout (%v) must bound the call", elapsed, routerCallTimeout)
	}
	t.Logf("hanging-router route bounded at %v (timeout %v)", elapsed, routerCallTimeout)
}

func TestRouterTools_EnvOff_ZeroRoutingActivity(t *testing.T) {
	// Router live and reachable, but PINCHER_ROUTER=off: the handlers
	// (still registered for HTTP /v1/<tool>) must short-circuit with a
	// structured error and generate ZERO traffic — off means no
	// routing activity of any kind, the rollback story for the whole
	// surface.
	fake := defaultFakeRouter(t)
	rt := httptest.NewServer(fake)
	t.Cleanup(rt.Close)
	t.Setenv("PINCHER_ROUTER", "off")
	t.Setenv("PINCHER_ROUTER_ADDR", strings.TrimPrefix(rt.URL, "http://"))
	srv, _, _ := newTestServer(t)

	req := makeReq(map[string]any{})
	req.Params.Name = "models"
	res, _ := srv.handleModels(context.Background(), req)
	if !res.IsError || !strings.Contains(textOf(t, res), "PINCHER_ROUTER=off") {
		t.Errorf("models under off must short-circuit naming the knob; got %s", textOf(t, res))
	}

	req = makeReq(map[string]any{"envelope": map[string]any{"tool_name": "x"}})
	req.Params.Name = "route"
	res, _ = srv.handleRoute(context.Background(), req)
	if !res.IsError || !strings.Contains(textOf(t, res), "PINCHER_ROUTER=off") {
		t.Errorf("route under off must short-circuit naming the knob; got %s", textOf(t, res))
	}

	if n := atomic.LoadInt64(&fake.hits); n != 0 {
		t.Errorf("PINCHER_ROUTER=off generated %d router request(s) — off means zero routing activity", n)
	}
}

// ── advertisement / env-override matrix ──────────────────────────────────

func TestRouterTools_AdvertisementMatrix(t *testing.T) {
	cases := []struct {
		router  string
		toolset string
		want    bool
	}{
		{"off", "", false},
		{"off", "full", false},
		{"on", "", true},
		{"on", "full", true},
		// Typos fail toward ABSENT (parseRouterEnv canonical-only).
		{"onn", "", false},
		{"enabled", "full", false},
	}
	for _, c := range cases {
		t.Run("router="+c.router+"/toolset="+c.toolset, func(t *testing.T) {
			t.Setenv("PINCHER_ROUTER", c.router)
			t.Setenv("PINCHER_TOOLSET", c.toolset)
			srv, _, _ := newTestServer(t)
			for name := range routerConditionalTools {
				if srv.mcpVisible[name] != c.want {
					t.Errorf("PINCHER_ROUTER=%s PINCHER_TOOLSET=%s: mcpVisible[%q] = %v, want %v",
						c.router, c.toolset, name, srv.mcpVisible[name], c.want)
				}
				// Registration is unconditional in every cell of the
				// matrix — the REST surface never loses the tools.
				if _, ok := srv.handlers[name]; !ok {
					t.Errorf("handlers[%q] missing under PINCHER_ROUTER=%s — registration must be unconditional", name, c.router)
				}
			}
		})
	}
}

func TestRouterTools_HTTPRouteExists_BothStates(t *testing.T) {
	// REST parity: /v1/models and /v1/route dispatch through the
	// generic /v1/<tool> route in BOTH detection states (the surface
	// convention every other tool follows — conditional advertisement
	// is MCP-only).
	for _, mode := range []string{"off", "on"} {
		t.Run("router="+mode, func(t *testing.T) {
			t.Setenv("PINCHER_ROUTER", mode)
			srv, _, _ := newTestServer(t)
			for name := range routerConditionalTools {
				rr := httptest.NewRecorder()
				body := strings.NewReader(`{"action":"nonsense"}`)
				httpReq := httptest.NewRequest(http.MethodPost, "/v1/"+name, body)
				httpReq.Header.Set("Content-Type", "application/json")
				srv.ServeHTTP(rr, httpReq)
				if rr.Code == http.StatusNotFound {
					t.Errorf("POST /v1/%s returned 404 under PINCHER_ROUTER=%s — the HTTP surface must keep the tool registered", name, mode)
				}
			}
		})
	}
}
