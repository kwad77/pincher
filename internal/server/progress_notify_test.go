// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// #1080: runWithProgress emits notifications/progress during a
// long-running operation when the client supplied a progressToken.
//
// Audit shape:
//   - positive: live session + progressToken => client's
//     ProgressNotificationHandler receives at least the final
//     "complete" event with the right token + total.
//   - negative (no token): fn still runs, zero notifications emitted.
//   - negative (nil session): fn still runs, no panic.
//   - control: the notifier goroutine is joined before runWithProgress
//     returns — no leak, no post-return emission.

// fakeProgress returns a progressFunc backed by an atomic counter the
// test drives, plus a setter to advance it.
func fakeProgress(total int64) (progressFunc, func(done int64), func(active bool)) {
	var done atomic.Int64
	var active atomic.Bool
	active.Store(true)
	pf := func() (int64, int64, bool) { return done.Load(), total, active.Load() }
	return pf, func(d int64) { done.Store(d) }, func(a bool) { active.Store(a) }
}

func reqWithProgressToken(token any) *mcp.CallToolRequest {
	// Meta must be non-nil before SetProgressToken: the SDK's
	// setProgressToken creates a throwaway local map when GetMeta()
	// returns nil and never stores it back, so the token write is lost.
	// Real wire requests always arrive with Meta populated; only test
	// construction hits this edge.
	p := &mcp.CallToolParamsRaw{Name: "index", Meta: mcp.Meta{}}
	p.SetProgressToken(token)
	return &mcp.CallToolRequest{Params: p}
}

func TestRunWithProgress_EmitsToSubscribedClient(t *testing.T) {
	srv, _, _ := newTestServer(t)

	var (
		mu   sync.Mutex
		recv []*mcp.ProgressNotificationParams
	)
	handler := func(_ context.Context, req *mcp.ProgressNotificationClientRequest) {
		mu.Lock()
		recv = append(recv, req.Params)
		mu.Unlock()
	}

	_, cleanup := connectInMemoryClient(t, srv, &mcp.ClientOptions{
		ProgressNotificationHandler: handler,
	})
	defer cleanup()
	waitForSession(t, srv, 2*time.Second)

	pf, setDone, setActive := fakeProgress(10)
	err := srv.runWithProgress(context.Background(), reqWithProgressToken("tok-1"), pf, "indexed files", func() error {
		// Simulate a multi-tick operation so the poller has time to emit.
		for i := int64(1); i <= 10; i++ {
			setDone(i)
			time.Sleep(60 * time.Millisecond)
		}
		setActive(false)
		return nil
	})
	if err != nil {
		t.Fatalf("runWithProgress: %v", err)
	}

	// Wait for the terminal "complete" notification (Progress==Total==10).
	// Mid-stream events race with the final emit over the async in-memory
	// transport, so assert on the specific terminal event, not "the last
	// one received so far".
	var terminal *mcp.ProgressNotificationParams
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && terminal == nil {
		mu.Lock()
		for _, p := range recv {
			if p.Progress == 10 && p.Total == 10 {
				terminal = p
				break
			}
		}
		mu.Unlock()
		if terminal == nil {
			time.Sleep(5 * time.Millisecond)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(recv) == 0 {
		t.Fatalf("no progress notifications delivered to subscribed client")
	}
	if terminal == nil {
		t.Fatalf("terminal progress event (10/10) never delivered; got %d events", len(recv))
	}
	if terminal.ProgressToken != "tok-1" {
		t.Errorf("progressToken: want %q, got %v", "tok-1", terminal.ProgressToken)
	}
}

func TestRunWithProgress_NoTokenRunsWithoutEmitting(t *testing.T) {
	srv, _, _ := newTestServer(t)

	var count atomic.Int32
	handler := func(_ context.Context, _ *mcp.ProgressNotificationClientRequest) {
		count.Add(1)
	}
	_, cleanup := connectInMemoryClient(t, srv, &mcp.ClientOptions{
		ProgressNotificationHandler: handler,
	})
	defer cleanup()
	waitForSession(t, srv, 2*time.Second)

	ran := false
	pf, setDone, _ := fakeProgress(5)
	// Request WITHOUT a progressToken — opt-out path.
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "index"}}
	err := srv.runWithProgress(context.Background(), req, pf, "indexed files", func() error {
		ran = true
		setDone(5)
		time.Sleep(120 * time.Millisecond)
		return nil
	})
	if err != nil {
		t.Fatalf("runWithProgress: %v", err)
	}
	if !ran {
		t.Fatal("fn did not run on the no-token path")
	}
	// Give any stray goroutine a beat to (incorrectly) emit.
	time.Sleep(100 * time.Millisecond)
	if n := count.Load(); n != 0 {
		t.Errorf("no-token path emitted %d notifications, want 0", n)
	}
}

func TestRunWithProgress_NilSessionNoPanic(t *testing.T) {
	srv, _, _ := newTestServer(t)
	srv.mcpSessionMu.Lock()
	srv.mcpSession = nil
	srv.mcpSessionMu.Unlock()

	ran := false
	pf, _, _ := fakeProgress(3)
	err := srv.runWithProgress(context.Background(), reqWithProgressToken("tok-2"), pf, "indexed files", func() error {
		ran = true
		return nil
	})
	if err != nil {
		t.Fatalf("runWithProgress: %v", err)
	}
	if !ran {
		t.Fatal("fn did not run on the nil-session path")
	}
}

// TestRunWithProgress_NotifierJoinedBeforeReturn pins that the notifier
// goroutine cannot outlive the call: after runWithProgress returns, no
// further notifications are emitted even if progress would have changed.
func TestRunWithProgress_NotifierJoinedBeforeReturn(t *testing.T) {
	srv, _, _ := newTestServer(t)

	var (
		mu   sync.Mutex
		recv []*mcp.ProgressNotificationParams
	)
	handler := func(_ context.Context, req *mcp.ProgressNotificationClientRequest) {
		mu.Lock()
		recv = append(recv, req.Params)
		mu.Unlock()
	}
	_, cleanup := connectInMemoryClient(t, srv, &mcp.ClientOptions{
		ProgressNotificationHandler: handler,
	})
	defer cleanup()
	waitForSession(t, srv, 2*time.Second)

	pf, setDone, setActive := fakeProgress(2)
	_ = srv.runWithProgress(context.Background(), reqWithProgressToken("tok-3"), pf, "indexed files", func() error {
		setDone(2)
		time.Sleep(80 * time.Millisecond)
		setActive(false)
		return nil
	})

	// Settle: runWithProgress emits one legitimate "complete" event
	// before returning, but its delivery over the async in-memory
	// transport may land just after return. Wait for in-flight delivery
	// to quiesce before snapshotting the baseline, so we measure only
	// post-return *new* emits.
	time.Sleep(2 * progressNotifyInterval)
	mu.Lock()
	countAtReturn := len(recv)
	mu.Unlock()

	// Advance the fake counter AFTER return; if the polling goroutine had
	// leaked it would emit on the next tick.
	setActive(true)
	setDone(99)
	time.Sleep(3 * progressNotifyInterval)

	mu.Lock()
	countAfter := len(recv)
	mu.Unlock()
	if countAfter != countAtReturn {
		t.Errorf("notifier emitted %d more notifications after return — goroutine leaked", countAfter-countAtReturn)
	}
}
