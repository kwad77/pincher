// SPDX-License-Identifier: MIT

package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

// Router-loop item B4: detection ladder tests (router_detect.go).
// Every rung is substituted — temp-dir config path, fake LookPath,
// httptest healthz — so nothing here depends on the machine actually
// having (or not having) a pincher-router installed.

var errNotOnPath = errors.New("executable file not found in $PATH")

func lookPathHit(string) (string, error)  { return "/fake/bin/pincher-router-serve", nil }
func lookPathMiss(string) (string, error) { return "", errNotOnPath }

// fakeRouter starts an httptest server whose /healthz answers with the
// given status and body, and counts hits so tests can assert the probe
// rung did (or did not) fire.
func fakeRouter(t *testing.T, status int, body string) (*httptest.Server, *int64) {
	t.Helper()
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// tempWorkersYAML creates a stand-in ~/.config/pincher-router/workers.yaml
// and returns its path (rung 1 hit).
func tempWorkersYAML(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "workers.yaml")
	if err := os.WriteFile(p, []byte("providers: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRouterDetect_NoConfigNoBinary_NotDetectedAndNoProbe(t *testing.T) {
	t.Parallel()
	srv, hits := fakeRouter(t, 200, `{"ok":true,"weights_version":"v3"}`)
	cfg := routerProbeConfig{
		mode:       routerModeAuto,
		configPath: filepath.Join(t.TempDir(), "nope", "workers.yaml"),
		binary:     routerServeBinary,
		healthzURL: srv.URL + "/healthz",
		timeout:    routerProbeTimeout,
		lookPath:   lookPathMiss,
	}
	if detectRouter(cfg) {
		t.Error("detected with no config dir and no binary on PATH")
	}
	// Rungs 1–2 both missed ⇒ the network rung must never fire: the
	// absent state is zero-traffic by design.
	if n := atomic.LoadInt64(hits); n != 0 {
		t.Errorf("healthz probed %d time(s) despite rungs 1–2 missing — absent must be zero-traffic", n)
	}
}

func TestRouterDetect_BinaryPresentButNoService_NotDetected(t *testing.T) {
	t.Parallel()
	// Grab a loopback port that is closed: start a server, note the
	// URL, close it. Connection refused on loopback is immediate.
	srv, _ := fakeRouter(t, 200, `{}`)
	deadURL := srv.URL + "/healthz"
	srv.Close()
	cfg := routerProbeConfig{
		mode:       routerModeAuto,
		configPath: filepath.Join(t.TempDir(), "nope", "workers.yaml"),
		binary:     routerServeBinary,
		healthzURL: deadURL,
		timeout:    routerProbeTimeout,
		lookPath:   lookPathHit,
	}
	if detectRouter(cfg) {
		t.Error("detected with binary on PATH but nothing serving — installed-but-not-serving must be absent")
	}
}

func TestRouterDetect_ServiceWithoutWeightsVersion_NotDetected(t *testing.T) {
	t.Parallel()
	// 200 + JSON but no weights_version: a healthz-shaped answer that
	// fails identity validation must NOT count (the :7878 collision —
	// PHASE0 §3.2 — is exactly this class of impostor).
	srv, hits := fakeRouter(t, 200, `{"ok":true,"status":"healthy"}`)
	cfg := routerProbeConfig{
		mode:       routerModeAuto,
		configPath: tempWorkersYAML(t),
		binary:     routerServeBinary,
		healthzURL: srv.URL + "/healthz",
		timeout:    routerProbeTimeout,
		lookPath:   lookPathMiss,
	}
	if detectRouter(cfg) {
		t.Error("detected from a healthz response lacking weights_version — identity validation is mandatory")
	}
	if n := atomic.LoadInt64(hits); n != 1 {
		t.Errorf("expected exactly 1 probe, got %d", n)
	}
}

func TestRouterDetect_PincherImpostorOn7878_NotDetected(t *testing.T) {
	t.Parallel()
	// Replay of the measured false positive (PHASE0 §4): a *pincher*
	// HTTP instance squatting on the router port answers /healthz with
	// its standardized 404 error envelope.
	srv, _ := fakeRouter(t, 404, `{"error":{"code":"not_found","message":"unknown tool \"/healthz\""}}`)
	cfg := routerProbeConfig{
		mode:       routerModeAuto,
		configPath: tempWorkersYAML(t),
		binary:     routerServeBinary,
		healthzURL: srv.URL + "/healthz",
		timeout:    routerProbeTimeout,
		lookPath:   lookPathMiss,
	}
	if detectRouter(cfg) {
		t.Error("detected a pincher HTTP instance as a router — the :7878 collision fixture must be rejected")
	}
}

func TestRouterDetect_RedirectingDashboard_NotDetected(t *testing.T) {
	t.Parallel()
	// Replay of the second measured false positive (PHASE0 §4): the
	// port-11000 DGX dashboard answers with a 302. Redirects are not
	// followed; anything but a direct 200 is rejected.
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		http.Redirect(w, r, "/login", http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	cfg := routerProbeConfig{
		mode:       routerModeAuto,
		configPath: tempWorkersYAML(t),
		binary:     routerServeBinary,
		healthzURL: srv.URL + "/healthz",
		timeout:    routerProbeTimeout,
		lookPath:   lookPathMiss,
	}
	if detectRouter(cfg) {
		t.Error("detected a 302-redirecting web UI as a router")
	}
	if n := atomic.LoadInt64(&hits); n != 1 {
		t.Errorf("redirect must not be followed; expected exactly 1 hit, got %d", n)
	}
}

func TestRouterDetect_FullLadder_Detected(t *testing.T) {
	t.Parallel()
	srv, _ := fakeRouter(t, 200, `{"ok":true,"weights_version":"2026-06-11T00:00:00Z"}`)
	cfg := routerProbeConfig{
		mode:       routerModeAuto,
		configPath: tempWorkersYAML(t),
		binary:     routerServeBinary,
		healthzURL: srv.URL + "/healthz",
		timeout:    routerProbeTimeout,
		lookPath:   lookPathMiss,
	}
	start := time.Now()
	detected := detectRouter(cfg)
	elapsed := time.Since(start)
	if !detected {
		t.Error("full ladder (config present + identity-validated healthz) not detected")
	}
	t.Logf("full-ladder detection took %v", elapsed)
}

func TestRouterDetect_BinaryRungAloneReachesProbe_Detected(t *testing.T) {
	t.Parallel()
	// Rung 2 (PATH) alone — installed-but-uninitialized config-wise —
	// still earns the liveness probe, and a valid healthz detects.
	srv, _ := fakeRouter(t, 200, `{"ok":true,"weights_version":"v1"}`)
	cfg := routerProbeConfig{
		mode:       routerModeAuto,
		configPath: filepath.Join(t.TempDir(), "nope", "workers.yaml"),
		binary:     routerServeBinary,
		healthzURL: srv.URL + "/healthz",
		timeout:    routerProbeTimeout,
		lookPath:   lookPathHit,
	}
	if !detectRouter(cfg) {
		t.Error("binary-on-PATH + valid healthz must detect")
	}
}

func TestRouterDetect_SlowService_BoundedAndNotDetected(t *testing.T) {
	t.Parallel()
	// A hanging healthz must neither block nor detect: the probe
	// timeout bounds the whole ladder (≤50ms budget; generous CI
	// margin on the assertion).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
	}))
	t.Cleanup(srv.Close)
	cfg := routerProbeConfig{
		mode:       routerModeAuto,
		configPath: tempWorkersYAML(t),
		binary:     routerServeBinary,
		healthzURL: srv.URL + "/healthz",
		timeout:    routerProbeTimeout,
		lookPath:   lookPathMiss,
	}
	start := time.Now()
	detected := detectRouter(cfg)
	elapsed := time.Since(start)
	if detected {
		t.Error("hanging service detected as router")
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("detection took %v against a hanging service — must be bounded by the %v probe timeout", elapsed, routerProbeTimeout)
	}
	t.Logf("hanging-service detection bounded at %v (timeout %v)", elapsed, routerProbeTimeout)
}

