// SPDX-License-Identifier: MIT

package db

// Guide-coaching PR-15/17: LoopLedgerNonEmpty gates guide's loop-resume
// recommendation. When the branch was authored, loop_checkpoints was a
// future migration it didn't own; since the loop-substrate integration
// the table ships with schema v40 and Open() creates it. The helper
// must still treat a missing table (pre-v40 stores opened read-only,
// damaged stores) as a normal "no", and only answer "yes" when rows
// exist — so these tests DROP the migrated table where they need to
// exercise the absent path.

import "testing"

func TestLoopLedgerNonEmpty_TableAbsent(t *testing.T) {
	t.Parallel()
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	// Open() migrates to schema head, which creates loop_checkpoints
	// (v40). Drop it so this test genuinely exercises the table-absent
	// path the helper guards against.
	if _, err := store.db.Exec(`DROP TABLE loop_checkpoints`); err != nil {
		t.Fatalf("drop loop_checkpoints: %v", err)
	}

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

	// Replace the migrated v40 ledger with a minimal column shape
	// (project_id only) — pins that the helper depends on nothing
	// beyond the columns it queries.
	if _, err := store.db.Exec(`DROP TABLE loop_checkpoints`); err != nil {
		t.Fatalf("drop migrated loop_checkpoints: %v", err)
	}
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
