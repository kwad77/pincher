// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// #724: parentWatchLoop reaps the process when the parent goes away.
// When the parent stays alive, it must never fire onGone/hardExit.
func TestParentWatchLoop_ParentDies_ReapsGracefullyThenHard(t *testing.T) {
	t.Parallel()
	var onGone, hardExit atomic.Bool
	// Parent is "dead" from the first poll.
	aliveFn := func(int) bool { return false }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		parentWatchLoop(ctx, 4242, 5*time.Millisecond, 10*time.Millisecond, aliveFn,
			func() int { return 4242 }, // ppid unchanged — exercises pid_dead path
			func() { onGone.Store(true) },
			func() { hardExit.Store(true) })
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("parentWatchLoop did not return after parent death")
	}
	if !onGone.Load() {
		t.Error("onGone was not called when parent died")
	}
	if !hardExit.Load() {
		t.Error("hardExit backstop was not called")
	}
}

// When onGone cancels the context (the production wiring), the loop
// must NOT wait the full hard-exit grace — it returns as soon as ctx
// is done, but still calls hardExit as the backstop.
func TestParentWatchLoop_GracefulCancelShortCircuitsGrace(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var hardExit atomic.Bool

	const grace = 2 * time.Second
	start := time.Now()
	parentWatchLoop(ctx, 4242, 5*time.Millisecond, grace,
		func(int) bool { return false },
		func() int { return 4242 }, // ppid unchanged — pid_dead path fires
		cancel,                     // onGone cancels ctx — the real wiring
		func() { hardExit.Store(true) })
	elapsed := time.Since(start)

	if elapsed >= grace {
		t.Errorf("loop waited the full grace (%v) despite ctx cancel; took %v", grace, elapsed)
	}
	if !hardExit.Load() {
		t.Error("hardExit backstop should still fire after a graceful cancel")
	}
}

// #1321 v0.71: the PID-recycle gap. Pre-fix, when the real parent
// died and the kernel reissued its PID to an unrelated process,
// aliveFn(originalPpid) flipped back to true and the orphan loop
// continued indefinitely. Post-fix, currentPpidFn() returning 1 (or
// any value != originalPpid) is the reparent signal and fires onGone
// even when aliveFn says the PID is alive.
func TestParentWatchLoop_ReparentedDespiteAliveFn_Reaps_1321(t *testing.T) {
	t.Parallel()
	var onGone, hardExit atomic.Bool

	// Simulate the worst case: aliveFn says "yes, that PID is alive"
	// every time (a recycled-PID holder). Only the reparent signal
	// should fire onGone.
	aliveFn := func(int) bool { return true }
	currentPpidFn := func() int { return 1 } // reparented to init

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		parentWatchLoop(ctx, 4242, 5*time.Millisecond, 10*time.Millisecond,
			aliveFn, currentPpidFn,
			func() { onGone.Store(true) },
			func() { hardExit.Store(true) })
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("parentWatchLoop did not return after reparent signal")
	}
	if !onGone.Load() {
		t.Error("onGone was not called when current ppid changed to 1 (reparented)")
	}
	if !hardExit.Load() {
		t.Error("hardExit backstop was not called")
	}
}

// #1321 cross-check: ppid changed to a NEW non-1 value (e.g. a debugger
// attached and inherited us) is still treated as reparented — we have
// no way to tell that's safe vs the original-parent-died-then-immediately-
// reattached case. Better to err on reaping than to keep an ambiguous
// orphan around. The reaped behaviour matches v0.69's monotonic-binary-
// stamp guard in spirit: when the world changes underneath us, exit
// cleanly rather than persist stale assumptions.
func TestParentWatchLoop_PpidChangedToNonOne_Reaps_1321(t *testing.T) {
	t.Parallel()
	var onGone atomic.Bool

	aliveFn := func(int) bool { return true }
	currentPpidFn := func() int { return 99999 } // any non-original value

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		parentWatchLoop(ctx, 4242, 5*time.Millisecond, 10*time.Millisecond,
			aliveFn, currentPpidFn,
			func() { onGone.Store(true) },
			func() {})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("parentWatchLoop did not return when ppid drifted")
	}
	if !onGone.Load() {
		t.Error("onGone was not called when current ppid != original ppid")
	}
}

// #1321 negative control: nil currentPpidFn falls back to the
// pre-#1321 pid-liveness behaviour. Ensures we don't crash if a future
// caller wires the param wrong, and gives Windows callers (where
// reparent doesn't apply) a safe degradation path.
func TestParentWatchLoop_NilCurrentPpidFn_FallsBackToAliveFn_1321(t *testing.T) {
	t.Parallel()
	var onGone atomic.Bool
	aliveFn := func(int) bool { return false }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		parentWatchLoop(ctx, 4242, 5*time.Millisecond, 10*time.Millisecond,
			aliveFn, nil, // explicitly nil
			func() { onGone.Store(true) },
			func() {})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("parentWatchLoop did not return when aliveFn returned false")
	}
	if !onGone.Load() {
		t.Error("onGone was not called via aliveFn fallback when currentPpidFn was nil")
	}
}

// A live parent → the loop just keeps polling and exits cleanly on
// ctx cancel, never reaping.
func TestParentWatchLoop_ParentAlive_NeverReaps(t *testing.T) {
	t.Parallel()
	var reaped atomic.Bool
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		parentWatchLoop(ctx, 4242, 5*time.Millisecond, 10*time.Millisecond,
			func(int) bool { return true }, // parent always alive
			func() int { return 4242 },     // ppid unchanged → not reparented
			func() { reaped.Store(true) },
			func() { reaped.Store(true) })
		close(done)
	}()

	time.Sleep(50 * time.Millisecond) // ~10 poll ticks
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("parentWatchLoop did not return on ctx cancel")
	}
	if reaped.Load() {
		t.Error("a live parent must never trigger reaping")
	}
}
