// SPDX-License-Identifier: MIT

package server

// routing_adoption_b11_test.go — guide/coach routing integration
// (router-loop plan item B11, §A4), dual-state like the #2021 B5
// goldens: with a router detected, guide appends a `route` consult to
// Make-shaped recommendations and coach grows a routing-adoption
// section plus the unrouted_task_spawns finding; with the router
// absent, BOTH responses are byte-identical to the pre-routing
// surface (plan §A6 zero-surface applies to response text, not just
// the tools/list advertisement).
//
// Detection state is forced per-test via PINCHER_ROUTER=on|off (the
// item-B4 override — zero network); the package-wide TestMain default
// is `off`, so nothing here depends on the host machine.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// callGuide invokes handleGuide and decodes the body.
func callGuide(t *testing.T, srv *Server, task string) map[string]any {
	t.Helper()
	res, err := srv.handleGuide(context.Background(), makeReq(map[string]any{"task": task}))
	if err != nil {
		t.Fatalf("handleGuide: %v", err)
	}
	if res.IsError {
		t.Fatalf("handleGuide returned error result: %s", textOf(t, res))
	}
	return decode(t, res)
}

func guideRecs(t *testing.T, body map[string]any) []map[string]any {
	t.Helper()
	raw, _ := body["recommended_next_tools"].([]any)
	recs := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		m, _ := r.(map[string]any)
		recs = append(recs, m)
	}
	return recs
}

// TestGuide_RouterDualState_MakeShapedTasks pins the §A4 contract from
// both sides at once: for Make-shaped tasks the router-present
// recommendation list is EXACTLY the router-absent list plus one
// appended `route` consult — nothing else may differ (the byte-identity
// half of the dual-state discipline, asserted by JSON equality on the
// shared prefix), and the absent state never names `route` at all.
func TestGuide_RouterDualState_MakeShapedTasks(t *testing.T) {
	makeTasks := []string{
		"implement INI file support in the indexer",                             // shapeAdd
		"add support for symlinked corpora",                                     // shapeAdd
		"refactor the error handling in flushSession to extract a retry helper", // shapeRefactor
		"apply this mechanical rename across all call sites",                    // shapeRefactor
	}

	t.Setenv("PINCHER_ROUTER", "on")
	present, _, _ := newTestServer(t)
	t.Setenv("PINCHER_ROUTER", "off")
	absent, _, _ := newTestServer(t)

	for _, task := range makeTasks {
		pRecs := guideRecs(t, callGuide(t, present, task))
		aRecs := guideRecs(t, callGuide(t, absent, task))

		if len(pRecs) != len(aRecs)+1 {
			t.Fatalf("task %q: present has %d recs, absent %d — want exactly one appended route consult",
				task, len(pRecs), len(aRecs))
		}
		last := pRecs[len(pRecs)-1]
		if last["tool"] != "route" {
			t.Errorf("task %q: present last rec = %v, want tool \"route\" appended last", task, last["tool"])
		}
		args, _ := last["args"].(string)
		if !strings.Contains(args, "envelope") {
			t.Errorf("task %q: route rec args must template a TaskEnvelope, got %q", task, args)
		}
		why, _ := last["why"].(string)
		for _, want := range []string{"Make-shaped", "mode=execute", "mode=advise", "never blocks", `action="outcome"`} {
			if !strings.Contains(why, want) {
				t.Errorf("task %q: route rec why missing %q:\n%s", task, want, why)
			}
		}

		// Byte-identity of the shared prefix: present minus the
		// appended consult IS the absent list.
		pPrefix, _ := json.Marshal(pRecs[:len(pRecs)-1])
		aAll, _ := json.Marshal(aRecs)
		if string(pPrefix) != string(aAll) {
			t.Errorf("task %q: router presence changed more than the appended route rec:\n present-prefix: %s\n absent:        %s",
				task, pPrefix, aAll)
		}
		// And the absent state never names route anywhere.
		for _, r := range aRecs {
			if r["tool"] == "route" || r["tool"] == "models" {
				t.Errorf("task %q: router-absent guide recommends %v — zero-surface-when-absent violated", task, r["tool"])
			}
		}
	}
}

