// SPDX-License-Identifier: MIT

package index

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// #1479: USES_VAR edges must SURVIVE re-index passes. The bug as
// filed (against v0.73): an Ansible project's USES_VAR edges showed
// in `pincher index --json-summary` but were absent from the DB —
// a second pass (the watcher, milliseconds after the CLI pass)
// wiped them via resolveUsesVar's DELETE-then-INSERT when its
// in-memory pending list was empty.
//
// The existing TestIndex_AnsibleUsesVar_BindsAcrossFiles_1165 only
// exercises a SINGLE index pass — it can't catch a second-pass wipe.
// These tests add the multi-pass assertions: index, then index
// again (no changes, and with one unrelated file touched), and
// assert the USES_VAR edge count is stable across all passes.
//
// On current code two mechanisms should keep them stable:
//   - #1627: USES_VAR pending edges are persisted to pending_edges
//     (appendDeferred(deferredUsesVar)), so the resolver can always
//     reload them even when the in-memory list is empty.
//   - #1629: a true no-change pass has totalFiles==0 so the resolve
//     block is skipped entirely; a one-unrelated-file pass scopes
//     the resolve to the changed file, leaving other files'
//     USES_VAR resolve_pass edges untouched.
// If either regresses, these tests fail.

// countUsesVarEdges returns the number of USES_VAR rows in the edges
// table for a project — the DB-truth count, independent of any
// in-memory summary.
func countUsesVarEdges(t *testing.T, idx *Indexer, projectID string) int {
	t.Helper()
	var n int
	if err := idx.store.DB().QueryRow(
		`SELECT COUNT(*) FROM edges WHERE project_id=? AND kind='USES_VAR'`,
		projectID,
	).Scan(&n); err != nil {
		t.Fatalf("count USES_VAR edges: %v", err)
	}
	return n
}

func writeAnsibleFixture(t *testing.T, dir string) {
	t.Helper()
	writeFile(t, dir, "group_vars/all.yml", "db_host: postgres.internal\napp_port: 8080\n")
	writeFile(t, dir, "roles/app/tasks/main.yml", `---
- name: configure
  shell: psql -h {{ db_host }} -p {{ app_port }} -c "SELECT 1"
`)
	writeFile(t, dir, "roles/app/templates/config.j2",
		"DATABASE_URL=postgres://{{ db_host }}:{{ app_port }}/mydb\n")
}

func TestIndex_AnsibleUsesVar_SurvivesNoChangeReindex_1479(t *testing.T) {
	idx, _ := newTestIndexer(t)
	dir := t.TempDir()
	writeAnsibleFixture(t, dir)

	res, err := idx.Index(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("pass 1 Index: %v", err)
	}
	projectID := res.ProjectID

	after1 := countUsesVarEdges(t, idx, projectID)
	if after1 == 0 {
		t.Fatalf("pass 1 produced 0 USES_VAR edges — extractor/resolver pipeline broken before the multi-pass scenario even applies")
	}
	t.Logf("#1479 pass 1: %d USES_VAR edges in DB", after1)

	// Pass 2 — no file changes. A true no-change pass must not touch
	// the resolved USES_VAR edges. This is the core #1479 scenario:
	// the watcher pass that found nothing to extract must not wipe
	// what the prior pass resolved.
	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("pass 2 Index: %v", err)
	}
	after2 := countUsesVarEdges(t, idx, projectID)
	if after2 != after1 {
		t.Errorf("USES_VAR edge count changed across a no-change re-index: pass1=%d pass2=%d "+
			"— a re-index with nothing to extract wiped resolved USES_VAR edges (regression of #1479)",
			after1, after2)
	}
}

func TestIndex_AnsibleUsesVar_SurvivesUnrelatedFileEdit_1479(t *testing.T) {
	idx, _ := newTestIndexer(t)
	dir := t.TempDir()
	writeAnsibleFixture(t, dir)
	// An unrelated file with no var references — editing it triggers
	// a re-index pass with totalFiles>0 but touches nothing that
	// emits USES_VAR. The resolve block runs; the USES_VAR edges from
	// the untouched Ansible files must still survive.
	writeFile(t, dir, "roles/app/files/notes.txt", "initial\n")

	res, err := idx.Index(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("pass 1 Index: %v", err)
	}
	projectID := res.ProjectID
	after1 := countUsesVarEdges(t, idx, projectID)
	if after1 == 0 {
		t.Fatalf("pass 1 produced 0 USES_VAR edges")
	}

	// Edit the unrelated file and bump its mtime so the hash-skip
	// doesn't short-circuit it — this forces a resolve pass with a
	// changed file that has no USES_VAR.
	time.Sleep(20 * time.Millisecond)
	notesPath := filepath.Join(dir, "roles/app/files/notes.txt")
	if err := os.WriteFile(notesPath, []byte("edited\n"), 0o644); err != nil {
		t.Fatalf("rewrite notes.txt: %v", err)
	}
	future := time.Now().Add(10 * time.Second)
	if err := os.Chtimes(notesPath, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("pass 2 Index: %v", err)
	}
	after2 := countUsesVarEdges(t, idx, projectID)
	if after2 != after1 {
		t.Errorf("USES_VAR edge count changed after an unrelated-file edit: pass1=%d pass2=%d "+
			"— resolving a changed non-Ansible file wiped USES_VAR edges from untouched files (regression of #1479)",
			after1, after2)
	}
}

// (Force-reindex persistence is covered by
// TestIndex_AnsibleUsesVar_PersistsAcrossForceReindex_1479 in
// uses_var_persistence_test.go — not duplicated here. The two tests
// above cover the angles that one doesn't: the true no-change
// incremental pass and the unrelated-file-edit pass, both of which
// route through different indexer paths than a force-reindex.)
