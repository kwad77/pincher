// SPDX-License-Identifier: MIT

package index

import "github.com/kwad77/pincher/internal/db"

// shouldSuppressBinaryDriftForce decides whether a binary-version drift
// detected at Index() start should NOT trigger a full force-reindex of
// the project (#1497 v0.83).
//
// Gate: suppress when migrations ran THIS startup AND their union is
// `invalidatesNothing`. Rationale: if every schema migration applied
// during this Open() was a DDL-only entry that didn't touch extractor
// surface, then the binary change is bounded — whatever the new binary
// brought in beside those migrations cannot have moved extraction in a
// way that requires a full re-extract. The 5 currently-classified
// "All" migrations (v18→v19, v19→v20, v22→v23, v24→v25, v32→v33) flip
// this branch to "don't suppress" automatically because their inv.All
// is true.
//
// Counter-case (KEEP binaryDriftForce true): no migrations ran this
// startup (migFrom == migTo). The DB was already at the current
// schema. The binary still changed though — and the schema-migration
// classification can't tell us why, so we conservatively assume the
// binary brought extractor changes that need a refresh. This is the
// existing behaviour; the gate doesn't relax it.
//
// Language-scoped (Languages set but not All): keep binaryDriftForce
// true for now. The selective per-language file_hash clearing is a
// follow-up workstream — for v0.83 we ship the all-or-nothing gate.
//
// Pure function — exported test in binary_drift_gate_test.go pins the
// truth table without needing a real Store or compile-time migration
// injection. The integration test for the suppress branch lives in
// binary_drift_atomic_test.go where the existing drift harness lives.
func shouldSuppressBinaryDriftForce(inv db.MigrationInvalidates, migFrom, migTo int) bool {
	if migFrom == migTo {
		return false
	}
	if inv.All {
		return false
	}
	if len(inv.Languages) > 0 {
		return false
	}
	return true
}
