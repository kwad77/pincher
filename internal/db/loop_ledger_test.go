// SPDX-License-Identifier: MIT

package db

// Guide-coaching PR-15/17: LoopLedgerNonEmpty gates guide's loop-resume
// recommendation. The loop_checkpoints table ships with schema v40,
// which this branch does not own — so the helper must treat a missing
// table as a normal "no", and only answer "yes" when rows exist.

import "testing"

func TestLoopLedgerNonEmpty_TableAbsent(t *testing.T) {
	t.Parallel()
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	if store.LoopLedgerNonEmpty("") {
		t.Error("LoopLedgerNonEmpty = true with no loop_checkpoints table")
	}
	if store.LoopLedgerNonEmpty("some-project") {
		t.Error("LoopLedgerNonEmpty(project) = true with no loop_checkpoints table")
	}
}

func TestLoopLedgerNonEmpty_TablePresent(t *testing.T) {
	t.Parallel()
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	// Simulate the v40 ledger: minimal column shape with project_id.
	if _, err := store.db.Exec(
		`CREATE TABLE loop_checkpoints (id INTEGER PRIMARY KEY, project_id TEXT, stage TEXT)`,
	); err != nil {
		t.Fatalf("create loop_checkpoints: %v", err)
	}

	// Empty table → still no.
	if store.LoopLedgerNonEmpty("p1") {
		t.Error("LoopLedgerNonEmpty = true on empty ledger")
	}

	if _, err := store.db.Exec(
		`INSERT INTO loop_checkpoints (project_id, stage) VALUES ('p1', 'deliver')`,
	); err != nil {
		t.Fatalf("insert checkpoint: %v", err)
	}

	if !store.LoopLedgerNonEmpty("p1") {
		t.Error("LoopLedgerNonEmpty(p1) = false with a checkpoint row")
	}
	if store.LoopLedgerNonEmpty("p2") {
		t.Error("LoopLedgerNonEmpty(p2) = true for a project with no rows")
	}
	// Unscoped probe: any rows at all.
	if !store.LoopLedgerNonEmpty("") {
		t.Error("LoopLedgerNonEmpty(\"\") = false with rows present")
	}
}
