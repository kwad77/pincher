// SPDX-License-Identifier: MIT

package db

import (
	"testing"
)

// #1149: when another connection holds an open reader snapshot at
// VACUUM time, wal_checkpoint(TRUNCATE) returns busy=1 and freelist
// pages stay pinned — VACUUM reclaims 0 B silently. The probing
// checkpoint result feeds VacuumResult so the CLI can render a
// targeted advisory instead of the misleading "0 B reclaimed" line.
//
// Negative: no open reader → WalReaderBusy stays false. This is the
// steady-state CLI path (a standalone `pincher vacuum` run with no
// MCP server attached); WalReaderBusy must NOT surface an advisory
// in this case.
func TestVacuum_NoReader_WalReaderBusyFalse(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	if err := s.UpsertProject(testProject("vac-clean")); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}

	res, err := s.Vacuum()
	if err != nil {
		t.Fatalf("Vacuum: %v", err)
	}
	if res.WalReaderBusy {
		t.Errorf("VacuumResult.WalReaderBusy = true with no open reader; expected false (this would surface a spurious advisory in the steady-state CLI path)")
	}
}

// Cross-check: the VacuumResult struct is the contract between
// Store.Vacuum and the CLI advisory renderer. Assert the field
// exists and has the documented bool type so a future signature
// drift breaks loudly. The runVacuumCLI handler in cmd/pinch
// consumes this struct directly.
func TestVacuumResult_BusyFieldShape(t *testing.T) {
	var r VacuumResult
	r.WalReaderBusy = true
	if !r.WalReaderBusy {
		t.Errorf("VacuumResult.WalReaderBusy didn't store true")
	}
}

// Cross-check: Vacuum still reclaims pages in the no-reader case
// (the #732 happy path). The #1149 advisory plumbing must not
// regress the core "VACUUM shrinks the file" guarantee. Mirrors
// TestVacuum_ReclaimsAfterDelete but verifies WalReaderBusy=false
// in the same run — a single assertion bundle keeps the cases
// from drifting apart on a refactor.
func TestVacuum_HappyPath_ReclaimsAndReportsNoBusy(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	if err := s.UpsertProject(testProject("vac-happy")); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	syms := make([]Symbol, 0, 500)
	for i := 0; i < 500; i++ {
		id := makeSymID(t, "vac-happy", "internal/x/x.go", "sym"+itoa(i), "Function")
		syms = append(syms, testSymbol(id, "sym"+itoa(i), "Function", "vac-happy", "internal/x/x.go"))
	}
	if err := s.BulkUpsertSymbols(syms); err != nil {
		t.Fatalf("BulkUpsertSymbols: %v", err)
	}
	if err := s.CheckpointTruncate(); err != nil {
		t.Fatalf("CheckpointTruncate: %v", err)
	}
	grown := dbFileSizeForTest(t, s.Path)

	if err := s.DeleteProject("vac-happy"); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	res, err := s.Vacuum()
	if err != nil {
		t.Fatalf("Vacuum: %v", err)
	}
	if res.WalReaderBusy {
		t.Errorf("happy-path Vacuum reported WalReaderBusy=true; expected false")
	}
	shrunk := dbFileSizeForTest(t, s.Path)
	if shrunk >= grown {
		t.Errorf("Vacuum did not reclaim space (grown=%d, shrunk=%d) — advisory plumbing regressed the core #732 guarantee", grown, shrunk)
	}
}

// itoa is a tiny strconv.Itoa stand-in to avoid the import pull just
// for this test file's symbol-naming loop.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// makeSymID returns the canonical symbol ID format used by tests in
// this package — same shape db.MakeSymbolID produces in production.
func makeSymID(t *testing.T, project, file, name, kind string) string {
	t.Helper()
	return file + "::" + project + "." + name + "#" + kind
}

// Coverage of the BUSY-reader positive case is exercised end-to-end
// by the #1149 issue repro (a running MCP child holds an open WAL
// reader, `pincher vacuum` reports the advisory in stdout). The unit
// test for that case deadlocks under WAL-mode VACUUM because a
// second SQL connection holding a snapshot blocks the rewrite — the
// real-world case works because the reader is in a different process
// with its own connection pool, and VACUUM falls back to the
// checkpoint-busy signal rather than waiting on the lock.
//
// The negative + happy-path tests above pin the steady-state CLI
// behaviour; the BUSY-reader path is well-covered by the issue's
// concrete repro and the runtime advisory string is small enough
// to grep-validate in dogfood smoke.