// TestGuide_RouterPresent_NonMakeShapesStayUnrouted pins the stage
// policy (plan §A1 table): Probe/understand/audit/fix/caller shapes do
// not get a route consult even with a live router — Make is the routed
// surface, and guide must not push the verse past it.
func TestGuide_RouterPresent_NonMakeShapesStayUnrouted(t *testing.T) {
	t.Setenv("PINCHER_ROUTER", "on")
	srv, _, _ := newTestServer(t)

	tasks := []string{
		"fix the login retry bug",                  // shapeFix — hypothesis formation never routes
		"who calls flushSession",                   // shapeTraceIn
		"understand how indexing handles symlinks", // shapeUnderstand
		"find every handler without a test",        // shapeAudit
		"review my diff before commit",             // shapeReview
	}
	for _, task := range tasks {
		for _, r := range guideRecs(t, callGuide(t, srv, task)) {
			if r["tool"] == "route" {
				t.Errorf("task %q: route recommended for a non-Make shape — stage policy violated", task)
			}
		}
	}
}

// TestGuide_RouterPresent_CoreToolset_NoFalseEscapeHatchNote pins the
// #2013 discipline: `route` is advertisement-gated on detection, not
// on the toolset knob — when detected it joins the core advertisement
// in every toolset mode, so guide's core-toolset escape-hatch note
// ("not on the core MCP toolset — restart with PINCHER_TOOLSET=full")
// would be FALSE for it and must not be attached.
func TestGuide_RouterPresent_CoreToolset_NoFalseEscapeHatchNote(t *testing.T) {
	t.Setenv("PINCHER_ROUTER", "on")
	t.Setenv("PINCHER_TOOLSET", "core") // opt-in core surface (#2054 made full the default)
	srv, _, _ := newTestServer(t)

	recs := guideRecs(t, callGuide(t, srv, "implement INI file support in the indexer"))
	var route map[string]any
	for _, r := range recs {
		if r["tool"] == "route" {
			route = r
		}
	}
	if route == nil {
		t.Fatal("router present + Make-shaped task: no route recommendation")
	}
	why, _ := route["why"].(string)
	if strings.Contains(why, "not on the core MCP toolset") {
		t.Errorf("route rec carries the core-toolset escape-hatch note, but route IS advertised when detected:\n%s", why)
	}
}

// seedRoutingTelemetry writes the shared fixture: 10 neutral tool-call
// events (no burst/heavy patterns — those prices depend on the
// capability slice, which differs across router states), three
// hook-observed Task spawns (one of them the advise_route advisory
// row), keyed to the given session id.
func seedRoutingTelemetry(t *testing.T, srv *Server, store *db.Store, sid string) {
	t.Helper()
	events := make([]db.ToolCallEvent, 10)
	for i := range events {
		events[i] = db.ToolCallEvent{Tool: "guide", TokensUsed: 1000}
	}
	seedCoachEvents(t, srv, events)
	now := time.Now().UnixNano()
	for i, dec := range []string{"pass_through", "pass_through", "advise_route"} {
		if err := store.LogHookInvocation(db.HookInvocation{
			TS: now + int64(i), SessionID: sid, ToolName: "Task", FilePath: sid, Decision: dec,
		}); err != nil {
			t.Fatalf("LogHookInvocation: %v", err)
		}
	}
}

// TestCoach_RouterPresent_RoutingSectionAndVerseSkipFinding pins the
// detected-state coach surface: the routing section carries the A1
// coverage metric with the live consult/outcome split, and the
// shortfall versus hook-observed spawns is flagged as the counts-only
// unrouted_task_spawns finding.
func TestCoach_RouterPresent_RoutingSectionAndVerseSkipFinding(t *testing.T) {
	t.Setenv("PINCHER_ROUTER", "on")
	srv, store, _ := newTestServer(t)
	seedRoutingTelemetry(t, srv, store, srv.persistentSessionID)
	atomic.StoreInt64(&srv.statsRouteConsults, 2)
	atomic.StoreInt64(&srv.statsRouteOutcomes, 1)

	body := callCoach(t, srv, map[string]any{})

	routing, _ := body["routing"].(map[string]any)
	if routing == nil {
		t.Fatalf("router present but coach response has no routing section: %v", body)
	}
	wantInts := map[string]float64{
		"route_consults":          2,
		"outcome_reports":         1,
		"task_spawns_observed":    3,
		"advise_route_advisories": 1,
		"route_tool_calls":        0, // no persisted route rows in this fixture
	}
	for k, want := range wantInts {
		if got, _ := routing[k].(float64); got != want {
			t.Errorf("routing.%s = %v, want %v", k, routing[k], want)
		}
	}
	if got, _ := routing["route_consult_coverage"].(float64); got != 0.67 {
		t.Errorf("route_consult_coverage = %v, want 0.67 (2 consults ÷ 3 spawns, rounded)", routing["route_consult_coverage"])
	}
	basis, _ := routing["basis"].(string)
	for _, want := range []string{"attempt", "Make-stage task units", "closest recorded proxy"} {
		if !strings.Contains(basis, want) {
			t.Errorf("routing basis missing %q — every approximation must be named:\n%s", want, basis)
		}
	}

	f := findingByPattern(t, body, "unrouted_task_spawns")
	if f == nil {
		t.Fatalf("expected unrouted_task_spawns finding (3 spawns vs 2 consults); findings: %v", body["findings"])
	}
	if got, _ := f["occurrences"].(float64); got != 1 {
		t.Errorf("occurrences = %v, want 1", f["occurrences"])
	}
	if got, _ := f["est_tokens_left_on_table"].(float64); got != 0 {
		t.Errorf("est_tokens_left_on_table = %v, want 0 — counts-only, never an invented number", f["est_tokens_left_on_table"])
	}
	fb, _ := f["basis"].(string)
	if !strings.Contains(fb, "counts-only") {
		t.Errorf("verse-skip basis must say counts-only:\n%s", fb)
	}
}

