// SPDX-License-Identifier: MIT

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Conditional pincher-router tool surface (router-loop plan item B5;
// ADRs ROUTER_CONTRACT_V2 / ROUTER_DETECTION_LADDER). Two thin HTTP
// proxies against the router's contract-v2 surface (pincher-router
// PR #26):
//
//	models — GET  /v1/models   registry render + the version-skew handshake
//	route  — POST /v1/route    mode-tagged ExecutionPlan + request_id
//	         POST /v1/outcomes (route action="outcome") — the GBT feedback join
//
// Division of labor is absolute: pincher renders, the router owns
// state. Pincher NEVER writes workers.yaml — the registry has exactly
// two writers (humans for `declared`, the discovery engine for
// `discovered`) and this file is not one of them.
//
// Surface discipline (plan §A6): both tools are registered on
// s.handlers/s.tools unconditionally (HTTP /v1/<tool>, OpenAPI and the
// tool-contract golden keep the complete surface), but they appear on
// the MCP tools/list advertisement ONLY when the startup detection
// ladder found a live router (s.routerDetected — see addTool). Absent
// ⇒ zero advertised surface in BOTH toolset modes; detected ⇒ they
// join the core advertisement (a routed loop is the core use-case).
// The surface is decided at startup; liveness is re-checked per call.
//
// Never-block semantics: every proxy call is bounded by
// routerCallTimeout. Unreachable, slow, or misbehaving routers yield
// the standard structured error envelope (errResultRich) — the loop
// proceeds at the originating model and logs the miss. A routing
// consult can never hang a loop iteration; that invariant is
// cross-repo (plan §D1) and tested against a deliberately hanging
// fake router.
const (
	// routerCallTimeout bounds every models/route proxy call. The
	// router's own latency bar is p95 < 50ms / p99 < 100ms for a local
	// /v1/route with the GBT loaded (plan T6); 250ms gives 2.5×
	// headroom over the p99 bar — generous enough that a healthy
	// router never trips it, small enough that a dead one is a
	// sub-perceptual blip in the loop, never a stall.
	routerCallTimeout = 250 * time.Millisecond

	// routerContractVersion is the contract generation this proxy
	// surface is built against (pincher-router PR #26: /v1/models
	// handshake, mode-tagged /v1/route, plural /v1/outcomes). Skew
	// policy (plan §D1): handshake below this ⇒ installed-but-old hint,
	// above ⇒ treated as a v2 superset. Never a hard failure.
	routerContractVersion = 2

	// routerErrBodyMax caps how much of a router error body is echoed
	// into a structured tool error.
	routerErrBodyMax = 300
)

// routerConditionalTools names the tools whose MCP advertisement is
// gated on s.routerDetected instead of the toolset knob (plan §A6).
// Deliberately NOT part of coreToolset: membership there is
// unconditional, and the whole point of this surface is that it does
// not exist — zero tokens, zero tools — on machines without a router.
var routerConditionalTools = map[string]bool{
	"models": true,
	"route":  true,
}

// routerToolClient is the shared HTTP client for the proxy calls.
// Timeout is enforced per-call via context (routerCallTimeout) so a
// caller-supplied shorter deadline also wins. Redirects are not
// followed — same probe hygiene as the detection ladder (the
// port-11000 dashboard fixture).
var routerToolClient = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// registerRouterTools registers models + route. Called from
// registerTools; advertisement gating happens in addTool.
func (s *Server) registerRouterTools() {
	s.addTool(&mcp.Tool{
		Name:        "models",
		Description: "**Render the pincher-router worker registry** (thin proxy over the router's GET /v1/models — read-only, no registry writes ever happen from pincher). Returns the contract-v2 handshake `{contract_version, weights_version, registry_version, capabilities}` — the single version-skew mechanism between pincher and the router — plus every provider/model entry exactly as the router serves it (tier, kind, ctx_window, cost, source, enabled, enabled_by, last_seen, capabilities). A router answering with `contract_version` below 2 still gets rendered, with a `hint` field saying the router is installed but too old — installed-but-old is a state, not an error, and the surface never vanishes mid-session. `enable`/`disable`/`test` actions are declared for shape-stability but answer with a structured error until the router contract exposes registry-mutation endpoints (the discovery train): the router owns all registry state. Only advertised over MCP when a live router was detected at startup (`router` ∈ _meta.capabilities); always reachable over HTTP /v1/models.",
		InputSchema: json.RawMessage(`{
			"type":"object","properties":{
				"action":{"type":"string","enum":["list","enable","disable","test"],"description":"'list' (default) renders GET /v1/models: handshake + registry. 'enable'/'disable'/'test' are reserved by the routing plan and answer with a structured error until the router contract exposes registry mutation — pincher never writes workers.yaml itself."},
				"model":{"type":"string","description":"Target for enable/disable/test as 'provider/model_id'. Ignored by list."}
			}
		}`),
	}, s.handleModels)

	s.addTool(&mcp.Tool{
		Name:        "route",
		Description: "**Consult the pincher-router before spawning a Make-stage task unit, and report the gated outcome back afterwards** (thin proxy: action=\"route\" → POST /v1/route, action=\"outcome\" → POST /v1/outcomes). The route response is mode-tagged: `mode: \"execute\"` means the router ran (or will run) the worker — treat the result as an untrusted maker artifact and send it to the gate; `mode: \"advise\"` means spawn a host subagent at the advised tier, passing the returned envelope verbatim. Every route response carries a `request_id`; after the gate verdict, report `{request_id, outcome_class: clean|errored|shallow, gate}` via action=\"outcome\" — the loop trains the router as a side effect of working, and skipping the report starves its model. Routing NEVER blocks the loop: an unreachable or slow router returns a structured error within the call budget (~250ms) — proceed at the originating model and log the miss in the loop checkpoint. Stage policy is binding (pincher-loop dispatch verse): Make routes, Probe may route a bounded question, Frame/Decide/Capture never route, and the gate never routes below the originating tier.",
		InputSchema: json.RawMessage(`{
			"type":"object","properties":{
				"action":{"type":"string","enum":["route","outcome"],"description":"'route' (default) POSTs the envelope to /v1/route and returns the mode-tagged ExecutionPlan + request_id. 'outcome' reports a gated result to POST /v1/outcomes."},
				"envelope":{"type":"object","description":"TaskEnvelope for action='route', POSTed to the router verbatim — the envelope composer's output (intent + pointers + pre-cut slices + probe _meta features such as tool_name, complexity_tier, role, session_id), never raw files."},
				"outcome":{"type":"object","description":"OutcomeCard for action='outcome': {request_id, outcome_class: clean|errored|shallow, gate, quality_score, ...}. request_id comes from the prior route response — it is the join key the router's learner trains on."}
			}
		}`),
	}, s.handleRoute)
}

// handleModels proxies the router registry (action=list). Mutation
// actions are reserved: contract v2 (PR #26) exposes no registry-
// mutation endpoints, and pincher never writes workers.yaml.
func (s *Server) handleModels(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	start, tool, args := beginCall(req)
	if res := s.routerOffError(); res != nil {
		return res, nil
	}
	action := str(args, "action")
	if action == "" {
		action = "list"
	}
	switch action {
	case "list":
		body, errRes := s.routerDo(ctx, http.MethodGet, "/v1/models", nil)
		if errRes != nil {
			return errRes, nil
		}
		// Version skew (plan §D1): below-v2 handshakes render with an
		// upgrade hint instead of erroring or vanishing; above-v2 is
		// treated as a v2 superset and rendered as-is.
		if v, ok := routerHandshakeVersion(body); ok && v < routerContractVersion {
			body["hint"] = fmt.Sprintf(
				"router installed, contract too old (v%d < v%d) — upgrade pincher-router; this surface is built against contract v%d (/v1/models handshake, mode-tagged /v1/route, plural /v1/outcomes)",
				v, routerContractVersion, routerContractVersion)
		}
		return s.jsonResultWithMeta(body, start, tool, args, 0), nil
	case "enable", "disable", "test":
		return s.errResultRich(fmt.Sprintf(
			"models action %q is reserved: router contract v2 exposes no registry-mutation endpoints yet, and pincher never writes workers.yaml (the router owns all registry state — humans own declared entries, the discovery engine owns discovered ones). Manage entries with the pincher-router tooling until the discovery train ships the mutation surface.",
			action), []map[string]string{
			{"tool": "models", "args": `{"action":"list"}`,
				"why": "render the current registry + handshake — the read surface contract v2 does define"},
		}), nil
	default:
		return s.errResultRich(fmt.Sprintf("unknown models action %q — valid: list (default), enable, disable, test", action),
			[]map[string]string{
				{"tool": "models", "args": `{"action":"list"}`,
					"why": "list is the default and only live action under router contract v2"},
			}), nil
	}
}

// handleRoute proxies the route consult (action=route) and the
// outcomes report (action=outcome).
func (s *Server) handleRoute(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	start, tool, args := beginCall(req)
	if res := s.routerOffError(); res != nil {
		return res, nil
	}
	action := str(args, "action")
	if action == "" {
		action = "route"
	}
	switch action {
	case "route":
		envelope, ok := args["envelope"].(map[string]any)
		if !ok {
			return s.errResultRich("route requires an `envelope` object — the TaskEnvelope POSTed verbatim to the router's /v1/route (intent + pointers + pre-cut slices + probe _meta features; never raw files)",
				[]map[string]string{
					{"tool": "route", "args": `{"envelope":{"tool_name":"...","complexity_tier":"lite","role":"explore","session_id":"..."}}`,
						"why": "the envelope carries the features the router's model decides on — an empty consult is feature-starved by construction"},
				}), nil
		}
		body, errRes := s.routerDo(ctx, http.MethodPost, "/v1/route", envelope)
		if errRes != nil {
			return errRes, nil
		}
		// Pre-v2 routers answer /v1/route without the mode tag. Skew
		// policy: treat as EXECUTE (the only mode that existed) and
		// say so, rather than handing the caller an untagged plan.
		if _, ok := body["mode"]; !ok {
			body["mode"] = "execute"
			meta, _ := body["_meta"].(map[string]any)
			if meta == nil {
				meta = map[string]any{}
				body["_meta"] = meta
			}
			meta["warnings"] = []string{
				"router response was not mode-tagged (pre-contract-v2 router) — treated as mode=execute; upgrade pincher-router for ADVISE support and the request_id outcomes join",
			}
		}
		return s.jsonResultWithMeta(body, start, tool, args, 0), nil
	case "outcome":
		card, ok := args["outcome"].(map[string]any)
		if !ok {
			return s.errResultRich("route action=outcome requires an `outcome` object — the OutcomeCard POSTed to /v1/outcomes: {request_id, outcome_class: clean|errored|shallow, gate, ...}",
				[]map[string]string{
					{"tool": "route", "args": `{"action":"outcome","outcome":{"request_id":"<from the route response>","outcome_class":"clean","gate":"S5"}}`,
						"why": "request_id joins the outcome to the routing decision — the router's learner trains on this row"},
				}), nil
		}
		body, errRes := s.routerDo(ctx, http.MethodPost, "/v1/outcomes", card)
		if errRes != nil {
			return errRes, nil
		}
		return s.jsonResultWithMeta(body, start, tool, args, 0), nil
	default:
		return s.errResultRich(fmt.Sprintf("unknown route action %q — valid: route (default), outcome", action),
			[]map[string]string{
				{"tool": "route", "args": `{"envelope":{}}`,
					"why": "route is the default action: consult before spawning the task unit"},
			}), nil
	}
}

// routerOffError short-circuits the proxy handlers when
// PINCHER_ROUTER=off. Off is the rollback story for the entire routing
// surface and means zero routing activity of any kind — no probes at
// startup (router_detect.go) and no proxy dials per call, even for a
// direct HTTP /v1/models|route request that bypasses the (absent) MCP
// advertisement. Returns nil in auto/on modes: the surface is decided
// at startup, liveness is per-call.
func (s *Server) routerOffError() *mcp.CallToolResult {
	if s.routerMode != routerModeOff {
		return nil
	}
	return s.errResultRich("routing is disabled (PINCHER_ROUTER=off) — off means zero routing activity: no detection probes, no proxy calls. Unset PINCHER_ROUTER (or set auto) and restart the server to re-enable.", nil)
}

// routerDo performs one bounded proxy call against the router and
// decodes the JSON body. Every failure mode returns the standard
// structured error envelope (never an error from the handler, never a
// hang): the cross-repo invariant is that routing can not block the
// loop, so the error text always tells the caller to proceed at the
// originating model.
func (s *Server) routerDo(ctx context.Context, method, path string, payload any) (map[string]any, *mcp.CallToolResult) {
	ctx, cancel := context.WithTimeout(ctx, routerCallTimeout)
	defer cancel()
	var bodyReader io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, s.errResultRich(fmt.Sprintf("could not encode the %s body: %v", path, err), nil)
		}
		bodyReader = bytes.NewReader(b)
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, s.routerBaseURL+path, bodyReader)
	if err != nil {
		return nil, s.routerMissError(path, err)
	}
	if payload != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	resp, err := routerToolClient.Do(httpReq)
	if err != nil {
		return nil, s.routerMissError(path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, s.routerMissError(path, err)
	}
	if resp.StatusCode != http.StatusOK {
		snippet := strings.TrimSpace(string(raw))
		if len(snippet) > routerErrBodyMax {
			snippet = snippet[:routerErrBodyMax] + "…"
		}
		return nil, s.errResultRich(fmt.Sprintf(
			"pincher-router answered %s %s%s with HTTP %d: %s — routing never blocks the loop: proceed at the originating model and log the miss in the loop checkpoint",
			method, s.routerBaseURL, path, resp.StatusCode, snippet), nil)
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, s.errResultRich(fmt.Sprintf(
			"pincher-router answered %s%s with a non-JSON body (%v) — something other than a router may be on this address; routing never blocks the loop: proceed at the originating model",
			s.routerBaseURL, path, err), nil)
	}
	if parsed == nil {
		parsed = map[string]any{}
	}
	return parsed, nil
}

// routerMissError is the structured envelope for unreachable/slow
// routers (connection refused, DNS, timeout). Deliberately one shape
// for all of them: from the loop's perspective they are the same event
// — a routing miss to checkpoint, never a stall.
func (s *Server) routerMissError(path string, err error) *mcp.CallToolResult {
	return s.errResultRich(fmt.Sprintf(
		"pincher-router unreachable (%s%s, budget %s): %v — routing never blocks the loop: proceed at the originating model and log the miss in the loop checkpoint",
		s.routerBaseURL, path, routerCallTimeout, err), []map[string]string{
		{"tool": "health", "args": `{}`,
			"why": "check _meta.capabilities — if `router` is advertised the service was alive at startup and this miss is transient; if not, the surface should not have been called at all"},
	})
}

// routerHandshakeVersion extracts handshake.contract_version from a
// /v1/models body. ok=false when absent/malformed — absence is not
// skew evidence (don't hint on bodies we can't read).
func routerHandshakeVersion(body map[string]any) (int, bool) {
	hs, ok := body["handshake"].(map[string]any)
	if !ok {
		return 0, false
	}
	v, ok := hs["contract_version"].(float64)
	if !ok {
		return 0, false
	}
	return int(v), true
}
