// SPDX-License-Identifier: MIT

package supervisor

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// waitForCond polls cond until it returns true or the timeout elapses.
func waitForCond(t *testing.T, cond func() bool, timeout time.Duration, what string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

// TestSupervisor_DegradedMode_AnswersRequestsAndRecovers (#1901): when
// respawn exhausts its retry budget, the supervisor must NOT exit (which
// would close the host's MCP transport for good). Instead it degrades —
// id-bearing client requests get a JSON-RPC error, the status tool
// reports degraded=true — and once spawning works again it recovers and
// resumes forwarding, all within the same Run call.
func TestSupervisor_DegradedMode_AnswersRequestsAndRecovers(t *testing.T) {
	clientStdinR, clientStdinW := io.Pipe()
	var clientStdout syncBuffer

	var (
		spawnMu    sync.Mutex
		spawnCount int
		failSpawns bool
		fakes      []*fakeInner
	)

	sup := &Supervisor{
		Stdin:                 clientStdinR,
		Stdout:                &clientStdout,
		Stderr:                io.Discard,
		ProbeInterval:         24 * time.Hour,
		RespawnAttempts:       2,
		RespawnRetryDelay:     5 * time.Millisecond,
		DegradedRetryInterval: 30 * time.Millisecond,
		spawnFn: func() (*innerProc, error) {
			spawnMu.Lock()
			defer spawnMu.Unlock()
			if failSpawns {
				return nil, errors.New("binary swap window")
			}
			spawnCount++
			f := newFakeInner(spawnCount)
			fakes = append(fakes, f)
			return f.makeProc(), nil
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runDone := make(chan error, 1)
	go func() { runDone <- sup.Run(ctx) }()

	// Wait for the initial spawn, then make every further spawn fail
	// and kill the inner — respawnWithRetry exhausts and degrades.
	waitForCond(t, func() bool {
		spawnMu.Lock()
		defer spawnMu.Unlock()
		return len(fakes) == 1
	}, 2*time.Second, "initial spawn")
	spawnMu.Lock()
	failSpawns = true
	first := fakes[0]
	spawnMu.Unlock()
	first.Close()

	waitForCond(t, func() bool { return sup.degraded.Load() }, 3*time.Second, "degraded flag")

	// A request while degraded gets a JSON-RPC error, not silence.
	if _, err := clientStdinW.Write([]byte(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"health","arguments":{}}}` + "\n")); err != nil {
		t.Fatalf("client write: %v", err)
	}
	waitForCond(t, func() bool {
		return bytes.Contains(clientStdout.Bytes(), []byte(`"id":7`))
	}, 2*time.Second, "degraded error reply")
	if !strings.Contains(clientStdout.String(), "inner unavailable") {
		t.Fatalf("expected inner-unavailable error, got %q", clientStdout.String())
	}

	// The supervisor's own status tool still answers, and reports
	// degraded=true with the reason.
	if _, err := clientStdinW.Write([]byte(`{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"` + SupervisorStatusToolName + `","arguments":{}}}` + "\n")); err != nil {
		t.Fatalf("client write: %v", err)
	}
	waitForCond(t, func() bool {
		return bytes.Contains(clientStdout.Bytes(), []byte(`\"degraded\": true`))
	}, 2*time.Second, "status reports degraded")

	// A notification (no id) while degraded must NOT get a reply.
	before := bytes.Count(clientStdout.Bytes(), []byte(`"error"`))
	if _, err := clientStdinW.Write([]byte(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{}}` + "\n")); err != nil {
		t.Fatalf("client write: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if after := bytes.Count(clientStdout.Bytes(), []byte(`"error"`)); after != before {
		t.Errorf("notification got a reply while degraded: %d -> %d error frames", before, after)
	}

	// Let spawns succeed again — the supervisor recovers in-place.
	spawnMu.Lock()
	failSpawns = false
	spawnMu.Unlock()
	waitForCond(t, func() bool { return !sup.degraded.Load() }, 3*time.Second, "recovery")

	// Traffic flows to the new inner again.
	if _, err := clientStdinW.Write([]byte(`{"jsonrpc":"2.0","id":9,"method":"tools/list","params":{}}` + "\n")); err != nil {
		t.Fatalf("client write: %v", err)
	}
	waitForCond(t, func() bool {
		spawnMu.Lock()
		defer spawnMu.Unlock()
		if len(fakes) < 2 {
			return false
		}
		return bytes.Contains(fakes[len(fakes)-1].Receive(), []byte(`"id":9`))
	}, 2*time.Second, "post-recovery forwarding")

	// Through the whole degrade/recover cycle, Run must not return.
	select {
	case err := <-runDone:
		t.Fatalf("Run returned during degraded/recovery cycle: %v", err)
	default:
	}

	clientStdinW.Close()
	cancel()
	<-runDone
}

// TestSupervisor_CircuitBreaker_DegradesByDefault (#1901): the breaker
// tripping no longer kills the supervisor unless ExitOnGiveUp is set.
func TestSupervisor_CircuitBreaker_DegradesByDefault(t *testing.T) {
	clientStdinR, clientStdinW := io.Pipe()
	var clientStdout syncBuffer

	var (
		spawnMu    sync.Mutex
		spawnCount int
	)

	sup := &Supervisor{
		Stdin:         clientStdinR,
		Stdout:        &clientStdout,
		Stderr:        io.Discard,
		ProbeInterval: 24 * time.Hour,
		MaxRestarts:   2,
		RestartWindow: 10 * time.Second,
		// Long enough that the test observes a stable degraded state
		// rather than racing the next retry.
		DegradedRetryInterval: time.Hour,
		spawnFn: func() (*innerProc, error) {
			spawnMu.Lock()
			defer spawnMu.Unlock()
			spawnCount++
			f := newFakeInner(spawnCount)
			// Close shortly after spawn to force a respawn loop that
			// trips the breaker.
			go func(f *fakeInner) {
				time.Sleep(20 * time.Millisecond)
				f.Close()
			}(f)
			return f.makeProc(), nil
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runDone := make(chan error, 1)
	go func() { runDone <- sup.Run(ctx) }()

	waitForCond(t, func() bool { return sup.degraded.Load() }, 5*time.Second, "degraded after breaker trip")

	status := sup.Status()
	if !status.Degraded {
		t.Error("Status().Degraded = false, want true")
	}
	if !strings.Contains(status.DegradedReason, "circuit breaker") {
		t.Errorf("DegradedReason = %q, want mention of circuit breaker", status.DegradedReason)
	}
	if status.DegradedSince == "" {
		t.Error("DegradedSince is empty while degraded")
	}

	// Run must still be alive.
	select {
	case err := <-runDone:
		t.Fatalf("Run returned after breaker trip without ExitOnGiveUp: %v", err)
	default:
	}

	clientStdinW.Close()
	cancel()
	<-runDone
}
