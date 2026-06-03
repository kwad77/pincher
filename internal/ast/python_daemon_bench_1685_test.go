// SPDX-License-Identifier: MIT

package ast

import (
	"testing"
	"time"
)

// #1685 v0.88: bench gate for the Python AST persistent subprocess
// (#1626). The daemon's whole reason to exist is amortising the
// per-file process-spawn + interpreter-init cost (~80ms cold on
// Windows) across many extract calls. These benchmarks + the ratio
// test below are the committed proof that it does.
//
// Why a ratio assertion, not an absolute: subprocess-spawn timing is
// hardware- and OS-dependent (Windows process creation is far slower
// than Linux fork). An absolute "daemon must be under Nms" gate would
// be a CI flake generator. The daemon-IS-faster-than-spawn invariant
// holds on every platform, so the test asserts the ratio.

// pythonBenchSource is a small-but-representative module — class +
// methods + function + imports. Big enough that ast.parse does real
// work, small enough that the spawn cost dominates (which is exactly
// the regime the daemon optimises).
var pythonBenchSource = []byte(`"""bench fixture module."""
import os
import sys

class Widget:
    def __init__(self, name):
        self.name = name

    def render(self):
        return "<" + self.name + ">"

def build(name):
    return Widget(name)

def main():
    w = build("bench")
    sys.stdout.write(w.render())
`)

// BenchmarkPythonExtract_PerFileSpawn measures the legacy path: one
// fresh python3 subprocess per file. Every iteration pays the full
// spawn + interpreter-init cost.
func BenchmarkPythonExtract_PerFileSpawn(b *testing.B) {
	if !PythonAvailable() {
		b.Skip("no working CPython 3 on PATH")
	}
	// Force the daemon env-var OFF so extractPythonAST takes the
	// per-file spawn path — otherwise an ambient PINCHER_PYTHON_AST_
	// DAEMON=1 would silently route this through the daemon and the
	// "spawn" measurement would be meaningless.
	b.Setenv("PINCHER_PYTHON_AST_DAEMON", "0")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok := extractPythonAST(pythonBenchSource, "bench.py"); !ok {
			b.Fatal("per-file extract returned ok=false")
		}
	}
}

// BenchmarkPythonExtract_Daemon measures the persistent-subprocess
// path: the first iteration pays the spawn, every subsequent one is a
// stdin/stdout round-trip against the already-running interpreter.
func BenchmarkPythonExtract_Daemon(b *testing.B) {
	if !PythonAvailable() {
		b.Skip("no working CPython 3 on PATH")
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok := defaultPythonRunner.extract("bench.py", pythonBenchSource); !ok {
			b.Fatal("daemon extract returned ok=false")
		}
	}
}

// TestPythonDaemon_FasterThanPerFileSpawn_1685 is the gate: across a
// fixed batch of files, the daemon must beat per-file spawn. This is
// the #1685 acceptance — "a committed benchmark proving the per-file
// amortisation." Expressed as a ratio so it holds cross-platform.
func TestPythonDaemon_FasterThanPerFileSpawn_1685(t *testing.T) {
	if !PythonAvailable() {
		t.Skip("no working CPython 3 on PATH")
	}
	// extractPythonAST below must take the per-file spawn path —
	// pin the daemon env-var off regardless of the ambient value.
	t.Setenv("PINCHER_PYTHON_AST_DAEMON", "0")
	const batch = 12 // enough files that spawn cost dominates the spawn path

	// Warm the daemon once so its own one-time spawn isn't charged to
	// the measured loop — the daemon's value is steady-state, and the
	// per-file path has no equivalent warmup to amortise.
	if _, ok := defaultPythonRunner.extract("warm.py", pythonBenchSource); !ok {
		t.Fatal("daemon warmup extract failed")
	}

	spawnStart := time.Now()
	for i := 0; i < batch; i++ {
		if _, ok := extractPythonAST(pythonBenchSource, "bench.py"); !ok {
			t.Fatalf("per-file extract %d failed", i)
		}
	}
	spawnDur := time.Since(spawnStart)

	daemonStart := time.Now()
	for i := 0; i < batch; i++ {
		if _, ok := defaultPythonRunner.extract("bench.py", pythonBenchSource); !ok {
			t.Fatalf("daemon extract %d failed", i)
		}
	}
	daemonDur := time.Since(daemonStart)

	t.Logf("#1685 batch=%d: per-file-spawn=%v, daemon=%v (%.1f× faster)",
		batch, spawnDur, daemonDur, float64(spawnDur)/float64(daemonDur))

	// The invariant: the daemon must be strictly faster across the
	// batch. No absolute threshold — just the directional gate. If
	// this ever fails, the daemon is not amortising and #1626's
	// premise is broken.
	if daemonDur >= spawnDur {
		t.Errorf("daemon (%v) was not faster than per-file spawn (%v) across %d files — "+
			"the persistent-subprocess amortisation (#1626) is not working",
			daemonDur, spawnDur, batch)
	}
}
