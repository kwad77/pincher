// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pinit "github.com/kwad77/pincher/internal/init"
)

// Tests for `pincher init --router` (router-loop plan §A3): the
// detection ladder with init semantics, the status / guidance /
// bootstrap-hint output, and the managed-block append/refresh
// idempotency for the routing subsection (§A5).

func lookPathMiss(string) (string, error) { return "", errors.New("not found") }

func lookPathHit(name string) (string, error) { return "/usr/local/bin/" + name, nil }

// probeFor builds a routerInitProbe against a temp config dir and a
// caller-supplied base URL, with every rung injectable.
func probeFor(t *testing.T, configPresent bool, lookPath func(string) (string, error), baseURL string) routerInitProbe {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "pincher-router", "workers.yaml")
	if configPresent {
		if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(configPath, []byte("registry_version: 2\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return routerInitProbe{
		configPath: configPath,
		binary:     "pincher-router-serve",
		baseURL:    baseURL,
		timeout:    200 * time.Millisecond,
		lookPath:   lookPath,
	}
}

// deadBaseURL returns a loopback URL nothing is listening on.
func deadBaseURL(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	return url
}

func TestDetectRouterForInit_Absent(t *testing.T) {
	t.Parallel()
	// baseURL deliberately unreachable: the absent path must answer
	// with zero network traffic, so a dead port must not matter.
	st := detectRouterForInit(probeFor(t, false, lookPathMiss, deadBaseURL(t)))
	if st.installed() {
		t.Fatalf("nothing installed, got installed() = true (%+v)", st)
	}
	if st.serving {
		t.Fatalf("absent router cannot be serving (%+v)", st)
	}
}

func TestDetectRouterForInit_ConfigOnlyNotServing(t *testing.T) {
	t.Parallel()
	st := detectRouterForInit(probeFor(t, true, lookPathMiss, deadBaseURL(t)))
	if !st.configFound || st.binaryFound {
		t.Fatalf("want config-only detection, got %+v", st)
	}
	if !st.installed() || st.serving {
		t.Fatalf("config present ⇒ installed, dead port ⇒ not serving; got %+v", st)
	}
}

func TestDetectRouterForInit_Serving(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/healthz":
			w.Write([]byte(`{"ok": true, "weights_version": 3}`))
		case "/v1/models":
			w.Write([]byte(`{"handshake": {"contract_version": 2, "weights_version": 3}, "models": []}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	st := detectRouterForInit(probeFor(t, true, lookPathHit, srv.URL))
	if !st.serving {
		t.Fatalf("live identity-validated router not detected: %+v", st)
	}
	if st.weightsVersion != "3" {
		t.Errorf("weightsVersion = %q, want %q", st.weightsVersion, "3")
	}
	if st.contractVersion != 2 {
		t.Errorf("contractVersion = %d, want 2", st.contractVersion)
	}
}

// The two real-world false positives (PHASE0 §4) replayed at the init
// layer: a non-router answering 200 JSON without weights_version, and
// a 302-redirecting dashboard. Both must read as installed-but-NOT-
// serving, never as a live router.
func TestDetectRouterForInit_ImpostorAndRedirect(t *testing.T) {
	t.Parallel()
	impostor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status": "ok"}`)) // pincher-on-7878 shape: 200, JSON, no weights_version
	}))
	defer impostor.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/login", http.StatusFound) // DGX-dashboard shape
	}))
	defer redirector.Close()

	for name, url := range map[string]string{"impostor": impostor.URL, "redirect": redirector.URL} {
		st := detectRouterForInit(probeFor(t, true, lookPathMiss, url))
		if st.serving {
			t.Errorf("[%s] identity validation failed open: %+v", name, st)
		}
		if !st.installed() {
			t.Errorf("[%s] config rung should still report installed", name)
		}
	}
}

func TestPrintRouterInitStatus_Present(t *testing.T) {
	t.Parallel()
	var buf strings.Builder
	printRouterInitStatus(&buf, routerInitStatus{
		configPath: "/home/u/.config/pincher-router/workers.yaml", configFound: true,
		binaryFound: true, baseURL: "http://127.0.0.1:7878",
		serving: true, weightsVersion: "3", contractVersion: 2,
	})
	got := buf.String()
	for _, want := range []string{
		"pincher-router detected",
		"workers.yaml (present)",
		"pincher-router-serve on PATH (present)",
		"serving:  yes — http://127.0.0.1:7878 (weights_version 3, contract v2)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("status output missing %q:\n%s", want, got)
		}
	}
}