// TestCoach_RouterPresent_FullCoverage_NoVerseSkipFinding: consults ≥
// spawns means no finding — coach prices shortfalls, not adherence.
func TestCoach_RouterPresent_FullCoverage_NoVerseSkipFinding(t *testing.T) {
	t.Setenv("PINCHER_ROUTER", "on")
	srv, store, _ := newTestServer(t)
	seedRoutingTelemetry(t, srv, store, srv.persistentSessionID)
	atomic.StoreInt64(&srv.statsRouteConsults, 3)

	body := callCoach(t, srv, map[string]any{})
	if f := findingByPattern(t, body, "unrouted_task_spawns"); f != nil {
		t.Errorf("3 consults ÷ 3 spawns is full coverage — no finding expected, got %v", f)
	}
	routing, _ := body["routing"].(map[string]any)
	if got, _ := routing["route_consult_coverage"].(float64); got != 1 {
		t.Errorf("route_consult_coverage = %v, want 1", routing["route_consult_coverage"])
	}
}

// TestCoach_RouterAbsent_ZeroRoutingSurface is the absent-state twin:
// identical telemetry (including stale live counters), router off —
// the response must carry NO routing section, NO verse-skip finding,
// and exactly the pre-routing key set. Zero-surface-when-absent for
// response text.
func TestCoach_RouterAbsent_ZeroRoutingSurface(t *testing.T) {
	t.Setenv("PINCHER_ROUTER", "off")
	srv, store, _ := newTestServer(t)
	seedRoutingTelemetry(t, srv, store, srv.persistentSessionID)
	atomic.StoreInt64(&srv.statsRouteConsults, 2)
	atomic.StoreInt64(&srv.statsRouteOutcomes, 1)

	res, err := srv.handleCoach(context.Background(), makeReq(map[string]any{}))
	if err != nil {
		t.Fatalf("handleCoach: %v", err)
	}
	raw := textOf(t, res)
	body := decode(t, res)

	if _, ok := body["routing"]; ok {
		t.Errorf("router absent but coach response carries a routing section: %v", body["routing"])
	}
	if f := findingByPattern(t, body, "unrouted_task_spawns"); f != nil {
		t.Errorf("router absent but verse-skip finding emitted: %v", f)
	}
	for _, leak := range []string{"routing", "route_consult", "unrouted_task_spawns", "pincher-router"} {
		if strings.Contains(raw, leak) {
			t.Errorf("router-absent coach response leaks %q — must be byte-identical to the pre-routing surface", leak)
		}
	}
	// Exactly the pre-routing top-level keys.
	for k := range body {
		switch k {
		case "window", "calls_analyzed", "findings", "note", "_meta":
		default:
			t.Errorf("router-absent coach response has unexpected top-level key %q", k)
		}
	}
}

