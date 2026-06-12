// SPDX-License-Identifier: MIT

package index

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/zeebo/xxh3"

	"github.com/kwad77/pincher/internal/db"
)

// Bug-hunt repro (HIGH-2): the byte-offset reader does Open→Seek→Read with
// no validation of the file vs the indexed state. When the file is edited
// after indexing, the stored StartByte/EndByte point at different content —
// the reader returns *some other symbol's* bytes with no error. When the
// file is shrunk below EndByte, the reader silently short-reads.
//
// Both are correctness violations: a stale read must surface as an explicit
// error, never as arbitrary (wrong) bytes.

func hashOf(content string) string {
	return fmt.Sprintf("%x", xxh3.Hash([]byte(content)))
}

// TestReadSymbolSource_StaleEdit_ReturnsError: a file edited after indexing
// must not return the bytes now occupying the stored offset range. The
// indexed FileHash no longer matches the on-disk content, so the read must
// error rather than ship a different symbol's body.
func TestReadSymbolSource_StaleEdit_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	original := "package main\n\nfunc Hello() string {\n\treturn \"hello\"\n}\n"
	writeFile(t, dir, "main.go", original)

	// Symbol spans the Hello function body in the ORIGINAL file.
	startByte := 14
	endByte := len(original)
	sym := db.Symbol{
		FilePath:  "main.go",
		StartByte: startByte,
		EndByte:   endByte,
		FileHash:  hashOf(original),
	}

	// Sanity: against the original file the read is correct.
	if got, err := ReadSymbolSource(dir, sym); err != nil || got != original[startByte:endByte] {
		t.Fatalf("baseline read failed: got=%q err=%v", got, err)
	}

	// Now edit the file. A prepended line shifts every byte; the stored
	// offsets point at unrelated content of the SAME total length (so a
	// size-only check wouldn't catch it — the hash must).
	edited := "// new top comment line padding\n" + "package main\n\nfunc Goodbye() string {\n\treturn \"bye\"\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(edited), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	got, err := ReadSymbolSource(dir, sym)
	if err == nil {
		t.Fatalf("expected staleness error on edited file, got source=%q (wrong bytes shipped silently)", got)
	}
	if !errors.Is(err, ErrStaleByteOffset) {
		t.Fatalf("expected ErrStaleByteOffset, got %v", err)
	}
	if got != "" {
		t.Fatalf("expected empty source on stale read, got %q", got)
	}
}

// TestReadSymbolSource_ShrunkFile_ErrorsNotSilentTruncation: when the file
// is shrunk so EndByte now lies past EOF, the reader must error instead of
// silently returning a short (truncated) read.
func TestReadSymbolSource_ShrunkFile_ErrorsNotSilentTruncation(t *testing.T) {
	dir := t.TempDir()
	original := "package main\n\nfunc Hello() string {\n\treturn \"a long body here\"\n}\n"
	writeFile(t, dir, "main.go", original)

	startByte := 14
	endByte := len(original)
	sym := db.Symbol{
		FilePath:  "main.go",
		StartByte: startByte,
		EndByte:   endByte,
		FileHash:  hashOf(original),
	}

	// Shrink the file below EndByte. EndByte now exceeds the file size.
	shrunk := "package main\n"
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(shrunk), 0o644); err != nil {
		t.Fatalf("shrink: %v", err)
	}

	got, err := ReadSymbolSource(dir, sym)
	if err == nil {
		t.Fatalf("expected error on shrunk file (EndByte past EOF), got source=%q (silent short read)", got)
	}
	if !errors.Is(err, ErrStaleByteOffset) {
		t.Fatalf("expected ErrStaleByteOffset, got %v", err)
	}
}

// TestReadSymbolSource_NoHash_SizeGuardStillCatchesShrink: a symbol with no
// stored FileHash (Document/URL kinds, pre-#236 rows) still gets the cheap
// size guard — a read whose EndByte exceeds the current file size errors
// rather than silently short-reading.
func TestReadSymbolSource_NoHash_SizeGuardStillCatchesShrink(t *testing.T) {
	dir := t.TempDir()
	original := "0123456789abcdefghijABCDEFGHIJ\n"
	writeFile(t, dir, "blob.txt", original)

	sym := db.Symbol{
		FilePath:  "blob.txt",
		StartByte: 5,
		EndByte:   len(original),
		// FileHash deliberately empty.
	}

	// Shrink so EndByte is now past EOF.
	if err := os.WriteFile(filepath.Join(dir, "blob.txt"), []byte("0123\n"), 0o644); err != nil {
		t.Fatalf("shrink: %v", err)
	}

	got, err := ReadSymbolSource(dir, sym)
	if err == nil {
		t.Fatalf("expected size-guard error on shrunk no-hash file, got source=%q", got)
	}
	if !errors.Is(err, ErrStaleByteOffset) {
		t.Fatalf("expected ErrStaleByteOffset, got %v", err)
	}
}

// TestReadSymbolSource_UnchangedFile_StillWorks: the validation must not
// break the happy path — an unedited file whose hash matches reads exactly
// as before.
func TestReadSymbolSource_UnchangedFile_StillWorks(t *testing.T) {
	dir := t.TempDir()
	content := "package main\n\nfunc Hello() string {\n\treturn \"hello\"\n}\n"
	writeFile(t, dir, "main.go", content)

	startByte := 14
	endByte := len(content)
	sym := db.Symbol{
		FilePath:  "main.go",
		StartByte: startByte,
		EndByte:   endByte,
		FileHash:  hashOf(content),
	}

	got, err := ReadSymbolSource(dir, sym)
	if err != nil {
		t.Fatalf("unexpected error on unchanged file: %v", err)
	}
	if got != content[startByte:endByte] {
		t.Fatalf("source mismatch: got=%q want=%q", got, content[startByte:endByte])
	}
}
