// SPDX-License-Identifier: MIT

package server

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/kwad77/pincher/internal/db"
	"github.com/kwad77/pincher/internal/index"
)

// #2036 — the echo-durability decision (durability half of #2032).
//
// Decision: PERSIST the request_id → echo map across a respawn, rather
// than only documenting the bounded in-process degradation. The #2033
// root cause is environmental, not logical: the LRU is in-process, so an
// auto-restart-on-drift respawn (auto_restart.go), a crash, or an MCP
// client reconnect between a same-session route and its outcome wipes
// the cache and the verse's minimal card 422s — invisibly to the caller,
// who sees one continuous session. #2033 shipped the *observability*
// (echo_source:none + the loud echo_miss log) but not the *fix*; telling
// the caller to "echo full fields" defeats the minimal-card verse
// contract item B10 exists to honour. Persisting a bounded (≤64-entry)
// map to one data-dir-keyed JSON sidecar closes the gap with no new
// dependency and no schema migration.
//
// These tests build a SECOND server against the SAME data-dir — the
// faithful in-test analogue of a respawn (the next process loads the
// sidecar the prior one wrote) — and assert the documented behaviour:
//   - post-respawn, the minimal card auto-fills from the persisted cache
//     (echo_source:cache), the 422-shaped path #2032 reported is gone;
//   - LRU recency/order survives the round-trip;
//   - a corrupt sidecar degrades to a cold (in-process-only) start, not
//     a startup failure;
//   - PINCHER_ROUTER=off writes no sidecar (zero routing activity);
//   - an embedded store with no path leaves the in-process behaviour.

// newRouterToolServerWithDir wires a pincher Server at `handler` (a fake
// router) AND a caller-supplied data-dir, so two server instances can
// share one echo sidecar — the in-test analogue of a process respawn (B
// loads the file A wrote). Same env discipline as newRouterToolServer
// (PINCHER_ROUTER=on + the httptest addr) but with the dir pinned instead
// of a fresh t.TempDir() per server.
func newRouterToolServerWithDir(t *testing.T, handler *fakeRouterV2, dir string) *Server {
	t.Helper()
	rt := httptest.NewServer(handler)
	t.Cleanup(rt.Close)
	t.Setenv("PINCHER_ROUTER", "on")
	t.Setenv("PINCHER_ROUTER_ADDR", strings.TrimPrefix(rt.URL, "http://"))
	store, err := db.Open(dir)
	if err != nil {
		t.Fatalf("db.Open(%q): %v", dir, err)
	}
	t.Cleanup(func() { store.Close() })
	srv := New(store, index.New(store), "test")
	srv.autoRestartDelay = 0
	return srv
}

// routeBodyWithID is a v2 ExecutionPlan fixture whose model + request_id
// are keyed off i, so a sequence of consults pins distinct cache entries.
func routeBodyWithID(i int) string {
	return `{"schema_version":"execution-plan/v2","model":"m-` +
		strconv.Itoa(i) + `","lane":"fast","mode":"execute","request_id":"req-` + strconv.Itoa(i) + `"}`
}

// minimalOutcomeCard is the verse's faithful minimal card — exactly the
// body that 422'd in the #2032 transcript when the cache was cold.
func minimalOutcomeCard(requestID string) map[string]any {
	return map[string]any{
		"action": "outcome",
		"outcome": map[string]any{
			"request_id":    requestID,
			"outcome_class": "clean",
			"gate":          "S5",
		},
	}
}