func TestParseRouterEnv_CanonicalValuesOnly(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		{"", routerModeAuto},
		{"auto", routerModeAuto},
		{" AUTO ", routerModeAuto},
		{"on", routerModeOn},
		{"ON", routerModeOn},
		{"off", routerModeOff},
		{"Off", routerModeOff},
		// Fail direction is ABSENT: typos and v0-style values disable
		// detection, they never land on auto or on (plan §A6).
		{"1", routerModeOff},
		{"0", routerModeOff},
		{"true", routerModeOff},
		{"onn", routerModeOff},
		{"autoo", routerModeOff},
		{"enabled", routerModeOff},
	}
	for _, c := range cases {
		if got := parseRouterEnv(c.in); got != c.want {
			t.Errorf("parseRouterEnv(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRouterDetect_EnvOff_ForcesAbsentWithoutProbing(t *testing.T) {
	t.Parallel()
	// Everything present and healthy, but PINCHER_ROUTER=off wins —
	// and the ladder must not even touch the network (rollback story:
	// off means zero routing activity of any kind).
	srv, hits := fakeRouter(t, 200, `{"ok":true,"weights_version":"v3"}`)
	cfg := routerProbeConfig{
		mode:       routerModeOff,
		configPath: tempWorkersYAML(t),
		binary:     routerServeBinary,
		healthzURL: srv.URL + "/healthz",
		timeout:    routerProbeTimeout,
		lookPath:   lookPathHit,
	}
	if detectRouter(cfg) {
		t.Error("PINCHER_ROUTER=off but detection reported present")
	}
	if n := atomic.LoadInt64(hits); n != 0 {
		t.Errorf("PINCHER_ROUTER=off must not probe; healthz hit %d time(s)", n)
	}
}

func TestRouterDetect_EnvOn_ForcesDetected(t *testing.T) {
	t.Parallel()
	// Nothing installed anywhere, but PINCHER_ROUTER=on forces the
	// capability on (operator override / fixture story).
	cfg := routerProbeConfig{
		mode:       routerModeOn,
		configPath: filepath.Join(t.TempDir(), "nope", "workers.yaml"),
		binary:     routerServeBinary,
		healthzURL: "http://127.0.0.1:1/healthz", // would refuse if dialed
		timeout:    routerProbeTimeout,
		lookPath:   lookPathMiss,
	}
	if !detectRouter(cfg) {
		t.Error("PINCHER_ROUTER=on but detection reported absent")
	}
}

// TestCapability_RouterConditional pins the capabilities shape in BOTH
// detection states: absent ⇒ the advertisement is byte-identical to
// today's (no `router`, nothing else moved); present ⇒ exactly the
// same slice plus the `router` tag. This is the A6 zero-surface
// discipline applied to the tag half of the feature (the tool-surface
// half is item B5; tool-contract goldens are untouched by this item).
func TestCapability_RouterConditional(t *testing.T) {
	srv, _, _ := newTestServer(t)

	srv.routerDetected = false
	capsAbsent := computeCapabilities(srv)
	for _, c := range capsAbsent {
		if c == "router" {
			t.Fatal("router advertised with routerDetected=false")
		}
	}

	srv.routerDetected = true
	capsPresent := computeCapabilities(srv)
	if !reflect.DeepEqual(capsPresent, append(append([]string{}, capsAbsent...), "router")) {
		t.Errorf("present-state capabilities must be the absent-state slice + \"router\" appended\nabsent:  %v\npresent: %v", capsAbsent, capsPresent)
	}
}

// TestCapability_RouterServedOverHTTP verifies the detected-state tag
// reaches GET /v1/capabilities (the polling twin of _meta.capabilities).
func TestCapability_RouterServedOverHTTP(t *testing.T) {
	srv, _, _ := newTestServer(t)
	srv.routerDetected = true
	srv.capabilities = computeCapabilities(srv)

	req := httptest.NewRequest("GET", "/v1/capabilities", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("GET /v1/capabilities returned %d: %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("capabilities body not JSON: %v", err)
	}
	caps, ok := body["capabilities"].([]any)
	if !ok {
		t.Fatalf("capabilities key missing/wrong type: %v", body)
	}
	found := false
	for _, c := range caps {
		if c == "router" {
			found = true
		}
	}
	if !found {
		t.Errorf("router missing from GET /v1/capabilities in detected state; got %v", caps)
	}
}

// ── RouterInstalledNoProbe (rungs 1–2 only — the hook advisory seam,
//    router-loop §A2 / item B8) ───────────────────────────────────────────

// TestRouterInstalledRungs12_NeverDialsAndAnswersByInstallIntent pins
// the no-network contract: the hook-side detection MUST answer from
// rungs 1–2 alone (config stat + LookPath) inside the <50ms hook
// budget. A configured healthz hit-counter proves no rung-3 dial ever
// happens, in any mode.
func TestRouterInstalledRungs12_NeverDialsAndAnswersByInstallIntent(t *testing.T) {
	srv, hits := fakeRouter(t, http.StatusOK, `{"ok": true, "weights_version": "vX"}`)
	cases := []struct {
		name       string
		mode       string
		configPath string
		lookPath   func(string) (string, error)
		want       bool
	}{
		{"off forces absent even when installed", routerModeOff, tempWorkersYAML(t), lookPathHit, false},
		{"on forces installed", routerModeOn, "", lookPathMiss, true},
		{"auto + config file (rung 1)", routerModeAuto, tempWorkersYAML(t), lookPathMiss, true},
		{"auto + binary on PATH (rung 2)", routerModeAuto, filepath.Join(t.TempDir(), "missing.yaml"), lookPathHit, true},
		{"auto + neither rung", routerModeAuto, filepath.Join(t.TempDir(), "missing.yaml"), lookPathMiss, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := routerProbeConfig{
				mode:       tc.mode,
				configPath: tc.configPath,
				binary:     routerServeBinary,
				healthzURL: srv.URL + "/healthz",
				timeout:    routerProbeTimeout,
				lookPath:   tc.lookPath,
			}
			if got := routerInstalledRungs12(cfg); got != tc.want {
				t.Errorf("routerInstalledRungs12 = %v, want %v", got, tc.want)
			}
		})
	}
	if n := atomic.LoadInt64(hits); n != 0 {
		t.Errorf("rungs-1–2 detection dialed the network %d time(s) — the hook seam must never probe", n)
	}
}

// TestRouterInstalledNoProbe_EnvContract drives the exported wrapper
// through PINCHER_ROUTER: off (and the typo fail-direction) answers
// absent without touching the machine state; on forces installed.
// The auto rungs are covered above with injected paths — auto against
// the real machine would make the test depend on what's installed.
func TestRouterInstalledNoProbe_EnvContract(t *testing.T) {
	t.Setenv("PINCHER_ROUTER", "off")
	if RouterInstalledNoProbe() {
		t.Error("PINCHER_ROUTER=off must answer absent")
	}
	t.Setenv("PINCHER_ROUTER", "offf") // typo: fail direction is absent
	if RouterInstalledNoProbe() {
		t.Error("typo'd PINCHER_ROUTER must answer absent")
	}
	t.Setenv("PINCHER_ROUTER", "on")
	if !RouterInstalledNoProbe() {
		t.Error("PINCHER_ROUTER=on must answer installed")
	}
}