func TestPrintRouterInitStatus_Absent(t *testing.T) {
	t.Parallel()
	var buf strings.Builder
	printRouterInitStatus(&buf, routerInitStatus{configPath: "/x/workers.yaml", baseURL: "http://127.0.0.1:7878"})
	got := buf.String()
	if !strings.Contains(got, "NOT detected") {
		t.Errorf("absent status should say NOT detected:\n%s", got)
	}
	if strings.Contains(got, "serving:") {
		t.Errorf("absent status must not print a serving line (no probe ran):\n%s", got)
	}
}

// The absent path's guidance is the "what a user still needs" pointer:
// where to get the router, the exact bootstrap commands, and the
// explicit statement that nothing was written.
func TestRouterAbsentGuidance(t *testing.T) {
	t.Parallel()
	got := routerAbsentGuidance(routerInitStatus{configPath: "/x/workers.yaml"})
	for _, want := range []string{
		"nothing router-related was written",
		"github.com/kwad77/pincher-router",
		"pincher-router-init",
		"pincher-router-serve",
		"pincher init --router",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("guidance missing %q:\n%s", want, got)
		}
	}
}

func TestPrintRouterBootstrapHints(t *testing.T) {
	t.Parallel()
	var binaryOnly strings.Builder
	printRouterBootstrapHints(&binaryOnly, routerInitStatus{binaryFound: true, configPath: "/x/workers.yaml"})
	if got := binaryOnly.String(); !strings.Contains(got, "pincher-router-init") || !strings.Contains(got, "pincher-router-serve") {
		t.Errorf("binary-only hint should name both bootstrap commands:\n%s", got)
	}

	var notServing strings.Builder
	printRouterBootstrapHints(&notServing, routerInitStatus{configFound: true, configPath: "/x/workers.yaml"})
	if got := notServing.String(); !strings.Contains(got, "start it: pincher-router-serve") {
		t.Errorf("installed-not-serving hint should say how to start the service:\n%s", got)
	}

	var serving strings.Builder
	printRouterBootstrapHints(&serving, routerInitStatus{configFound: true, serving: true})
	if got := serving.String(); got != "" {
		t.Errorf("fully-live router needs no bootstrap hints, got:\n%s", got)
	}
}

// The managed-block contract for --router: the routing subsection rides
// inside the existing <!-- pincher:start/end --> markers, re-runs
// replace in place (no duplicates), and a later run WITHOUT --router
// refreshes the subsection back out.
func TestInitRouterPolicyBlock_AppendRefreshIdempotent(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	var buf strings.Builder

	// Seed a plain (router-less) block first — the upgrade path.
	if err := runInitTarget(&buf, pinit.ClaudeTarget, tmp, false, false, pinit.PolicyMarkdown); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(tmp, "CLAUDE.md")
	plain, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain), "## Routing (pincher-router detected)") {
		t.Fatal("plain policy must not carry the routing subsection")
	}

	// --router refresh: subsection appears, inside the markers, once.
	if err := runInitTarget(&buf, pinit.ClaudeTarget, tmp, false, false, pinit.PolicyWithRouter()); err != nil {
		t.Fatal(err)
	}
	withRouter, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(withRouter), "## Routing (pincher-router detected)"); n != 1 {
		t.Fatalf("routing subsection should appear exactly once, got %d:\n%s", n, withRouter)
	}
	start := strings.Index(string(withRouter), pinit.MarkerStart)
	end := strings.Index(string(withRouter), pinit.MarkerEnd)
	sub := strings.Index(string(withRouter), "## Routing (pincher-router detected)")
	if !(start >= 0 && start < sub && sub < end) {
		t.Fatalf("routing subsection must live inside the managed markers (start=%d sub=%d end=%d)", start, sub, end)
	}

	// Idempotent: a second --router run is byte-identical.
	if err := runInitTarget(&buf, pinit.ClaudeTarget, tmp, false, false, pinit.PolicyWithRouter()); err != nil {
		t.Fatal(err)
	}
	again, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(withRouter) {
		t.Fatal("second --router run must be byte-identical (idempotent replace-in-place)")
	}

	// Downgrade path: plain init refreshes the subsection back out and
	// returns to the original plain content.
	if err := runInitTarget(&buf, pinit.ClaudeTarget, tmp, false, false, pinit.PolicyMarkdown); err != nil {
		t.Fatal(err)
	}
	back, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(back), "## Routing (pincher-router detected)") {
		t.Fatal("plain re-init must refresh the routing subsection back out")
	}
	if string(back) != string(plain) {
		t.Fatal("round-trip (plain → router → plain) must restore the original block")
	}
}
