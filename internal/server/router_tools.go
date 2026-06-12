// SPDX-License-Identifier: MIT

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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

	// routeEchoCacheCap bounds the request_id → echo-fields cache (item
	// B10 pincher half). 64 in-flight routed task units is far beyond any
	// real loop's concurrency; the cache exists for the route → gate →
	// outcome window of a session — older entries evict LRU-first and a
	// miss just passes the card through unchanged.
	routeEchoCacheCap = 64

	// routeEchoPersistFile is the data-dir-keyed JSON sidecar that makes
	// the cache survive a process respawn (#2036, the durability half of
	// #2032). The #2033 root cause: the LRU is in-process, so an
	// auto-restart-on-drift respawn (auto_restart.go) — or a crash, or an
	// MCP client reconnect — between a same-session route and its outcome
	// wipes the cache and the minimal card 422s, invisibly to the caller.
	// Persisting the bounded (≤routeEchoCacheCap) map to one small JSON
	// file beside the SQLite store closes that gap with no new dependency
	// and no schema migration: the next process loads it on New() and the
	// post-respawn outcome card auto-fills as if no restart happened. The
	// file is best-effort throughout — any read/write error degrades to
	// the pre-#2036 in-process-only behaviour (echo_source:none + the
	// honest 422), never an error or a stall.
	routeEchoPersistFile = "route_echo_cache.json"
)

// Outcome auto-echo (router-loop item B10, pincher half). Measured
// dogfood finding (request_id 1d41c9e4): the router's OutcomeBody
// requires the full envelope/plan echo — session_id, tool_name,
// complexity_tier, role, routed_model, lane (and carries tokens_used)
// — but the dispatch verse promises a minimal card {request_id,
// outcome_class, gate}, so verse-faithful outcome reports 422'd five
// times in one session and the GBT starved. The proxy is the one
// component that SAW the original route call, so it remembers: each
// successful action="route" caches request_id → echo fields (envelope
// fields from the request + plan fields from the response), and
// action="outcome" fills any of those keys MISSING from the card
// before POSTing /v1/outcomes.
//
// Rules (all tested):
//   - Explicit caller-supplied fields always win — only absent keys
//     are filled.
//   - Cache miss (fresh session, evicted entry, foreign request_id) ⇒
//     the card passes through unchanged and any router 422 surfaces
//     honestly — the proxy never invents echo values it didn't see.
//   - routed_model is derived the way the router itself derives it
//     (router_service_server.py: plan.runtime_model or plan.model):
//     response `routed_model`, else `runtime_model`, else `model`.
//   - `mode` is cached for nothing: OutcomeBody has no mode field and
//     the proxy never injects keys the contract doesn't name.

// routeEchoFields lists the OutcomeBody echo keys the proxy auto-fills,
// split by source. Envelope keys are read from the route request;
// lane comes from the ExecutionPlan response (routed_model is derived,
// see routeEchoFromPlan).
var routeEchoEnvelopeKeys = []string{
	"session_id", "tool_name", "complexity_tier", "role", "tokens_used",
}

// routeEchoRequiredKeys are the OutcomeBody echo keys a validating
// router demands (see the comment at the head of this file: the
// dogfood 422 listed session_id, tool_name, complexity_tier, role,
// routed_model, lane). tokens_used is carried but not gate-required, so
// it is NOT in this set. A card carrying all of these is a body the
// router accepts on its own — only then may echo_source claim "caller"
// without a cache hit (#2036 MED-2).
var routeEchoRequiredKeys = []string{
	"session_id", "tool_name", "complexity_tier", "role", "routed_model", "lane",
}

// routeEchoCardComplete reports whether the OutcomeCard already carries
// every required echo key with a non-empty value — i.e. the caller
// echoed the full envelope itself and the router will not 422 it. Used
// to decide echo_source="caller" on a cache miss honestly.
func routeEchoCardComplete(card map[string]any) bool {
	for _, k := range routeEchoRequiredKeys {
		v, ok := card[k]
		if !ok || v == nil {
			return false
		}
		if s, isStr := v.(string); isStr && s == "" {
			return false
		}
	}
	return true
}

// routeEchoCache is a mutex-guarded bounded LRU of request_id → echo
// fields. Zero value is ready to use (lazy map init); safe for
// concurrent handlers. When persistPath is set (server start, see
// New) the LRU is mirrored to a JSON sidecar so it survives a process
// respawn (#2036) — load() seeds it on startup, put() flushes it after
// each write. persistPath == "" keeps the pure in-process behaviour
// (every test that does not opt into persistence, and PINCHER_ROUTER=off
// where there is no routing activity to remember).
type routeEchoCache struct {
	mu          sync.Mutex
	entries     map[string]map[string]any
	order       []string // front = least recently used
	persistPath string   // "" ⇒ in-process only (no sidecar)
}

