// SPDX-License-Identifier: MIT

package server

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// pincher-router detection (router-loop plan item B4; ADR
// ROUTER_DETECTION_LADDER). Detects whether a pincher-router
// installation is present and serving, so the `router` capability tag
// can be advertised in _meta.capabilities and GET /v1/capabilities.
// The conditional `models`/`route` tool surface (plan item B5) gates
// on the same detection result; this file ships only the detection +
// tag half.
//
// The ladder, cheap → expensive (PHASE0.md §3.2):
//
//  1. Config dir — stat ~/.config/pincher-router/workers.yaml. One
//     syscall; presence means the user ran `pincher-router-init`
//     (intent).
//  2. PATH lookup — exec.LookPath("pincher-router-serve"). Catches
//     installed-but-uninitialized. There is NO bare `pincher-router`
//     binary (PHASE0 §3.1) — probing for one would always miss.
//  3. Identity-validated liveness probe — GET /healthz on the
//     configured/default router address, REQUIRING a 200 response
//     whose JSON body contains `weights_version`. This is not
//     paranoia: on the spike box, port 7878 was occupied by a
//     *pincher* HTTP instance answering on that port (PHASE0 §4, the
//     false-positive fixture) — a status-code-only probe would have
//     detected a router on a machine where none is running. Redirects
//     are NOT followed (the port-11000 DGX-dashboard 302 fixture).
//
// Steps 1–2 are pre-filters: when neither hits, detection is a
// no-network "absent" answer. When either hits, step 3 decides — a
// router that is installed but not serving (or something else
// squatting on the port) is NOT detected. The whole ladder is
// best-effort and bounded (≤50ms probe timeout): any error on any
// rung means "absent", never an error path out of New().
//
// Read once at New() and cached on s.routerDetected, exactly like
// PINCHER_META_CAPABILITIES and the schema-diet knobs (read-once
// contract: schema_diet.go) — detection cannot toggle mid-process.
//
// Override knob: PINCHER_ROUTER=off|auto|on, default auto (= run the
// ladder). Canonical-value-only parse with fail-direction ABSENT: a
// typo yields zero router surface, never a phantom one (plan §A6).
// PINCHER_ROUTER=off is the rollback story for the whole routing
// surface.
const (
	routerModeOff  = "off"
	routerModeAuto = "auto"
	routerModeOn   = "on"

	// routerServeBinary is the PATH probe target. The router installs
	// pincher-router-init / pincher-router-serve / pincher-router-stats
	// etc. — there is no bare `pincher-router` binary (PHASE0 §3.1).
	routerServeBinary = "pincher-router-serve"

	// routerDefaultAddr is the router service's conventional loopback
	// bind (PHASE0 §3.1). Override via PINCHER_ROUTER_ADDR.
	routerDefaultAddr = "127.0.0.1:7878"

	// routerProbeTimeout bounds the healthz rung. The detection budget
	// is ≤50ms total: rungs 1–2 are a stat + a PATH walk (microseconds)
	// and the HTTP client timeout caps rung 3. Closed loopback ports
	// refuse immediately; a live router answers healthz in well under
	// 10ms (PHASE0 §4 measured <50ms for a heavier vLLM endpoint).
	routerProbeTimeout = 50 * time.Millisecond
)

// routerProbeConfig carries the ladder inputs so tests can substitute
// every rung (temp-dir config path, fake LookPath, httptest healthz).
type routerProbeConfig struct {
	mode       string // off|auto|on — output of parseRouterEnv
	configPath string // rung 1: workers.yaml path
	binary     string // rung 2: PATH probe target
	baseURL    string // router service base ("http://<addr>") — item B5 proxy target
	healthzURL string // rung 3: identity-validated liveness endpoint
	timeout    time.Duration
	lookPath   func(string) (string, error)
}

// defaultRouterProbeConfig builds the production ladder config from
// the process environment.
func defaultRouterProbeConfig() routerProbeConfig {
	addr := strings.TrimSpace(os.Getenv("PINCHER_ROUTER_ADDR"))
	if addr == "" {
		addr = routerDefaultAddr
	}
	cfg := routerProbeConfig{
		mode:       parseRouterEnv(os.Getenv("PINCHER_ROUTER")),
		binary:     routerServeBinary,
		baseURL:    "http://" + addr,
		healthzURL: "http://" + addr + "/healthz",
		timeout:    routerProbeTimeout,
		lookPath:   exec.LookPath,
	}
	if home, err := os.UserHomeDir(); err == nil {
		cfg.configPath = filepath.Join(home, ".config", "pincher-router", "workers.yaml")
	}
	return cfg
}

// parseRouterEnv reads PINCHER_ROUTER and returns the canonical mode.
// Same canonical-value-only rule as parseToolsetEnv (#2003): only the
// exact values switch state, and the fail direction is ABSENT — an
// unrecognized value (typo) disables detection rather than landing on
// auto, so a mis-set knob can never produce a phantom router surface
// (plan §A6 / PHASE0 §3.4). Unset (or explicit "auto") runs the ladder.
func parseRouterEnv(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", routerModeAuto:
		return routerModeAuto
	case routerModeOn:
		return routerModeOn
	default: // "off" and every typo: fail direction is absent
		return routerModeOff
	}
}

// detectRouter runs the detection ladder and reports whether a live,
// identity-validated pincher-router is present. Pure best-effort:
// every failure mode (missing config, no binary, dead port, wrong
// service on the port, malformed body, timeout) returns false. It
// never returns an error — detection must not be able to block New().
func detectRouter(cfg routerProbeConfig) bool {
	switch cfg.mode {
	case routerModeOff:
		return false
	case routerModeOn:
		return true
	}
	// auto: rungs 1–2 establish installation intent; without either,
	// answer "absent" with zero network traffic.
	installed := false
	if cfg.configPath != "" {
		if _, err := os.Stat(cfg.configPath); err == nil {
			installed = true
		}
	}
	if !installed && cfg.lookPath != nil {
		if _, err := cfg.lookPath(cfg.binary); err == nil {
			installed = true
		}
	}
	if !installed {
		return false
	}
	// Rung 3: identity-validated liveness. Installed-but-not-serving
	// (or an impostor on the port) is NOT detected.
	return probeRouterHealthz(cfg.healthzURL, cfg.timeout)
}

// probeRouterHealthz performs the identity-validated healthz rung:
// 200 status AND a JSON body containing `weights_version`
// (router_service_server.py returns {ok, weights_version}). Both
// real-world false positives from the spike are rejected here: the
// pincher-on-7878 instance fails the status/identity check, and the
// DGX-dashboard 302 is rejected because redirects are not followed.
func probeRouterHealthz(url string, timeout time.Duration) bool {
	client := &http.Client{
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return false
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return false
	}
	_, ok := parsed["weights_version"]
	return ok
}
