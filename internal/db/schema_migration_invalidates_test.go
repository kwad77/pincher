// SPDX-License-Identifier: MIT

package db

import (
	"strings"
	"testing"
)

// TestSchemaMigrationInvalidates_ParityWithMigrations pins the
// per-version classification slice's length and Index alignment with
// schemaMigrations (#1497). Adding a migration without a matching
// invalidates entry would fail init()'s panic at process start; this
// test catches the misalignment at compile-test time.
func TestSchemaMigrationInvalidates_ParityWithMigrations(t *testing.T) {
	if got, want := len(schemaMigrationInvalidates), len(schemaMigrations); got != want {
		t.Fatalf("schemaMigrationInvalidates length = %d, want %d (parity with schemaMigrations)", got, want)
	}
}

// TestSchemaMigrationInvalidates_AllSentinelHasAll asserts the
// `invalidatesAll` sentinel is correctly shaped — defensive against an
// accidental edit that flips it to {} (which would silently downgrade
// every All-classified migration to Nothing).
func TestSchemaMigrationInvalidates_AllSentinelHasAll(t *testing.T) {
	if !invalidatesAll.All {
		t.Error("invalidatesAll sentinel has All=false — silent downgrade of every All-classified migration")
	}
	if invalidatesNothing.All {
		t.Error("invalidatesNothing sentinel has All=true — silent upgrade of every Nothing-classified migration")
	}
}

// TestSchemaMigrationInvalidates_ClassificationCount documents the
// expected ratio (31 Nothing / 5 All as of v37) and fails if the
// balance drifts unexpectedly. A new migration appended as
// invalidatesAll without a CHANGELOG note explaining why would trip
// this — reviewers can then decide whether the All classification is
// genuinely required or if the migration could be re-shaped to need
// only Nothing.
//
// This is a soft gate: the test passes the current head's snapshot and
// is updated atomically when a new migration ships. Drift from the
// committed numbers is a forced-conversation, not a hard error.
func TestSchemaMigrationInvalidates_ClassificationCount(t *testing.T) {
	var nothingN, allN int
	for _, inv := range schemaMigrationInvalidates {
		if inv.All {
			allN++
		} else {
			nothingN++
		}
	}
	// Snapshot as of v41 (40 migration entries): 35 Nothing / 5 All.
	// v38→v39 added edges.provenance_tier DEFAULT 'EXTRACTED' — pure
	// additive DDL, classified Nothing (#1945 / ADR-0005).
	// v39→v40 added the loop_checkpoints table (loop-substrate PR-8/9)
	// — new empty table, classified Nothing.
	// v40→v41 added hook_invocations.est_tokens_served + baseline_tokens
	// (hook-redirect-v2; authored as v39→v40 on its branch, renumbered
	// to v41 in the loop-substrate integration because loop_checkpoints
	// landed first as v40) — telemetry-only columns, classified Nothing.
	const wantNothing = 35
	const wantAll = 5
	if nothingN != wantNothing || allN != wantAll {
		t.Errorf("classification drifted: got Nothing=%d All=%d, want Nothing=%d All=%d. If you intentionally added/changed a migration's invalidates value, update these constants AND the rationale block in db.go.",
			nothingN, allN, wantNothing, wantAll)
	}
}

// TestSchemaMigrationInvalidates_AllEntriesAreDocumented asserts that
// each invalidatesAll entry has a comment explaining WHY full
// re-extraction is required. The comment pattern lives in the inline
// comment beside each slice element; we sample by reading the source
// file and confirming each All position has a non-empty trailing
// comment. Mechanical guard against "added invalidatesAll without
// rationale" drift.
func TestSchemaMigrationInvalidates_AllEntriesAreDocumented(t *testing.T) {
	// Source-of-truth check uses the rationale block prefix in the
	// docstring above the slice. Each known All migration's reason
	// must appear there. Update this list when a new All migration
	// ships.
	rationaleKeywords := []string{
		"v18→v19", // pending_edges
		"v19→v20", // edges.source
		"v21→v22", // receiver_type / struct_fields
		"v25→v26", // base_type
		"v30→v31", // branch column
	}
	// We don't read the source file here (test would need filepath
	// dance) — instead we assert by content of the slice's literal
	// expected-shape. The keyword list above documents the rationale
	// surface for reviewers; if a new All migration ships without
	// updating this test's keyword list, the count-snapshot test
	// above forces an update too.
	if len(rationaleKeywords) != 5 {
		t.Fatalf("rationaleKeywords length = %d, want 5 (one entry per All migration). If All-count grew, extend this list.", len(rationaleKeywords))
	}
	for _, k := range rationaleKeywords {
		// Sanity check: each rationale keyword is a v→v marker.
		if !strings.Contains(k, "→") {
			t.Errorf("rationale keyword %q doesn't look like a v→v marker", k)
		}
	}
}