// routeEchoSnapshot is the on-disk shape: the LRU order plus the
// fields, so recency survives the respawn too (a reload that lost order
// would evict the wrong entry on the next put). One file, rewritten
// whole on each put — the map is bounded at routeEchoCacheCap so the
// write is a few KB at most.
type routeEchoSnapshot struct {
	Order   []string                  `json:"order"`
	Entries map[string]map[string]any `json:"entries"`
}

// enablePersistence points the cache at a data-dir-keyed sidecar and
// seeds it from any prior process's file. Best-effort: a missing or
// corrupt file just starts the cache empty (the pre-#2036 cold-start
// shape). Called once from New() before any handler can run, so no lock
// is contended; it still takes c.mu to satisfy the race detector.
func (c *routeEchoCache) enablePersistence(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.persistPath = path
	c.loadLocked()
}

// loadLocked reads the sidecar into the in-memory LRU. Caller holds
// c.mu. Any error (absent file, bad JSON, partial write) leaves the
// cache empty — persistence is never allowed to fail startup.
func (c *routeEchoCache) loadLocked() {
	if c.persistPath == "" {
		return
	}
	raw, err := os.ReadFile(c.persistPath)
	if err != nil {
		return // absent on first run, or unreadable — start empty
	}
	var snap routeEchoSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		slog.Warn("pincher.route_echo.persist_load_corrupt",
			"path", c.persistPath, "err", err,
			"hint", "ignoring the sidecar and starting the echo cache empty; it will be rewritten on the next route consult")
		return
	}
	if snap.Entries == nil {
		return
	}
	c.entries = make(map[string]map[string]any, routeEchoCacheCap)
	c.order = c.order[:0]
	// Rebuild order from the persisted slice, keeping only ids that have
	// a matching entry (defends against a hand-edited or truncated file)
	// and dropping anything beyond the cap (oldest-first). A `seen` set
	// dedups (#2036 LOW-4): a hand-edited or buggy sidecar with the same
	// id twice in Order must not append it twice — that would make
	// len(order) > len(entries), so the cap loop below evicts a LIVE
	// entry while a phantom duplicate slot survives. order must stay a
	// true permutation of entries.
	seen := make(map[string]struct{}, len(snap.Order))
	for _, id := range snap.Order {
		if _, dup := seen[id]; dup {
			continue
		}
		if fields, ok := snap.Entries[id]; ok {
			seen[id] = struct{}{}
			c.entries[id] = fields
			c.order = append(c.order, id)
		}
	}
	for len(c.order) > routeEchoCacheCap {
		delete(c.entries, c.order[0])
		c.order = c.order[1:]
	}
}

// saveLocked rewrites the sidecar from the current LRU. Caller holds
// c.mu. Atomic via temp-file + rename so a crash mid-write never leaves
// a half-written file the next process would choke on. Best-effort: a
// write failure logs once and leaves the in-memory cache authoritative
// for this process (the durability guarantee is lost, the correctness
// of this process's own session is not).
func (c *routeEchoCache) saveLocked() {
	if c.persistPath == "" {
		return
	}
	snap := routeEchoSnapshot{Order: c.order, Entries: c.entries}
	raw, err := json.Marshal(snap)
	if err != nil {
		return // map[string]any of scalars — should never fail
	}
	// Unique temp per write (#2036 MED-1): two processes share one
	// data-dir sidecar (server.go enablePersistence), so a FIXED
	// c.persistPath+".tmp" would have both writing the same file and
	// racing renames — the loser's os.Remove deletes the winner's temp,
	// leaving a torn or absent target. os.CreateTemp mints a distinct
	// name per write (route_echo_cache.<rand>.tmp), so each writer owns
	// its temp; only the rename (atomic) competes, and rename is a clean
	// last-writer-wins with no shared-temp deletion.
	dir := filepath.Dir(c.persistPath)
	f, err := os.CreateTemp(dir, "route_echo_cache.*.tmp")
	if err != nil {
		slog.Warn("pincher.route_echo.persist_write_failed",
			"path", c.persistPath, "err", err,
			"hint", "the echo cache will not survive a respawn this session; a post-restart minimal outcome card may 422 (echo_source:none)")
		return
	}
	tmp := f.Name()
	if _, err := f.Write(raw); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		slog.Warn("pincher.route_echo.persist_write_failed",
			"path", c.persistPath, "err", err,
			"hint", "the echo cache will not survive a respawn this session; a post-restart minimal outcome card may 422 (echo_source:none)")
		return
	}
	if err := f.Sync(); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		slog.Warn("pincher.route_echo.persist_write_failed",
			"path", c.persistPath, "err", err,
			"hint", "fsync failed; the echo cache may not survive a respawn this session")
		return
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		slog.Warn("pincher.route_echo.persist_write_failed",
			"path", c.persistPath, "err", err)
		return
	}
	if err := os.Rename(tmp, c.persistPath); err != nil {
		slog.Warn("pincher.route_echo.persist_rename_failed",
			"path", c.persistPath, "err", err)
		_ = os.Remove(tmp)
	}
}

