// SPDX-License-Identifier: MIT

package server

import "sync"

// sseEvent is one event published on the /v1/events SSE stream (#654).
// Type is the SSE `event:` name; Payload is marshalled to the `data:`
// line (with `type` folded in). ProjectID is pulled out for the
// `?project=` subscriber filter so the filter doesn't have to reach
// into Payload.
type sseEvent struct {
	Type      string
	ProjectID string
	Payload   map[string]any
}

// eventBus fans index lifecycle events out to every live /v1/events
// subscriber (#654). It is deliberately tiny: subscribers register a
// buffered channel on connect and drop it on disconnect, and publish is
// non-blocking — a subscriber whose buffer is full misses the event
// rather than stalling the indexer goroutine that called publish.
//
// SSE is best-effort by contract: a missed index_started is corrected
// by the following index_complete, and a reconnecting client re-reads
// the binary_drift snapshot. So dropping under backpressure is the
// right failure mode, not blocking.
type eventBus struct {
	mu   sync.Mutex
	subs map[chan sseEvent]struct{}
}

func newEventBus() *eventBus {
	return &eventBus{subs: make(map[chan sseEvent]struct{})}
}

// subscribe returns a receive-only event channel and an unsubscribe
// func. The buffer absorbs short bursts; once full, publish drops for
// this subscriber. The unsubscribe func is idempotent and closes the
// channel so the reader's range/select sees a clean shutdown.
func (b *eventBus) subscribe() (<-chan sseEvent, func()) {
	ch := make(chan sseEvent, 32)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()

	var once sync.Once
	unsub := func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subs, ch)
			close(ch)
			b.mu.Unlock()
		})
	}
	return ch, unsub
}

// publish fans an event out to every current subscriber, non-blocking.
// Safe to call from the indexing goroutine.
func (b *eventBus) publish(ev sseEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- ev:
		default: // slow subscriber — drop rather than block the indexer
		}
	}
}

// subscriberCount reports how many SSE clients are currently connected.
// Drives the eventbus_test.go subscribe/publish lifecycle tests; could
// feed a future health field once `/v1/health` learns SSE liveness.
func (b *eventBus) subscriberCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}
