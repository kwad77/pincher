// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"log/slog"
	"os"
	"time"
)

// parentWatchInterval is how often a long-lived pincher process polls
// its parent's liveness. 30s keeps orphan-reaping latency low while the
// poll itself (one PID liveness check) is effectively free.
const parentWatchInterval = 30 * time.Second

// parentWatchHardExitGrace is how long the watcher waits after calling
// onGone (graceful cancel) before forcing a hard exit. If the graceful
// path tears the process down first, this never fires; it only matters
// when something downstream doesn't honour context cancellation.
const parentWatchHardExitGrace = 5 * time.Second

// watchParent reaps this process when its parent dies abnormally.
//
// #724: a stdio MCP child (or the supervisor) detects a *graceful*
// parent disconnect via stdin EOF. But a parent killed with SIGKILL,
// crashed, or lost to power-off may not close our stdin pipe — leaving
// us an orphan whose Watch() loop keeps running against the shared DB
// and stomping project metadata (binary_version, schema_version_at_index)
// for every other pincher process. Three such orphans were observed on
// one machine, one of them two days old.
//
// The parent PID is captured once at startup. On Unix a re-parent to
// init moves Getppid() to 1 while the *original* PID is now dead; on
// Windows Getppid() stays stale but the captured PID itself becomes
// dead. Polling pidIsAlive on the captured PID catches both.
//
// onGone is invoked once when the parent is detected gone — pass the
// process's context cancel so shutdown runs cleanly (session-stat flush,
// Watch() teardown). The watcher then waits parentWatchHardExitGrace and
// force-exits as a backstop. A no-op when there is no meaningful parent
// (ppid <= 1) — that includes intentionally-detached servers, which
// must NOT be reaped.
func watchParent(ctx context.Context, onGone func()) {
	ppid := os.Getppid()
	if ppid <= 1 {
		// No meaningful parent (already orphaned, or detached on Unix
		// via setsid). Detached HTTP servers rely on this no-op.
		return
	}
	go parentWatchLoop(ctx, ppid, parentWatchInterval, parentWatchHardExitGrace,
		pidIsAlive, os.Getppid, onGone, func() {
			slog.Warn("pincher.parent_gone.hard_exit", "ppid", ppid)
			os.Exit(0)
		})
}

// parentWatchLoop is the testable core of watchParent. On each tick it
// fires onGone (then waits hardExitGrace and calls hardExit) when any
// of these signals reports the original parent gone:
//
//  1. **Reparent signal** (#1321 v0.71): currentPpidFn() != ppid OR
//     <= 1. On Unix, the kernel reparents a child to init/launchd
//     (PID 1) when its real parent dies, so Getppid() will return 1
//     thereafter. This is PID-recycle-immune — the original PID being
//     re-issued to an unrelated process can't fool the reparent check.
//     User repro: three v0.58 stdio MCP children survived 10h / 14h /
//     25h+ after their Claude Code parent disappeared because the
//     pre-fix aliveFn(ppid) saw recycled PIDs as still-alive.
//
//  2. **PID-liveness signal** (pre-fix sole check): aliveFn(ppid) ==
//     false. Catches the macOS edge case where a process group leader
//     holds the PID open without reparenting, and catches Windows
//     (which doesn't reparent — Getppid() stays stale forever after
//     parent death). Kept as belt-and-suspenders.
//
// Returns early (without reaping) when ctx is cancelled — the normal
// shutdown path. Split out so tests can inject fake fns and short
// timings.
func parentWatchLoop(
	ctx context.Context,
	ppid int,
	interval time.Duration,
	hardExitGrace time.Duration,
	aliveFn func(int) bool,
	currentPpidFn func() int,
	onGone func(),
	hardExit func(),
) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			reason := ""
			if currentPpidFn != nil {
				cur := currentPpidFn()
				if cur != ppid {
					reason = "reparented"
				} else if cur <= 1 {
					// Defensive — currentPpidFn==ppid AND <=1 means the
					// captured ppid itself was ≤1, which watchParent's
					// early-return should have prevented. Treat as gone.
					reason = "ppid_le_1"
				}
			}
			if reason == "" && !aliveFn(ppid) {
				reason = "pid_dead"
			}
			if reason == "" {
				continue
			}
			slog.Warn("pincher.parent_gone",
				"ppid", ppid,
				"reason", reason,
				"action", "graceful_shutdown_then_exit")
			if onGone != nil {
				onGone()
			}
			select {
			case <-ctx.Done():
				// Graceful path tore us down inside the grace window —
				// no need for the hard backstop.
			case <-time.After(hardExitGrace):
			}
			if hardExit != nil {
				hardExit()
			}
			return
		}
	}
}
