package server

import (
	"strings"
	"testing"

	"github.com/kwad77/pincher/internal/index"
)

// #425: distinguish the three zero-symbol cases at diagnosis time so an
// incremental re-index of symbol-neutral edits doesn't fingerprint as
// "extractor missing".

func TestDiagnoseEmptyIndex_IncrementalSymbolNeutralReindex(t *testing.T) {
	t.Parallel()
	// skipped > 0 AND files > 0 AND symbols == 0 — body-only edits, the
	// benign case #425 split out.
	meta := diagnoseEmptyIndex(&index.IndexResult{
		Files:   3,
		Skipped: 247,
		Symbols: 0,
	}, false)
	if meta == nil {
		t.Fatal("expected diagnosis for symbol-neutral incremental re-index, got nil")
	}
	diag, _ := meta["diagnosis"].(string)
	if !strings.Contains(diag, "incremental re-index") {
		t.Errorf("diagnosis = %q, want 'incremental re-index' prefix", diag)
	}
	if !strings.Contains(diag, "3 reprocessed") || !strings.Contains(diag, "247 files unchanged") {
		t.Errorf("diagnosis should report both counts, got: %q", diag)
	}
	hint, _ := meta["hint"].(string)
	if !strings.Contains(hint, "symbol-neutral") {
		t.Errorf("hint should explain symbol-neutral edits, got: %q", hint)
	}
}

func TestDiagnoseEmptyIndex_ExtractorMissing(t *testing.T) {
	t.Parallel()
	// skipped == 0 AND files > 0 AND symbols == 0 — the genuine bug case.
	meta := diagnoseEmptyIndex(&index.IndexResult{
		Files:   5,
		Skipped: 0,
		Symbols: 0,
	}, false)
	if meta == nil {
		t.Fatal("expected diagnosis for extractor-missing case, got nil")
	}
	diag, _ := meta["diagnosis"].(string)
	if !strings.Contains(diag, "no symbols extracted") {
		t.Errorf("diagnosis = %q, want extractor-missing diagnosis", diag)
	}
	hint, _ := meta["hint"].(string)
	if !strings.Contains(hint, "language detection") {
		t.Errorf("hint should point at language detection, got: %q", hint)
	}
}

func TestDiagnoseEmptyIndex_AllUnchanged(t *testing.T) {
	t.Parallel()
	// files == 0 AND skipped > 0 — the all-cached fast path.
	meta := diagnoseEmptyIndex(&index.IndexResult{
		Files:   0,
		Skipped: 250,
		Symbols: 0,
	}, false)
	if meta == nil {
		t.Fatal("expected diagnosis for fully-cached run, got nil")
	}
	diag, _ := meta["diagnosis"].(string)
	if !strings.Contains(diag, "all 250 files unchanged") {
		t.Errorf("diagnosis = %q, want 'all 250 files unchanged'", diag)
	}
}

func TestDiagnoseEmptyIndex_NoIndexableFiles(t *testing.T) {
	t.Parallel()
	meta := diagnoseEmptyIndex(&index.IndexResult{}, false)
	if meta == nil {
		t.Fatal("expected diagnosis for empty path, got nil")
	}
	diag, _ := meta["diagnosis"].(string)
	if !strings.Contains(diag, "no indexable source files") {
		t.Errorf("diagnosis = %q, want 'no indexable source files'", diag)
	}
	hint, _ := meta["hint"].(string)
	if strings.Contains(hint, "pincher health") || !strings.Contains(hint, "MCP/HTTP `health` tool") {
		t.Errorf("hint should reference MCP/HTTP health, not removed CLI command; got %q", hint)
	}
}

func TestDiagnoseEmptyIndex_AllBlocked(t *testing.T) {
	t.Parallel()
	meta := diagnoseEmptyIndex(&index.IndexResult{
		Files:   0,
		Blocked: 12,
		Symbols: 0,
	}, false)
	if meta == nil {
		t.Fatal("expected diagnosis for all-blocked run, got nil")
	}
	diag, _ := meta["diagnosis"].(string)
	if !strings.Contains(diag, "all 12 files were blocked") {
		t.Errorf("diagnosis = %q, want 'all 12 files were blocked'", diag)
	}
}

// #1773: a healthy fully-indexed project's no-change incremental
// re-index hash-skips its source files (Skipped>0) AND blocks its
// lockfiles / minified bundles (Blocked>0). That must diagnose as
// incremental_no_change — NOT all_files_blocked, which would tell the
// agent a project with thousands of indexed symbols is a vendor-only
// directory with no sources. The all_files_blocked branch now requires
// Skipped==0 (blocked must be the ONLY reason nothing processed).
func TestDiagnoseEmptyIndex_BlockedPlusSkippedIsIncrementalNoChange_1773(t *testing.T) {
	t.Parallel()
	meta := diagnoseEmptyIndex(&index.IndexResult{
		Files:   0,
		Blocked: 71,
		Skipped: 874,
		Symbols: 0,
	}, false)
	if meta == nil {
		t.Fatal("expected diagnosis, got nil")
	}
	if meta["empty_reason"] != EmptyReasonIncrementalNoChange {
		t.Errorf("empty_reason = %v, want %s — blocked lockfiles must not mask a healthy hash-skip pass",
			meta["empty_reason"], EmptyReasonIncrementalNoChange)
	}
	diag, _ := meta["diagnosis"].(string)
	if !strings.Contains(diag, "all 874 files unchanged") {
		t.Errorf("diagnosis = %q, want 'all 874 files unchanged'", diag)
	}
}

func TestDiagnoseEmptyIndex_NonZeroSymbolsSilent(t *testing.T) {
	t.Parallel()
	// Healthy run — no diagnosis surfaced.
	if meta := diagnoseEmptyIndex(&index.IndexResult{
		Files:   5,
		Symbols: 100,
	}, false); meta != nil {
		t.Errorf("non-zero symbol result should not surface diagnosis, got: %v", meta)
	}
}

func TestDiagnoseEmptyIndex_ForceSkipsAllUnchangedBranch(t *testing.T) {
	t.Parallel()
	// With force=true and only skipped files, the "all unchanged" branch
	// shouldn't fire — that would be misleading because force was meant
	// to re-extract. Falls through to the extractor-missing default.
	meta := diagnoseEmptyIndex(&index.IndexResult{
		Files:   0,
		Skipped: 100,
		Symbols: 0,
	}, true)
	if meta == nil {
		t.Fatal("expected diagnosis under force=true, got nil")
	}
	diag, _ := meta["diagnosis"].(string)
	if strings.Contains(diag, "all 100 files unchanged") {
		t.Errorf("force=true should not produce 'all unchanged' diagnosis, got: %q", diag)
	}
	hint, _ := meta["hint"].(string)
	if strings.Contains(hint, "pincher health") || !strings.Contains(hint, "per-language coverage") {
		t.Errorf("hint should reference health tool coverage, not removed CLI command; got %q", hint)
	}
}
