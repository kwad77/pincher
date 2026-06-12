// SPDX-License-Identifier: MIT

package index

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// Regression guard for #2043. The #2040 staleness guard recomputed the file
// hash by reading the WHOLE file on every read where sym.FileHash != "" —
// which the indexer stamps on every symbol. On the capped search-snippet hot
// path (maxBytes > 0) that bypassed the cap entirely: a 200KB file with a
// 60-byte symbol and 240-byte cap read all 200KB (measured 26998 ns/op,
// 213929 B/op vs 3314 ns/op, 552 B/op for the bounded path).
//
// The fix gates the whole-file hash read behind maxBytes <= 0, so capped
// callers get the bounded size-guard-only seek again while unbounded
// symbol/context reads keep the full hash guard.

const stalePerfFileSize = 200 * 1024 // 200KB, matching the issue's benchmark.

// makeLargeFile writes a stalePerfFileSize file and returns a Symbol whose
// span is a small (60-byte) slice near the top, with FileHash stamped (the
// common indexer case).
func makeLargeFile(tb testing.TB) (dir string, sym db.Symbol) {
	tb.Helper()
	dir = tb.TempDir()
	content := strings.Repeat("x", stalePerfFileSize)
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(content), 0o644); err != nil {
		tb.Fatalf("write big file: %v", err)
	}
	return dir, db.Symbol{
		FilePath:  "big.txt",
		StartByte: 100,
		EndByte:   160, // 60-byte symbol span
		FileHash:  hashOf(content),
	}
}

// BenchmarkReadSymbolSourceCapped_UnchangedLargeFile is the proof for #2043.
// An unchanged 200KB file read with a 240-byte cap must allocate ~bounded
// bytes — NOT the whole file. Before the fix this reported ~213929 B/op
// (whole-file read); after, allocations are back near the pre-#2040 bounded
// path (a few hundred B/op), independent of file size.
func BenchmarkReadSymbolSourceCapped_UnchangedLargeFile(b *testing.B) {
	dir, sym := makeLargeFile(b)
	const cap = 240
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := ReadSymbolSourceCapped(dir, sym, cap); err != nil {
			b.Fatalf("capped read failed: %v", err)
		}
	}
}

// TestReadSymbolSourceCapped_UnchangedLargeFile_BoundedAllocs asserts the
// regression cannot silently return: a capped read of a 200KB file must
// allocate far less than the file size (proving no whole-file read). The
// threshold is generous (8KB) to stay robust across runtimes while still
// catching a 200KB+ whole-file allocation.
func TestReadSymbolSourceCapped_UnchangedLargeFile_BoundedAllocs(t *testing.T) {
	dir, sym := makeLargeFile(t)
	const cap = 240

	// Warm up once (open path, etc.) so AllocsPerRun measures steady state.
	if _, err := ReadSymbolSourceCapped(dir, sym, cap); err != nil {
		t.Fatalf("capped read failed: %v", err)
	}

	bytesPerOp := testingAllocBytes(t, func() {
		if _, err := ReadSymbolSourceCapped(dir, sym, cap); err != nil {
			t.Fatalf("capped read failed: %v", err)
		}
	})
	const ceiling = 8 * 1024
	if bytesPerOp > ceiling {
		t.Fatalf("#2043 regression: capped read allocated %d B/op on a %d-byte file (ceiling %d B/op) — whole-file read is back",
			bytesPerOp, stalePerfFileSize, ceiling)
	}
	t.Logf("capped read of %d-byte file allocated ~%d B/op (well under whole-file read)", stalePerfFileSize, bytesPerOp)
}

// TestReadSymbolSource_SameLengthEdit_UnboundedStillStale proves the fix did
// NOT weaken the authoritative unbounded path: a same-length edit (which the
// size guard alone cannot catch) still surfaces as ErrStaleByteOffset on the
// unbounded symbol/context read path.
func TestReadSymbolSource_SameLengthEdit_UnboundedStillStale(t *testing.T) {
	dir := t.TempDir()
	original := strings.Repeat("a", 4096)
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte(original), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	sym := db.Symbol{
		FilePath:  "f.txt",
		StartByte: 10,
		EndByte:   70,
		FileHash:  hashOf(original),
	}

	// Same-length edit: flip a byte well outside [StartByte,EndByte) so the
	// span bytes are identical but the file hash differs. Size guard can't
	// see this; the hash guard must.
	edited := []byte(original)
	edited[2000] = 'b'
	if len(edited) != len(original) {
		t.Fatalf("edit changed length: %d != %d", len(edited), len(original))
	}
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), edited, 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	// Unbounded (symbol/context) path: full hash guard — must error.
	if _, err := ReadSymbolSource(dir, sym); !errors.Is(err, ErrStaleByteOffset) {
		t.Fatalf("unbounded read of same-length-edited file: expected ErrStaleByteOffset, got %v", err)
	}
}

// TestReadSymbolSourceCapped_ShrunkFile_StillStale confirms the cheap size
// guard remains active on the capped path: a shrink past EndByte still errors
// even though the (expensive) hash guard is skipped for capped reads.
func TestReadSymbolSourceCapped_ShrunkFile_StillStale(t *testing.T) {
	dir := t.TempDir()
	original := strings.Repeat("a", 4096)
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte(original), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	sym := db.Symbol{
		FilePath:  "f.txt",
		StartByte: 10,
		EndByte:   4000,
		FileHash:  hashOf(original),
	}
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("short\n"), 0o644); err != nil {
		t.Fatalf("shrink: %v", err)
	}
	if _, err := ReadSymbolSourceCapped(dir, sym, 240); !errors.Is(err, ErrStaleByteOffset) {
		t.Fatalf("capped read of shrunk file: expected ErrStaleByteOffset (size guard), got %v", err)
	}
}

// testingAllocBytes returns the average bytes allocated per call of f using
// testing.AllocsPerRun-style measurement against memstats, without pulling in
// runtime/metrics. It runs f a fixed number of times and reports the mean
// heap bytes allocated per call.
func testingAllocBytes(t *testing.T, f func()) uint64 {
	t.Helper()
	return measureAllocBytes(f, 50)
}

// measureAllocBytes runs f n times and returns the mean heap bytes allocated
// per call (TotalAlloc delta / n). GC is disabled around the loop so the
// delta reflects allocation, not concurrent collection.
func measureAllocBytes(f func(), n int) uint64 {
	old := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(old)
	runtime.GC()

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for i := 0; i < n; i++ {
		f()
	}
	runtime.ReadMemStats(&after)
	return (after.TotalAlloc - before.TotalAlloc) / uint64(n)
}
