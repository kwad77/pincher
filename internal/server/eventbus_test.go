// SPDX-License-Identifier: MIT

package server

import (
	"sync"
	"testing"
	"time"
)

// subscriberCount has been a load-bearing diagnostic in eventbus.go's
// comment since #654 ("Used by tests…") but no test actually called it
// until now — surfaced as a dead_code candidate during the v0.58
// dogfood sweep. Wiring up the lifecycle test the comment promised
// closes the loop and pins the count() contract against future
// eventBus refactors.

func TestEventBus_SubscriberCountTracksLifecycle(t *testing.T) {
	t.Parallel()
	bus := newEventBus()

	if got := bus.subscriberCount(); got != 0 {
		t.Fatalf("fresh bus has %d subscribers, want 0", got)
	}

	_, unsub1 := bus.subscribe()
	_, unsub2 := bus.subscribe()
	if got := bus.subscriberCount(); got != 2 {
		t.Fatalf("after 2 subscribes, count = %d, want 2", got)
	}

	unsub1()
	if got := bus.subscriberCount(); got != 1 {
		t.Fatalf("after 1 unsubscribe, count = %d, want 1", got)
	}

	// Idempotent unsubscribe — calling twice must not double-decrement.
	unsub1()
	if got := bus.subscriberCount(); got != 1 {
		t.Fatalf("after double-unsubscribe, count = %d, want 1 (idempotent)", got)
	}

	unsub2()
	if got := bus.subscriberCount(); got != 0 {
		t.Fatalf("after all unsubscribes, count = %d, want 0", got)
	}
}

func TestEventBus_PublishFansOutToAllSubscribers(t *testing.T) {
	t.Parallel()
	bus := newEventBus()
	ch1, unsub1 := bus.subscribe()
	ch2, unsub2 := bus.subscribe()
	t.Cleanup(unsub1)
	t.Cleanup(unsub2)

	ev := sseEvent{Type: "index_complete", ProjectID: "p1", Payload: map[string]any{"k": "v"}}
	bus.publish(ev)

	for i, ch := range []<-chan sseEvent{ch1, ch2} {
		select {
		case got := <-ch:
			if got.Type != ev.Type || got.ProjectID != ev.ProjectID {
				t.Errorf("subscriber %d got %+v, want %+v", i, got, ev)
			}
		case <-time.After(time.Second):
			t.Errorf("subscriber %d timed out waiting for event", i)
		}
	}
}

func TestEventBus_PublishNonBlockingUnderBackpressure(t *testing.T) {
	t.Parallel()
	bus := newEventBus()
	_, unsub := bus.subscribe()
	t.Cleanup(unsub)

	// Subscriber buffer is 32. Fire 1000 events without draining — must
	// not deadlock the publisher.
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			bus.publish(sseEvent{Type: "x"})
		}
		close(done)
	}()

	select {
	case <-done:
		// good — publisher returned despite full subscriber buffer.
	case <-time.After(2 * time.Second):
		t.Fatal("publish deadlocked under backpressure (slow subscriber should be skipped, not blocked)")
	}
	wg.Wait()
}