// TestEchoDurability_MinimalCardSurvivesRespawn is the decision's proof.
// Server A routes (caching the echo), then "respawns": server B opens the
// SAME data-dir and serves the verse's minimal outcome card. Pre-#2036
// that card 422'd (cold cache, echo_source:none); the persisted sidecar
// must make B auto-fill it from A's route call as if no restart happened.
func TestEchoDurability_MinimalCardSurvivesRespawn(t *testing.T) {
	dir := t.TempDir()
	fakeA := defaultFakeRouter(t)
	srvA := newRouterToolServerWithDir(t, fakeA, dir)

	// Server A's route consult pins request_id deadbeef… → the echo.
	if res := callRoute(t, srvA, map[string]any{"envelope": minimalEnvelope()}); res.IsError {
		t.Fatalf("server A route consult errored: %s", textOf(t, res))
	}

	// The sidecar must now exist beside the store (proof A flushed it).
	sidecar := filepath.Join(dir, routeEchoPersistFile)
	if _, err := os.Stat(sidecar); err != nil {
		t.Fatalf("route echo sidecar not written by server A: %v", err)
	}

	// RESPAWN: server B, same data-dir, NEVER saw the route call. Its
	// cache is seeded purely from A's sidecar.
	fakeB := defaultFakeRouter(t)
	srvB := newRouterToolServerWithDir(t, fakeB, dir)

	res := callRoute(t, srvB, minimalOutcomeCard("deadbeefdeadbeefdeadbeefdeadbeef"))
	if res.IsError {
		t.Fatalf("post-respawn outcome errored: %s", textOf(t, res))
	}

	// The forwarded /v1/outcomes body carries the echo A cached — the
	// minimal card was completed across the respawn boundary.
	posted, _ := fakeB.lastBodyJSON.Load().(map[string]any)
	if posted == nil {
		t.Fatal("no outcome body reached server B's fake router")
	}
	for k, want := range map[string]any{
		"session_id":      "sess-autoecho",
		"tool_name":       "Task",
		"complexity_tier": "lite",
		"role":            "fix",
		"tokens_used":     float64(1234),
		"routed_model":    "qwen3.6-35b-a3b",
		"lane":            "fast",
	} {
		if posted[k] != want {
			t.Errorf("post-respawn echo %q = %#v, want %#v (cache did not survive respawn)", k, posted[k], want)
		}
	}

	// And the response SAYS the echo came from the (persisted) cache —
	// the #2032 failure shape (echo_source:none) is gone.
	body := decode(t, res)
	if body["echo_source"] != "cache" {
		t.Errorf("echo_source = %v, want \"cache\" — the persisted cache should fill the post-respawn card", body["echo_source"])
	}
}

// TestEchoDurability_LRUOrderSurvivesRespawn pins that recency, not just
// membership, round-trips through the sidecar: a reload that lost order
// would evict the wrong entry on the next put. Fill A to the cap, respawn
// into B, add one entry, and assert the oldest (not some arbitrary one)
// evicted.
func TestEchoDurability_LRUOrderSurvivesRespawn(t *testing.T) {
	dir := t.TempDir()
	fakeA := defaultFakeRouter(t)
	srvA := newRouterToolServerWithDir(t, fakeA, dir)

	// A routes exactly cap entries: req-0 (oldest) … req-(cap-1).
	for i := 0; i < routeEchoCacheCap; i++ {
		fakeA.routeBody = routeBodyWithID(i)
		if res := callRoute(t, srvA, map[string]any{"envelope": minimalEnvelope()}); res.IsError {
			t.Fatalf("A route %d errored: %s", i, textOf(t, res))
		}
	}

	// RESPAWN into B and route ONE more — this overflows the cap, so the
	// oldest persisted id (req-0) must evict.
	fakeB := defaultFakeRouter(t)
	srvB := newRouterToolServerWithDir(t, fakeB, dir)
	fakeB.routeBody = routeBodyWithID(routeEchoCacheCap) // req-cap
	if res := callRoute(t, srvB, map[string]any{"envelope": minimalEnvelope()}); res.IsError {
		t.Fatalf("B overflow route errored: %s", textOf(t, res))
	}

	// req-0 (the oldest, persisted from A) is the one that evicted ⇒
	// its outcome is a cache-miss passthrough.
	res := callRoute(t, srvB, minimalOutcomeCard("req-0"))
	if res.IsError {
		t.Fatalf("outcome for evicted id errored: %s", textOf(t, res))
	}
	if posted, _ := fakeB.lastBodyJSON.Load().(map[string]any); posted["routed_model"] != nil {
		t.Errorf("oldest persisted entry (req-0) should have evicted after respawn+overflow: %#v", posted)
	}

	// A mid-range id persisted from A still auto-fills in B ⇒ the
	// surviving entries crossed the respawn intact.
	res = callRoute(t, srvB, minimalOutcomeCard("req-1"))
	if res.IsError {
		t.Fatalf("outcome for surviving id errored: %s", textOf(t, res))
	}
	if posted, _ := fakeB.lastBodyJSON.Load().(map[string]any); posted["routed_model"] != "m-1" {
		t.Errorf("surviving persisted entry req-1 not auto-filled after respawn: %#v", posted)
	}
}

