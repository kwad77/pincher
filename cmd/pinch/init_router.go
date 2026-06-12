// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/kwad77/pincher/internal/server"
)

// pincher init --router (router-loop plan §A3): seed the router-aware
// additions once — and only once — a pincher-router installation is
// actually present. The flag follows the established opt-in grammar
// (--git-hooks, --dco-hook: explicitly requested, idempotent, never
// overwrites user content) and composes three legs:
//
//  1. Detection first. The same ladder the server runs at startup
//     (config stat → PATH lookup → identity-validated healthz;
//     internal/server/router_detect.go), printed as a status block.
//     No installation found ⇒ the flag ERRORS with install guidance
//     and writes nothing — the CLAUDE.md routing block says
//     "(pincher-router detected)" and must never lie.
//  2. The managed-block refresh picks up the routing subsection
//     (internal/init/policy_router.md, plan §A5) inside the existing
//     <!-- pincher:start/end --> markers — replace-in-place,
//     idempotent; re-running `pincher init` without --router
//     refreshes the subsection back out.
//  3. The claude-skills leg runs so the pincher-loop skill (v0.4,
//     carrying the dispatch verse) is installed — preview by default,
//     --write to apply, exactly the claude-skills contract.
//
// Pincher never writes router-owned files: when the binary is present
// but ~/.config/pincher-router/workers.yaml is absent, we print the
// exact bootstrap commands (`pincher-router-init` then
// `pincher-router-serve` — there is NO bare `pincher-router` binary)
// instead of creating the registry ourselves.
//
// Unlike the server's capability tag, this leg ignores PINCHER_ROUTER:
// the flag is an explicit user request to seed configuration, not a
// runtime surface decision (the seeded artifacts are themselves
// self-inerting — the verse keys off `router` ∈ _meta.capabilities,
// which PINCHER_ROUTER still governs).

// routerInitProbeTimeout bounds the liveness + handshake fetches. Init
// is interactive, not on the server's 50ms startup budget, so it can
// afford the proxy-call budget (250ms) for a more reliable answer.
const routerInitProbeTimeout = 250 * time.Millisecond

// routerInitProbe carries the ladder inputs so tests can substitute
// every rung (temp-dir config path, fake lookPath, httptest endpoints).
type routerInitProbe struct {
	configPath string // rung 1: workers.yaml path
	binary     string // rung 2: PATH probe target
	baseURL    string // router service base ("http://<addr>")
	timeout    time.Duration
	lookPath   func(string) (string, error)
}

// defaultRouterInitProbe mirrors the server's production ladder config
// (one source of truth: server.RouterDetectionDefaults) with the
// init-appropriate timeout.
func defaultRouterInitProbe() routerInitProbe {
	configPath, binary, baseURL, _ := server.RouterDetectionDefaults()
	return routerInitProbe{
		configPath: configPath,
		binary:     binary,
		baseURL:    baseURL,
		timeout:    routerInitProbeTimeout,
		lookPath:   exec.LookPath,
	}
}

// routerInitStatus is the detection result `init --router` acts on —
// finer-grained than the server's boolean (the bootstrap pointers need
// to know WHICH rung hit).
type routerInitStatus struct {
	configPath      string
	configFound     bool // rung 1: workers.yaml exists
	binaryFound     bool // rung 2: pincher-router-serve on PATH
	baseURL         string
	serving         bool   // rung 3: identity-validated healthz passed
	weightsVersion  string // from the healthz body, when serving
	contractVersion int    // from the /v1/models handshake; 0 = unknown
}

// installed reports whether either install-intent rung hit. This is
// the gate for writing the routing block: installed-but-not-serving
// still seeds (the artifacts self-inert at runtime via the capability
// tag), but a machine with no trace of a router gets an error instead.
func (s routerInitStatus) installed() bool { return s.configFound || s.binaryFound }

