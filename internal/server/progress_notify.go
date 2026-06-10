// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// progressNotifyInterval is the cadence at which the progress-notifier
// goroutine polls the indexer and emits a notifications/progress event.
// 400ms is fast enough for a responsive progress bar in a UI host without
// flooding the wire on a fast index pass. Skipping unchanged ticks (see
// runWithProgress) keeps a stalled or idle pass from emitting at all.
const progressNotifyInterval = 400 * time.Millisecond

// progressFunc reports the current progress of a long-running operation:
// done items, total items (0 if unknown), and whether the operation is
// still active. It must be safe to call from the notifier goroutine
// concurrently with the operation it observes — the indexer's atomic
// counters satisfy this.
type progressFunc func() (done, total int64, active bool)

// runWithProgress runs fn while emitting MCP notifications/progress events
// keyed on the request's progressToken (MCP 2025-11-25 progress utility,
// #1080). The events are polled from getProgress on a fixed cadence.
//
// The notification stream is strictly opt-in: a client that does not set a
// progressToken on the request, or a server with no live MCP session, pays
// zero overhead — fn runs directly with no goroutine. This keeps the common
// case (no progress wanted) free.
//
// Cancellation is NOT handled here and does not need to be: the MCP SDK maps
// an incoming notifications/cancelled to a jsonrpc2 connection cancel at the
// transport Preempt layer, which cancels the handler's ctx. fn's underlying
// work (indexer.Index) already honors ctx per-iteration, so a cancel
// propagates without any extra wiring. runWithProgress simply stops emitting
// when ctx is done.
//
// The notifier goroutine is guaranteed to stop and be joined before
// runWithProgress returns, so it cannot outlive the request or leak.
func (s *Server) runWithProgress(ctx context.Context, req *mcp.CallToolRequest, getProgress progressFunc, label string, fn func() error) error {
	token := tokenFromRequest(req)
	s.mcpSessionMu.Lock()
	sess := s.mcpSession
	s.mcpSessionMu.Unlock()
	if token == nil || sess == nil {
		// Opt-out fast path: no progressToken or no session — run fn
		// directly with zero notifier overhead.
		return fn()
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(progressNotifyInterval)
		defer ticker.Stop()
		var lastDone int64 = -1
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				done, total, active := getProgress()
				// Don't emit before the pass has actually started, or
				// when nothing changed since the last tick — a stalled or
				// idle pass produces no wire traffic.
				if !active && done == 0 {
					continue
				}
				if done == lastDone {
					continue
				}
				lastDone = done
				msg := fmt.Sprintf("%s %d", label, done)
				if total > 0 {
					msg = fmt.Sprintf("%s %d/%d", label, done, total)
				}
				// Best-effort: a failed notify (closed session, slow
				// client) must never fail the underlying operation.
				_ = sess.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
					ProgressToken: token,
					Progress:      float64(done),
					Total:         float64(total),
					Message:       msg,
				})
			}
		}
	}()

	err := fn()
	close(stop)
	wg.Wait()

	// Emit one final 100%-ish event so a UI host's progress bar lands at
	// the end instead of freezing at the last polled tick. Only when the
	// operation succeeded and we have a final count to report.
	if err == nil {
		if done, total, _ := getProgress(); done > 0 {
			final := total
			if final <= 0 {
				final = done
			}
			_ = sess.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
				ProgressToken: token,
				Progress:      float64(done),
				Total:         float64(final),
				Message:       fmt.Sprintf("%s complete (%d)", label, done),
			})
		}
	}
	return err
}

// tokenFromRequest extracts the MCP progressToken from a CallToolRequest,
// returning nil when the client did not request progress. Isolated so the
// SDK accessor shape lives in one place.
func tokenFromRequest(req *mcp.CallToolRequest) any {
	if req == nil || req.Params == nil {
		return nil
	}
	return req.Params.GetProgressToken()
}
