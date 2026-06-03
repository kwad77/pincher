// SPDX-License-Identifier: MIT

package db

import (
	"testing"
	"time"
)

// #1824: TraceViaCTEScoped and the session / all-time metric reads are
// classified reader-routed in db_test.go, but were implemented against
// s.db — the single-writer connection (SetMaxOpenConns(1)). Every such
// read then serialized behind indexer writes; `changes` fires one trace
// per changed symbol, so a `scope=all` run over a doc-heavy diff queued
// hundreds of traces behind the watcher and took 145-348s.
//
// This test checks out the single writer connection and holds it, then
// asserts each reader-classified method still completes promptly. A
// method still routed to s.db blocks on connection-pool exhaustion and
// trips the timeout.
func TestReaderRoutedMethods_DoNotBlockOnHeldWriter_1824(t *testing.T) {
	t.Parallel()
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	// Hold the single writer connection for the whole test. Any method
	// still routed to s.db will block waiting for a connection.
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatalf("begin writer-holding txn: %v", err)
	}
	defer tx.Rollback()

	run := func(name string, fn func() error) {
		t.Helper()
		done := make(chan error, 1)
		go func() { done <- fn() }()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("%s errored while the writer connection was held: %v", name, err)
			}
		case <-time.After(5 * time.Second):
			t.Errorf("%s blocked >5s with the writer connection held — "+
				"it is routed to s.db (the single-writer pool), not s.ro (#1824)", name)
		}
	}

	// The CTE runs against an empty graph — routing, not result
	// correctness, is under test, so no fixture data is needed.
	run("TraceViaCTEScoped", func() error {
		_, e := store.TraceViaCTEScoped("proj", "sym", "inbound", []string{"CALLS"}, 3)
		return e
	})
	run("GetSessions", func() error {
		_, e := store.GetSessions(10)
		return e
	})
	run("GetAllTimeSavings", func() error {
		_, _, _, _, e := store.GetAllTimeSavings()
		return e
	})
	run("GetAllTimeCallsByLanguage", func() error {
		_, e := store.GetAllTimeCallsByLanguage()
		return e
	})
	run("GetAllTimeQueryMetrics", func() error {
		_, e := store.GetAllTimeQueryMetrics()
		return e
	})
	run("ResolveStaleID", func() error {
		store.ResolveStaleID("proj", "old-id") // returns ("", false) on no row
		return nil
	})
}