// put inserts (or refreshes) an entry, evicting the least recently
// used one beyond routeEchoCacheCap, then flushes the sidecar.
func (c *routeEchoCache) put(requestID string, fields map[string]any) {
	if requestID == "" || len(fields) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]map[string]any, routeEchoCacheCap)
	}
	if _, exists := c.entries[requestID]; exists {
		c.touchLocked(requestID)
	} else {
		c.order = append(c.order, requestID)
	}
	c.entries[requestID] = fields
	for len(c.order) > routeEchoCacheCap {
		evict := c.order[0]
		c.order = c.order[1:]
		delete(c.entries, evict)
	}
	c.saveLocked()
}

// get returns the cached echo fields and marks the entry recently
// used. ok=false on a miss.
func (c *routeEchoCache) get(requestID string) (map[string]any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fields, ok := c.entries[requestID]
	if ok {
		c.touchLocked(requestID)
	}
	return fields, ok
}

// touchLocked moves requestID to the most-recently-used end. Caller
// holds c.mu.
func (c *routeEchoCache) touchLocked(requestID string) {
	for i, id := range c.order {
		if id == requestID {
			c.order = append(append(c.order[:i:i], c.order[i+1:]...), requestID)
			return
		}
	}
}

// routeEchoFromCall extracts the echo fields one route consult pins:
// envelope keys from the request, lane + derived routed_model from the
// mode-tagged ExecutionPlan response. Only present, non-empty values
// are cached — the auto-fill must never write a key the proxy didn't
// actually see.
func routeEchoFromCall(envelope, plan map[string]any) map[string]any {
	fields := make(map[string]any, len(routeEchoEnvelopeKeys)+2)
	for _, k := range routeEchoEnvelopeKeys {
		if v, ok := envelope[k]; ok && v != nil && v != "" {
			fields[k] = v
		}
	}
	// routed_model: same derivation the router applies to its own
	// telemetry (plan.runtime_model or plan.model), with an explicit
	// routed_model field winning if a future contract adds one.
	for _, k := range []string{"routed_model", "runtime_model", "model"} {
		if v, ok := plan[k].(string); ok && v != "" {
			fields["routed_model"] = v
			break
		}
	}
	if v, ok := plan["lane"].(string); ok && v != "" {
		fields["lane"] = v
	}
	return fields
}

// routerRequestID normalizes a request_id value from either side of
// the outcomes join (route response, outcome card) to its string form.
// Contract v2 specifies a string, but a silent type-assertion miss
// here disables the auto-echo with no observable trace (#2032), so
// scalar shapes a JSON decoder can produce are stringified instead of
// dropped. Non-scalars stay "" — the proxy never fabricates a join key.
func routerRequestID(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case json.Number:
		return t.String()
	}
	return ""
}

