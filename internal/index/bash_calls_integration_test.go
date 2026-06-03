// SPDX-License-Identifier: MIT

package index

import (
	"context"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// #1482 — Bash CALLS edges are emitted by the extractor (per-PR-1359 unit
// tests TestExtractBash_FunctionToFunctionCall_EmitsCALLS_1341 pass)
// but get dropped at the indexer's per-file resolution stage. This
// integration test exercises the full pipeline (extract → resolve →
// persist) to lock down whether Bash CALLS reach the DB.

const bashCallsCorpus = `#!/usr/bin/env bash
foo() {
  echo "hi"
}
bar() {
  foo
  foo arg
}
baz() {
  bar
}
`

// Modeled on pincher-repo/scripts/release-channel_test.sh — a real
// dogfood-shape Bash script: top-level assert() function called many
// times from top-level statements. The #1482 report names this exact
// shape ("test scripts heavily use assert-style helper functions
// called many times") as producing zero Bash CALLS edges in production.
const bashRealisticTestScript = `#!/usr/bin/env bash
set -euo pipefail

PASS=0
FAIL=0

assert() {
  local tag="$1"
  local want="$2"
  local got
  got="some_command $tag"
  if [[ "$got" == "$want" ]]; then
    PASS=$((PASS + 1))
  else
    FAIL=$((FAIL + 1))
  fi
}

assert "case1" "expected1"
assert "case2" "expected2"
assert "case3" "expected3"
`

func TestIndex_BashIntraFileCalls_PersistedToDB_1482(t *testing.T) {
	// Positive shape. baz calls bar; bar calls foo. The extractor
	// emits these as CALLS edges. The indexer's per-file nameToID
	// map should resolve both ends (both FromQN and ToName look up
	// against the QualifiedName/Name keys), and the edges should
	// land in the DB.
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "scripts/helpers.sh", bashCallsCorpus)

	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("Index: %v", err)
	}

	projectID := db.ProjectIDFromPath(dir)

	// Find the symbols.
	syms, err := store.GetSymbolsByQN(projectID, "helpers.foo")
	if err != nil || len(syms) == 0 {
		t.Fatalf("expected helpers.foo Function symbol; got %d syms err=%v", len(syms), err)
	}
	fooID := syms[0].ID

	// Verify a CALLS edge lands targeting foo (from bar).
	edges, err := store.EdgesTo(fooID, []string{"CALLS"})
	if err != nil {
		t.Fatalf("EdgesTo(fooID, CALLS): %v", err)
	}
	if len(edges) == 0 {
		t.Errorf("expected at least one CALLS edge into helpers.foo (bar calls foo); got 0 edges. #1482 bug: bash-source CALLS dropped at indexer's per-file nameToID resolution stage.")
	}
}

// Cross-check shape. The #1482 report named test scripts
// (release-channel_test.sh etc.) as the production-failure case. That
// pattern: a top-level helper function (assert) called many times from
// top-level statements. This corpus reproduces that exact shape.
func TestIndex_BashTopLevelCallsToLocalFunc_PersistedToDB_1482(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "scripts/probe_test.sh", bashRealisticTestScript)

	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("Index: %v", err)
	}

	projectID := db.ProjectIDFromPath(dir)
	syms, err := store.GetSymbolsByQN(projectID, "probe_test.assert")
	if err != nil || len(syms) == 0 {
		t.Fatalf("expected probe_test.assert Function symbol; got %d syms err=%v", len(syms), err)
	}
	assertID := syms[0].ID

	edges, err := store.EdgesTo(assertID, []string{"CALLS"})
	if err != nil {
		t.Fatalf("EdgesTo: %v", err)
	}
	if len(edges) == 0 {
		t.Errorf("expected ≥1 CALLS edge into probe_test.assert (3 top-level call sites); got 0. This is the production-failure shape #1482 names.")
	}
}
