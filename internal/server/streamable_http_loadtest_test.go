// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"fmt"
	"net/http/httptest"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kwad77/pincher/internal/db"
)

// streamable-HTTP MCP concurrent-session loadtest (#687). #651 landed the
// transport as a single in-process *mcp.Server mounted on the HTTP
// gateway; the wiring tests (streamable_http_test.go) cover the contract
// but nothing verifies behaviour under concurrent load. Each MCP session
// opens its own JSON-RPC id keyspace and SSE stream against the shared
// singleton — this test pins that N sessions compose without response
// interleaving or goroutine leaks, so a router can commit to
// streamable-HTTP as a primary backend with a documented bound.
//
// The strict interleaving detector is `symbols`: session K calls it with
// an id only session K's seeded symbol satisfies, then asserts the
// response names fnK. If session K's request id ever got session J's
// response, K would see fnJ and the test fails loudly. `health` and
// `search` are mixed in for round-trip variety per the issue's
// acceptance list, asserted only for non-error.

// loadtestSessionCount sweeps concurrency. The issue asks for
// N=10/20/50/100; 100 stays in here because the calls are in-process and
// fast, but if CI ever flakes on the top rung, drop it — the 10/20/50
// rungs already exercise the contention path.
var loadtestSessionCounts = []int{10, 20, 50, 100}

const loadtestCallsPerSession = 10

func TestStreamableHTTP_ConcurrentSessions(t *testing.T) {
	for _, n := range loadtestSessionCounts {
		n := n
		t.Run(fmt.Sprintf("N=%d", n), func(t *testing.T) {
			runStreamableLoadtest(t, n, loadtestCallsPerSession)
		})
	}
}

func runStreamableLoadtest(t *testing.T, sessions, callsPer int) {
	srv, store, _ := newTestServer(t)
	srv.SetMCPHTTPPath("/mcp")

	const projectID = "loadtest"
	mustUpsertProject(t, store, projectID, "/tmp/loadtest", "loadtest")

	// One uniquely-named symbol per session — the interleaving detector's
	// fixture. fnK is only reachable via session K's id, so a crossed
	// response surfaces as a name mismatch.
	syms := make([]db.Symbol, 0, sessions)
	for k := 0; k < sessions; k++ {
		syms = append(syms, db.Symbol{
			ID:                   loadtestSymbolID(k),
			ProjectID:            projectID,
			FilePath:             fmt.Sprintf("loadtest/file%d.go", k),
			Name:                 loadtestSymbolName(k),
			QualifiedName:        "pkg." + loadtestSymbolName(k),
			Kind:                 "Function",
			Language:             "Go",
			ExtractionConfidence: 1.0,
		})
	}
	mustUpsertSymbols(t, store, syms)

	httpServer := httptest.NewServer(srv)
	// CloseClientConnections before Close: the streamable transport keeps
	// a standalone SSE GET stream open per session for server→client
	// notifications. httptest.Server.Close() blocks until every
	// outstanding request finishes, so without forcibly dropping those
	// long-lived streams the teardown deadlocks. Sessions are explicitly
	// Close()'d in runOneSession; this is the belt-and-suspenders for any
	// stream the client's DELETE didn't unwind.
	defer func() {
		httpServer.CloseClientConnections()
		httpServer.Close()
	}()
	endpoint := httpServer.URL + "/mcp"

	// Baseline goroutine count after one warmup connect/close — the first
	// session primes lazily-initialized server state (the sync.Once
	// handler, reader-pool connections) that would otherwise look like a
	// "leak" against a pre-warmup baseline.
	warmupSession(t, endpoint, projectID)
	settleGoroutines()
	baseline := runtime.NumGoroutine()

	var (
		durMu sync.Mutex
		durs  []time.Duration
		wg    sync.WaitGroup
	)
	errCh := make(chan error, sessions)

	for k := 0; k < sessions; k++ {
		k := k
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := runOneSession(endpoint, k, callsPer, &durMu, &durs); err != nil {
				errCh <- fmt.Errorf("session %d: %w", k, err)
			}
		}()
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Error(err)
	}

	// p95 latency — reported, not gated (the issue defers gating until a
	// real-world bound exists). Still surfaced so a regression is visible
	// in test output.
	if len(durs) > 0 {
		sort.Slice(durs, func(i, j int) bool { return durs[i] < durs[j] })
		p95 := durs[int(float64(len(durs))*0.95)]
		t.Logf("N=%d: %d calls, p95 round-trip latency = %v", sessions, len(durs), p95)
	}

	// Leak assertion: after every session closes, the goroutine count must
	// return to ~baseline. A per-session leak (an SSE stream goroutine not
	// torn down on session.Close) would scale with `sessions`, so the
	// tolerance is a small constant, not a fraction of N.
	settleGoroutines()
	after := runtime.NumGoroutine()
	const leakTolerance = 5
	if after > baseline+leakTolerance {
		t.Errorf("goroutine leak: baseline=%d after=%d (tolerance %d) — a per-session stream goroutine likely outlived session.Close",
			baseline, after, leakTolerance)
	}
}

