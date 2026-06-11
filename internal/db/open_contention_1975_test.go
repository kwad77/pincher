// SPDX-License-Identifier: MIT

package db

import (
	"context"
	"testing"
	"time"
)

// #1975 regression: a schema-current store must Open instantly even
// while another connection holds the SQLite write lock. Pre-fix,
// migrate() ran baseline DDL + the schema_version bootstrap INSERT on
// EVERY open, so a held writer (a long `pincher index --force`) blocked
// every new MCP server / `pincher web` / health probe on the machine
// for the busy-timeout/retry window (~17s) and then killed it.
//
// The writer hold here is a real SQLite write transaction (BEGIN
// IMMEDIATE acquires the write lock at begin) on an independent
// connection — SQLite locking is per-connection, so same-process
// contention is identical to cross-process contention.

// holdWriteLock pins a connection from s's writer pool and opens an
// immediate write transaction on it. Returns a release func.
func holdWriteLock(t *testing.T, s *Store) func() {
	t.Helper()
	ctx := context.Background()
	conn, err := s.DB().Conn(ctx)
	if err != nil {
		t.Fatalf("pin writer conn: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		conn.Close()
		t.Fatalf("BEGIN IMMEDIATE: %v", err)
	}
	return func() {
		_, _ = conn.ExecContext(ctx, `ROLLBACK`)
		conn.Close()
	}
}

func TestOpen_SchemaCurrent_DoesNotContendWithHeldWriter(t *testing.T) {
	dir := t.TempDir()
	holder, err := Open(dir) // fresh DB: migrates to current, uncontended
	if err != nil {
		t.Fatalf("initial Open: %v", err)
	}
	defer holder.Close()

	release := holdWriteLock(t, holder)
	defer release()

	start := time.Now()
	s, err := Open(dir)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Open while writer held: %v (after %v)", err, elapsed)
	}
	defer s.Close()

	// Pre-fix this path burned >=15s of busy-timeout and then failed.
	// The fast path is pure reads; 3s is a generous CI margin while still
	// proving no write entered the 5s busy_timeout window.
	if elapsed > 3*time.Second {
		t.Fatalf("Open took %v under writer contention — a startup write slipped back in (busy_timeout window entered)", elapsed)
	}

	// The opened store must actually serve reads.
	if _, err := s.ListProjects(); err != nil {
		t.Fatalf("ListProjects on contention-opened store: %v", err)
	}
}

func TestOpenReadOnly_DoesNotContendWithHeldWriter(t *testing.T) {
	dir := t.TempDir()
	holder, err := Open(dir)
	if err != nil {
		t.Fatalf("initial Open: %v", err)
	}
	defer holder.Close()

	release := holdWriteLock(t, holder)
	defer release()

	start := time.Now()
	s, err := OpenReadOnly(dir)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("OpenReadOnly while writer held: %v (after %v)", err, elapsed)
	}
	defer s.Close()
	if elapsed > 3*time.Second {
		t.Fatalf("OpenReadOnly took %v under writer contention", elapsed)
	}
	if _, err := s.ListProjects(); err != nil {
		t.Fatalf("ListProjects on read-only store: %v", err)
	}
}

// The second Open of a current store must report "no migrations ran" —
// pinning that the fast path populates the startup-migration bookkeeping
// identically to the old full pass.
func TestOpen_FastPath_ReportsNoStartupMigrations(t *testing.T) {
	dir := t.TempDir()
	first, err := Open(dir)
	if err != nil {
		t.Fatalf("initial Open: %v", err)
	}
	first.Close()

	s, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s.Close()
	inv, from, to := s.LastStartupMigrationInvalidates()
	if from != to || from != CurrentSchemaVersion() {
		t.Errorf("fast-path startup migration range = v%d→v%d, want v%d→v%d", from, to, CurrentSchemaVersion(), CurrentSchemaVersion())
	}
	if inv.All || len(inv.Languages) != 0 {
		t.Errorf("fast-path reported invalidates %+v, want none", inv)
	}
}