// detectRouterForInit runs the detection ladder with init semantics:
// all rungs are evaluated independently (the status print wants each
// answer, not the first hit), and a serving router additionally gets a
// best-effort contract-version handshake from GET /v1/models.
func detectRouterForInit(p routerInitProbe) routerInitStatus {
	st := routerInitStatus{configPath: p.configPath, baseURL: p.baseURL}
	if p.configPath != "" {
		if _, err := os.Stat(p.configPath); err == nil {
			st.configFound = true
		}
	}
	if p.lookPath != nil {
		if _, err := p.lookPath(p.binary); err == nil {
			st.binaryFound = true
		}
	}
	if !st.installed() {
		return st // zero network traffic on the absent path
	}
	if wv, ok := server.RouterHealthzIdentity(p.baseURL+"/healthz", p.timeout); ok {
		st.serving = true
		st.weightsVersion = wv
		st.contractVersion = fetchRouterContractVersion(p.baseURL+"/v1/models", p.timeout)
	}
	return st
}

// fetchRouterContractVersion reads handshake.contract_version from the
// router's GET /v1/models response. Purely best-effort status data —
// any failure (old router without the endpoint, timeout, non-JSON)
// returns 0, rendered as "unknown".
func fetchRouterContractVersion(url string, timeout time.Duration) int {
	client := &http.Client{
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Get(url)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0
	}
	var parsed struct {
		Handshake struct {
			ContractVersion int `json:"contract_version"`
		} `json:"handshake"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0
	}
	return parsed.Handshake.ContractVersion
}

// printRouterInitStatus renders the detection ladder's per-rung answers
// (plan §A3 leg 1: "prints status — installed / serving / contract
// version").
func printRouterInitStatus(out io.Writer, st routerInitStatus) {
	mark := func(b bool) string {
		if b {
			return "present"
		}
		return "absent"
	}
	if st.installed() {
		fmt.Fprintln(out, "pincher init [router]: pincher-router detected")
	} else {
		fmt.Fprintln(out, "pincher init [router]: pincher-router NOT detected")
	}
	fmt.Fprintf(out, "  config:   %s (%s)\n", condenseHome(st.configPath), mark(st.configFound))
	fmt.Fprintf(out, "  binary:   pincher-router-serve on PATH (%s)\n", mark(st.binaryFound))
	switch {
	case st.serving && st.contractVersion > 0:
		fmt.Fprintf(out, "  serving:  yes — %s (weights_version %s, contract v%d)\n", st.baseURL, st.weightsVersion, st.contractVersion)
	case st.serving:
		fmt.Fprintf(out, "  serving:  yes — %s (weights_version %s, contract version unknown — pre-v2 router?)\n", st.baseURL, st.weightsVersion)
	case st.installed():
		fmt.Fprintf(out, "  serving:  no — %s/healthz not answering (or not a router)\n", st.baseURL)
	}
}

// routerAbsentGuidance is what the user still needs when detection says
// absent — printed to stderr right before the non-zero exit. Nothing
// router-related is written on this path: seeding a routing block
// against a missing installation would make CLAUDE.md lie.
func routerAbsentGuidance(st routerInitStatus) string {
	return fmt.Sprintf(`pincher init [router]: no pincher-router installation found — nothing router-related was written.
  Looked for %s and `+"`pincher-router-serve`"+` on PATH.
  Install pincher-router first (https://github.com/kwad77/pincher-router), then bootstrap it:
    pincher-router-init     # seed the worker registry (pincher never writes router-owned files)
    pincher-router-serve    # start the routing service
  Re-run `+"`pincher init --router`"+` afterwards.
`, condenseHome(st.configPath))
}

// printRouterBootstrapHints prints what a detected-but-incomplete
// installation still needs. Pincher never writes the registry itself —
// the router is the only writer of router-owned files — so the hints
// name the router's own bootstrap commands.
func printRouterBootstrapHints(out io.Writer, st routerInitStatus) {
	switch {
	case st.binaryFound && !st.configFound:
		fmt.Fprintf(out, "pincher init [router]: binary found but %s is missing — bootstrap the router:\n", condenseHome(st.configPath))
		fmt.Fprintln(out, "    pincher-router-init     # seed the worker registry")
		fmt.Fprintln(out, "    pincher-router-serve    # start the routing service")
	case !st.serving:
		fmt.Fprintln(out, "pincher init [router]: installed but not serving — start it: pincher-router-serve")
		fmt.Fprintln(out, "  (the seeded skill verse and routing block self-inert until the `router` capability is live)")
	}
}