// TestCoach_7dWindow_RoutingCoverageIsUpperBound pins the documented
// 7d degradation: persisted route rows can't split consults from
// outcome reports, so the section omits the live-counter keys, uses
// the combined count as the coverage numerator, and says UPPER bound
// in the basis.
func TestCoach_7dWindow_RoutingCoverageIsUpperBound(t *testing.T) {
	t.Setenv("PINCHER_ROUTER", "on")
	srv, store, _ := newTestServer(t)
	events := []db.ToolCallEvent{
		{Tool: "route", TokensUsed: 200},
		{Tool: "route", TokensUsed: 200},
	}
	for i := 0; i < 8; i++ {
		events = append(events, db.ToolCallEvent{Tool: "guide", TokensUsed: 1000})
	}
	seedCoachEvents(t, srv, events)
	now := time.Now().UnixNano()
	for i, dec := range []string{"pass_through", "pass_through", "advise_route"} {
		if err := store.LogHookInvocation(db.HookInvocation{
			TS: now + int64(i), SessionID: "host-sess-b11", ToolName: "Task", FilePath: "host-sess-b11", Decision: dec,
		}); err != nil {
			t.Fatalf("LogHookInvocation: %v", err)
		}
	}

	body := callCoach(t, srv, map[string]any{"window": "7d"})
	routing, _ := body["routing"].(map[string]any)
	if routing == nil {
		t.Fatalf("router present but 7d coach response has no routing section: %v", body)
	}
	if got, _ := routing["route_tool_calls"].(float64); got != 2 {
		t.Errorf("route_tool_calls = %v, want 2", routing["route_tool_calls"])
	}
	for _, absent := range []string{"route_consults", "outcome_reports"} {
		if _, ok := routing[absent]; ok {
			t.Errorf("7d window must omit %s — the per-action split exists only for the live session", absent)
		}
	}
	if got, _ := routing["task_spawns_observed"].(float64); got != 3 {
		t.Errorf("task_spawns_observed = %v, want 3 (ts-windowed, any session)", routing["task_spawns_observed"])
	}
	if got, _ := routing["route_consult_coverage"].(float64); got != 0.67 {
		t.Errorf("route_consult_coverage = %v, want 0.67 (2 route calls ÷ 3 spawns)", routing["route_consult_coverage"])
	}
	if basis, _ := routing["basis"].(string); !strings.Contains(basis, "UPPER bound") {
		t.Errorf("7d basis must say the coverage is an UPPER bound:\n%s", basis)
	}
	// Verse-skip count uses the upper-bound numerator — an
	// under-estimate, the conservative direction.
	f := findingByPattern(t, body, "unrouted_task_spawns")
	if f == nil {
		t.Fatal("expected unrouted_task_spawns finding (3 spawns vs 2 route calls)")
	}
	if got, _ := f["occurrences"].(float64); got != 1 {
		t.Errorf("occurrences = %v, want 1", f["occurrences"])
	}
}

// TestHandleRoute_AdoptionCountersIncrementAtAttemptTime pins the
// counter semantics coach's session numbers rest on: consults and
// outcome reports increment after argument validation regardless of
// whether the router answered — a consult against a dead router is
// verse adherence (the miss path) — while malformed calls never count.
func TestHandleRoute_AdoptionCountersIncrementAtAttemptTime(t *testing.T) {
	srv := newRouterToolServer(t, defaultFakeRouter(t))

	// Successful consult + outcome report: one each.
	res, err := srv.handleRoute(context.Background(), makeReq(map[string]any{
		"envelope": map[string]any{"tool_name": "Task", "complexity_tier": "lite", "role": "maker", "session_id": "s"},
	}))
	if err != nil || res.IsError {
		t.Fatalf("route consult failed: err=%v res=%v", err, res)
	}
	res, err = srv.handleRoute(context.Background(), makeReq(map[string]any{
		"action":  "outcome",
		"outcome": map[string]any{"request_id": "deadbeefdeadbeefdeadbeefdeadbeef", "outcome_class": "clean", "gate": "S5"},
	}))
	if err != nil || res.IsError {
		t.Fatalf("outcome report failed: err=%v res=%v", err, res)
	}
	if got := atomic.LoadInt64(&srv.statsRouteConsults); got != 1 {
		t.Errorf("statsRouteConsults = %d, want 1", got)
	}
	if got := atomic.LoadInt64(&srv.statsRouteOutcomes); got != 1 {
		t.Errorf("statsRouteOutcomes = %d, want 1", got)
	}

	// Malformed consult (no envelope): rejected before counting.
	res, err = srv.handleRoute(context.Background(), makeReq(map[string]any{}))
	if err != nil {
		t.Fatalf("handleRoute: %v", err)
	}
	if !res.IsError {
		t.Fatal("envelope-less route call must error")
	}
	if got := atomic.LoadInt64(&srv.statsRouteConsults); got != 1 {
		t.Errorf("malformed consult counted: statsRouteConsults = %d, want still 1", got)
	}

	// Dead router: the consult still counts (attempt-time semantics).
	dead := newRouterToolServer(t, http.NotFoundHandler())
	res, err = dead.handleRoute(context.Background(), makeReq(map[string]any{
		"envelope": map[string]any{"tool_name": "Task"},
	}))
	if err != nil {
		t.Fatalf("handleRoute: %v", err)
	}
	if !res.IsError {
		t.Fatal("dead-router consult should surface the structured miss error")
	}
	if got := atomic.LoadInt64(&dead.statsRouteConsults); got != 1 {
		t.Errorf("dead-router consult must still count as adherence: statsRouteConsults = %d, want 1", got)
	}
}