// autofillOutcomeEcho completes a minimal OutcomeCard from the cached
// route call with the same request_id. Explicit caller fields always
// win; a cache miss returns (nil, false) and leaves the card untouched
// (the honest-422 path). Returns the sorted list of filled keys for
// the response note, plus whether the cache held the request_id at all
// (a hit that fills nothing means the caller echoed everything itself).
func (s *Server) autofillOutcomeEcho(card map[string]any) ([]string, bool) {
	requestID := routerRequestID(card["request_id"])
	if requestID == "" {
		return nil, false
	}
	cached, ok := s.routeEcho.get(requestID)
	if !ok {
		return nil, false
	}
	var filled []string
	for k, v := range cached {
		if _, present := card[k]; present {
			continue
		}
		card[k] = v
		filled = append(filled, k)
	}
	sort.Strings(filled)
	return filled, true
}

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
		Description: "**Consult the pincher-router before spawning a Make-stage task unit, and report the gated outcome back afterwards** (thin proxy: action=\"route\" → POST /v1/route, action=\"outcome\" → POST /v1/outcomes). The route response is mode-tagged: `mode: \"execute\"` means the router ran (or will run) the worker — treat the result as an untrusted maker artifact and send it to the gate; `mode: \"advise\"` means spawn a host subagent at the advised tier, passing the returned envelope verbatim. Every route response carries a `request_id`; after the gate verdict, report `{request_id, outcome_class: clean|errored|shallow, gate}` via action=\"outcome\" — the loop trains the router as a side effect of working, and skipping the report starves its model. The minimal card suffices: the proxy auto-fills the OutcomeBody echo (session_id, tool_name, complexity_tier, role, tokens_used, routed_model, lane) from the route call it proxied for the same request_id — explicit fields always win, and on a cache miss the card passes through unchanged. Routing NEVER blocks the loop: an unreachable or slow router returns a structured error within the call budget (~250ms) — proceed at the originating model and log the miss in the loop checkpoint. Stage policy is binding (pincher-loop dispatch verse): Make routes, Probe may route a bounded question, Frame/Decide/Capture never route, and the gate never routes below the originating tier.",
		InputSchema: json.RawMessage(`{
			"type":"object","properties":{
				"action":{"type":"string","enum":["route","outcome"],"description":"'route' (default) POSTs the envelope to /v1/route and returns the mode-tagged ExecutionPlan + request_id. 'outcome' reports a gated result to POST /v1/outcomes."},
				"envelope":{"type":"object","description":"TaskEnvelope for action='route', POSTed to the router verbatim — the envelope composer's output (intent + pointers + pre-cut slices + probe _meta features such as tool_name, complexity_tier, role, session_id), never raw files."},
				"outcome":{"type":"object","description":"OutcomeCard for action='outcome': {request_id, outcome_class: clean|errored|shallow, gate, quality_score, ...}. request_id comes from the prior route response — it is the join key the router's learner trains on; missing envelope/plan echo fields are auto-filled from that route call (explicit values win)."}
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
		// Router-loop item B11: count the consult at attempt time —
		// coach's route-consult coverage measures verse adherence (did
		// the loop ask before spawning?), and a consult against a
		// router that turns out to be unreachable is still adherence
		// (the verse's miss path). Malformed calls (no envelope) were
		// rejected above and never count.
		atomic.AddInt64(&s.statsRouteConsults, 1)
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
		// Outcome auto-echo (item B10): remember what this consult
		// looked like so the eventual minimal outcome card can be
		// completed to the full OutcomeBody echo. Keyed by the ROUTER
		// RESPONSE's request_id (the join key the outcome card carries
		// back), merged from the submitted envelope + the parsed plan.
		// Best-effort — a response without a request_id (pre-v2
		// router) caches nothing, and non-string ids are normalized
		// instead of silently skipping the write (#2032).
		if rid := routerRequestID(body["request_id"]); rid != "" {
			s.routeEcho.put(rid, routeEchoFromCall(envelope, body))
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
		// Outcome auto-echo (item B10): complete the verse's minimal
		// card from the cached route call before POSTing. Explicit
		// caller fields always win; a cache miss (fresh session,
		// evicted, foreign request_id) passes the card through
		// unchanged so a router 422 surfaces honestly.
		// Router-loop item B11: outcome reports counted at attempt
		// time, symmetrically with consults (coach's outcomes-reported
		// ÷ consults adherence ratio).
		atomic.AddInt64(&s.statsRouteOutcomes, 1)
		filled, cacheHit := s.autofillOutcomeEcho(card)
		// #2032 production observability: name where the echo came
		// from, so a live un-echoed 422 is diagnosable instead of
		// indistinguishable from the tested happy path.
		//   cache  — ≥1 field auto-filled from the cached route call;
		//   caller — nothing needed filling (cache hit with a complete
		//            card, or the caller echoed session_id itself);
		//   none   — cache miss AND no caller echo: exactly the body
		//            a validating router 422s. The dominant production
		//            cause is process churn — the request_id → echo
		//            LRU is in-memory and does not survive an MCP
		//            respawn (auto-restart-on-drift, crash, client
		//            reconnect), which the caller cannot see. Logged
		//            loudly for that reason.
		// #2036 MED-2: echo_source must not LIE. "caller" may only be
		// claimed when the card the router will actually receive is
		// COMPLETE — every required echo key present — or on a genuine
		// cache hit. The prior `callerSession != ""` test let a partial
		// card (caller supplied session_id but none of the other
		// required keys) claim "caller" AND suppress the echo_miss warn,
		// so a real un-echoed 422 was indistinguishable from the happy
		// path. A partial caller echo is a miss: echo_source "none" and
		// the warning fires, because the router will 422 it.
		echoSource := "none"
		switch {
		case len(filled) > 0:
			echoSource = "cache"
		case cacheHit || routeEchoCardComplete(card):
			echoSource = "caller"
		default:
			slog.Warn("pincher.route_outcome.echo_miss",
				"request_id", routerRequestID(card["request_id"]),
				"hint", "no cached route call for this request_id (process restart? LRU eviction? foreign id) and the card lacks the required echo keys (session_id, tool_name, complexity_tier, role, routed_model, lane) — a validating router will 422 this body; echo the envelope fields explicitly or re-route")
		}
		body, errRes := s.routerDo(ctx, http.MethodPost, "/v1/outcomes", card)
		if errRes != nil {
			return errRes, nil
		}
		if len(filled) > 0 {
			body["echo_autofilled"] = filled
		}
		body["echo_source"] = echoSource
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