// runOneSession opens its own MCP client session over streamable-HTTP and
// performs callsPer round-trips, rotating through health / search /
// symbols. The symbols call is the strict per-session interleaving
// assertion; health and search are checked only for non-error.
func runOneSession(endpoint string, k, callsPer int, durMu *sync.Mutex, durs *[]time.Duration) error {
	ctx := context.Background()
	transport := &mcp.StreamableClientTransport{Endpoint: endpoint}
	client := mcp.NewClient(&mcp.Implementation{Name: "loadtest-client", Version: "v0"}, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer session.Close()

	wantName := loadtestSymbolName(k)
	for i := 0; i < callsPer; i++ {
		var (
			toolName string
			args     map[string]any
		)
		switch i % 3 {
		case 0:
			toolName, args = "health", map[string]any{"project": "loadtest"}
		case 1:
			toolName, args = "search", map[string]any{"query": wantName, "project": "loadtest"}
		default:
			toolName, args = "symbols", map[string]any{"ids": []string{loadtestSymbolID(k)}, "project": "loadtest"}
		}

		start := time.Now()
		res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: toolName, Arguments: args})
		elapsed := time.Since(start)
		durMu.Lock()
		*durs = append(*durs, elapsed)
		durMu.Unlock()

		if err != nil {
			return fmt.Errorf("call %d (%s): %w", i, toolName, err)
		}
		if res.IsError {
			return fmt.Errorf("call %d (%s) returned IsError: %s", i, toolName, contentText(res))
		}
		// Strict interleaving check on the symbols call: the response must
		// name THIS session's symbol. A crossed response names fnJ.
		if toolName == "symbols" {
			body := contentText(res)
			if !strings.Contains(body, wantName) {
				return fmt.Errorf("call %d (symbols): response does not mention %q — possible cross-session interleaving:\n%s",
					i, wantName, body)
			}
			for j := 0; j < callsPerSessionGuard; j++ {
				other := loadtestSymbolName(otherSession(k, j))
				if other != wantName && strings.Contains(body, other) {
					return fmt.Errorf("call %d (symbols): response for session %d mentions %q from another session — interleaving",
						i, k, other)
				}
			}
		}
	}
	return nil
}

// callsPerSessionGuard bounds the cross-contamination scan in
// runOneSession — checking a handful of other sessions' names is enough
// to catch interleaving without an O(N²) scan over every session.
const callsPerSessionGuard = 8

// otherSession returns a session index distinct from k for the
// cross-contamination scan — just k offset by j+1, wrapped. The exact
// indices don't matter; we only need names that aren't k's.
func otherSession(k, j int) int {
	return (k + j + 1) % 997
}

func loadtestSymbolName(k int) string {
	// Letter-only suffix keeps the name a single FTS token (digits can
	// split under some tokenizers) so the `search` round-trip stays a
	// clean exact-name probe.
	return "loadtestsym" + intToLetters(k)
}

func loadtestSymbolID(k int) string {
	return fmt.Sprintf("loadtest/file%d.go::pkg.%s#Function", k, loadtestSymbolName(k))
}

// intToLetters maps a non-negative int to a base-26 lowercase-letter
// string (0→a, 25→z, 26→ba, …) so every session gets a unique,
// digit-free, single-token symbol name.
func intToLetters(n int) string {
	if n == 0 {
		return "a"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('a' + n%26)}, b...)
		n /= 26
	}
	return string(b)
}

// warmupSession opens and closes one session so lazily-initialized server
// state (the sync.Once streamable handler, reader-pool connections) is
// primed before the goroutine baseline is captured.
func warmupSession(t *testing.T, endpoint, projectID string) {
	t.Helper()
	ctx := context.Background()
	transport := &mcp.StreamableClientTransport{Endpoint: endpoint}
	client := mcp.NewClient(&mcp.Implementation{Name: "warmup", Version: "v0"}, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("warmup connect: %v", err)
	}
	if _, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "health", Arguments: map[string]any{"project": projectID}}); err != nil {
		t.Fatalf("warmup call: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("warmup close: %v", err)
	}
}

// settleGoroutines gives in-flight teardown (SSE stream cancels, DELETE
// requests) a bounded window to finish before a goroutine count is read.
// Without it the leak assertion races session.Close's async cleanup.
func settleGoroutines() {
	prev := runtime.NumGoroutine()
	for i := 0; i < 50; i++ {
		time.Sleep(20 * time.Millisecond)
		runtime.GC()
		cur := runtime.NumGoroutine()
		if cur == prev {
			return
		}
		prev = cur
	}
}

// contentText flattens a CallToolResult's content to a string for
// substring assertions — the client-side mirror of textOf, tolerant of
// an empty content slice (returns "").
func contentText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}