// TestEchoDurability_CorruptSidecarColdStarts proves the best-effort
// posture: a garbage sidecar must NOT fail New(); the server starts with
// an empty (in-process) cache — the documented degradation, not a crash.
func TestEchoDurability_CorruptSidecarColdStarts(t *testing.T) {
	dir := t.TempDir()
	// Pre-seed a corrupt sidecar before any server opens the dir.
	if err := os.WriteFile(filepath.Join(dir, routeEchoPersistFile), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("seed corrupt sidecar: %v", err)
	}

	fake := defaultFakeRouter(t)
	srv := newRouterToolServerWithDir(t, fake, dir) // must not panic/fail

	// Cold cache: a minimal card for an id the (corrupt, ignored) sidecar
	// could not supply passes through unchanged with echo_source:none —
	// the honest-422 posture, intact.
	res := callRoute(t, srv, minimalOutcomeCard("deadbeefdeadbeefdeadbeefdeadbeef"))
	if res.IsError {
		t.Fatalf("outcome on corrupt-sidecar cold start errored: %s", textOf(t, res))
	}
	if body := decode(t, res); body["echo_source"] != "none" {
		t.Errorf("echo_source = %v, want \"none\" after corrupt-sidecar cold start", body["echo_source"])
	}

	// A fresh route now rewrites the sidecar to valid JSON (recovery).
	if res := callRoute(t, srv, map[string]any{"envelope": minimalEnvelope()}); res.IsError {
		t.Fatalf("recovery route errored: %s", textOf(t, res))
	}
	raw, err := os.ReadFile(filepath.Join(dir, routeEchoPersistFile))
	if err != nil {
		t.Fatalf("sidecar not rewritten after recovery: %v", err)
	}
	var snap routeEchoSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Errorf("recovered sidecar is not valid JSON: %v\n%s", err, raw)
	}
}

// TestEchoDurability_RouterOffWritesNoSidecar pins that PINCHER_ROUTER=off
// means zero routing activity, including zero persistence: off is the
// whole-surface rollback, and a sidecar on disk would be activity.
func TestEchoDurability_RouterOffWritesNoSidecar(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PINCHER_ROUTER", "off")
	store, err := db.Open(dir)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	srv := New(store, index.New(store), "test")

	// persistPath must be empty (enablePersistence was skipped).
	srv.routeEcho.mu.Lock()
	gotPath := srv.routeEcho.persistPath
	srv.routeEcho.mu.Unlock()
	if gotPath != "" {
		t.Errorf("PINCHER_ROUTER=off must not enable echo persistence, got persistPath=%q", gotPath)
	}
	if _, err := os.Stat(filepath.Join(dir, routeEchoPersistFile)); !os.IsNotExist(err) {
		t.Errorf("PINCHER_ROUTER=off must write no sidecar; stat err=%v", err)
	}
}

// TestEchoDurability_PersistPathDerivesFromDataDir documents the keying
// invariant: the sidecar sits beside the SQLite store (one data-dir ⇒ one
// sidecar, shared across the HTTP daemon and the stdio MCP process the
// same way the DB is).
func TestEchoDurability_PersistPathDerivesFromDataDir(t *testing.T) {
	dir := t.TempDir()
	fake := defaultFakeRouter(t)
	srv := newRouterToolServerWithDir(t, fake, dir)

	srv.routeEcho.mu.Lock()
	gotPath := srv.routeEcho.persistPath
	srv.routeEcho.mu.Unlock()

	want := filepath.Join(dir, routeEchoPersistFile)
	if gotPath != want {
		t.Errorf("persistPath = %q, want %q (sidecar must sit beside the store)", gotPath, want)
	}
	if !strings.HasPrefix(gotPath, filepath.Dir(srv.store.Path)) {
		t.Errorf("persistPath %q not under the store's data dir %q", gotPath, filepath.Dir(srv.store.Path))
	}
}
