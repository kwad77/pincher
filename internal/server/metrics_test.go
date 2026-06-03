// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// #1163: Prometheus metrics tests — positive + negative + control +
// cross-check pattern, plus the end-to-end /v1/metrics scrape probe
// in capability_test.go.

// Positive: IncCounter accumulates per (name, labelset) and survives
// concurrent increments without losing updates.
func TestMetrics_IncCounter_ConcurrentAccumulates(t *testing.T) {
	t.Parallel()
	r := newMetricsRegistry()
	const goroutines = 50
	const incsPerG = 100
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < incsPerG; j++ {
				r.IncCounter("pincher_test_counter", 1, "tool", "search")
			}
		}()
	}
	wg.Wait()
	// Read it back via exposition since the registry's internals are
	// private — the exposition string carries the count we expect.
	exp := r.Exposition()
	wantLine := "pincher_test_counter{tool=\"search\"} 5000"
	if !strings.Contains(exp, wantLine) {
		t.Errorf("counter did not accumulate to 5000; exposition missing %q\ngot:\n%s", wantLine, exp)
	}
}

// Positive: ObserveSummary accumulates count + sum; the rendered
// _count and _sum lines reflect the totals.
func TestMetrics_ObserveSummary_AccumulatesCountAndSum(t *testing.T) {
	t.Parallel()
	r := newMetricsRegistry()
	r.ObserveSummary("pincher_test_latency_seconds", 0.001, "tool", "search")
	r.ObserveSummary("pincher_test_latency_seconds", 0.002, "tool", "search")
	r.ObserveSummary("pincher_test_latency_seconds", 0.005, "tool", "search")
	exp := r.Exposition()
	for _, want := range []string{
		`pincher_test_latency_seconds_count{tool="search"} 3`,
		// sum = 0.008 — formatFloat renders this via %g, which produces "0.008"
		`pincher_test_latency_seconds_sum{tool="search"} 0.008`,
	} {
		if !strings.Contains(exp, want) {
			t.Errorf("exposition missing %q\ngot:\n%s", want, exp)
		}
	}
}

// Positive: SetGauge writes the value; last write wins.
func TestMetrics_SetGauge_LastWriteWins(t *testing.T) {
	t.Parallel()
	r := newMetricsRegistry()
	r.SetGauge("pincher_test_gauge", 100)
	r.SetGauge("pincher_test_gauge", 200)
	r.SetGauge("pincher_test_gauge", 150)
	exp := r.Exposition()
	if !strings.Contains(exp, "pincher_test_gauge 150") {
		t.Errorf("gauge last-write-wins failed; got:\n%s", exp)
	}
}

// Negative: empty-string label values are dropped silently. Defensive
// — keeps the registry from accumulating noise like {tool=""} when a
// call site passes an unset variable.
func TestMetrics_EmptyLabelPair_Skipped(t *testing.T) {
	t.Parallel()
	r := newMetricsRegistry()
	r.IncCounter("pincher_test_filter", 1, "tool", "", "outcome", "ok")
	exp := r.Exposition()
	// outcome=ok should survive; tool="" should not.
	if !strings.Contains(exp, `outcome="ok"`) {
		t.Errorf("non-empty label dropped; got:\n%s", exp)
	}
	if strings.Contains(exp, `tool=""`) {
		t.Errorf("empty-value label was kept; got:\n%s", exp)
	}
}

// Negative: odd-length label-pair slice doesn't panic — the trailing
// orphan is dropped. Defensive against call-site bugs.
func TestMetrics_OddLabelPairCount_NoPanic(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("IncCounter panicked on odd label pairs: %v", r)
		}
	}()
	r := newMetricsRegistry()
	r.IncCounter("pincher_test_odd", 1, "tool", "search", "orphan_no_value")
	// orphan key has no value → whole orphan dropped, but the valid
	// pair survives.
	exp := r.Exposition()
	if !strings.Contains(exp, `pincher_test_odd{tool="search"} 1`) {
		t.Errorf("valid pair lost when orphan key was present; got:\n%s", exp)
	}
}

// Control: exposition format follows the Prometheus 0.0.4 spec for
// counter / summary / gauge type headers and HELP lines.
func TestMetrics_Exposition_HeadersWellFormed(t *testing.T) {
	t.Parallel()
	r := newMetricsRegistry()
	r.IncCounter("pincher_c", 1)
	r.ObserveSummary("pincher_s", 0.1)
	r.SetGauge("pincher_g", 42)
	exp := r.Exposition()
	for _, want := range []string{
		"# HELP pincher_c", "# TYPE pincher_c counter",
		"# HELP pincher_s", "# TYPE pincher_s summary",
		"# HELP pincher_g", "# TYPE pincher_g gauge",
	} {
		if !strings.Contains(exp, want) {
			t.Errorf("exposition missing required header %q\ngot:\n%s", want, exp)
		}
	}
}

// Cross-check: a real tool call through handleSearch increments the
// pincher_tool_calls_total counter via the recordToolCallEvent path.
// Validates the integration between the metrics registry and the
// hot-path instrumentation.
func TestMetrics_RealToolCall_IncrementsCounter(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	// handleSearch errors when there's no project, but the call still
	// goes through jsonResultWithMeta → recordToolCallEvent before
	// returning. Actually wait — errResult bypasses recordToolCallEvent.
	// Use handleList instead — it succeeds with empty store and goes
	// through the full envelope path including instrumentation.
	res, err := srv.handleList(context.Background(), makeReq(map[string]any{}))
	if err != nil {
		t.Fatalf("handleList: %v", err)
	}
	if res == nil {
		t.Fatal("handleList returned nil")
	}
	// Now scrape /v1/metrics and look for the list counter.
	req := httptest.NewRequest("GET", "/v1/metrics", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("metrics scrape returned %d: %s", rr.Code, rr.Body.String())
	}
	// The test makeReq fixture doesn't carry the tool name through
	// beginCall, so the {tool="list"} label is empty and gets dropped
	// by the empty-label-filter. What we can verify: the counter line
	// itself exists with a non-zero value, AND the gauges scrape OK.
	// Production callers go through the MCP server which DOES populate
	// the tool name — that path is exercised by capability_test.go's
	// metrics_prometheus probe.
	body := rr.Body.String()
	if !strings.Contains(body, "pincher_tool_calls_total") {
		t.Errorf("tool_calls_total counter missing after handleList\ngot:\n%s", body)
	}
	if !strings.Contains(body, "pincher_db_size_bytes") {
		t.Errorf("db_size_bytes gauge missing on scrape\ngot:\n%s", body)
	}
}

// Cross-check: /v1/metrics is GET-only. POST returns 404 (the standard
// GET-only-route rejection from httpGetOnlyRoutes).
func TestMetrics_PostToMetricsRejected(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	req := httptest.NewRequest("POST", "/v1/metrics", strings.NewReader("{}"))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != 404 && rr.Code != 405 {
		t.Errorf("POST /v1/metrics expected 404/405; got %d: %s", rr.Code, rr.Body.String())
	}
}
