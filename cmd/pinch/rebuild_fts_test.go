// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// pincherBinaryName returns the platform-appropriate name for a pincher
// binary built into a temp dir. On Windows `go build -o foo` writes
// `foo.exe`; the exec call must use the actual filename or it will fail
// with "executable not found" even though the file exists right there.
func pincherBinaryName() string {
	if runtime.GOOS == "windows" {
		return "pincher.exe"
	}
	return "pincher"
}

// TestRebuildFTSCLI_Binary runs the actual `pincher rebuild-fts` binary
// against a fresh DB so the subcommand wiring (dispatch, flags, output
// format) is exercised end-to-end. The store-level rebuild semantics are
// covered by TestRebuildFTS_* in internal/db/db_test.go — this test
// guards the CLI contract: arg parsing, exit code, banner format.
func TestRebuildFTSCLI_Binary(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping CLI binary build in -short mode")
	}

	bin := buildPincherBinary(t)

	// Seed a DB with some symbols so the rebuild has something to count.
	dataDir := t.TempDir()
	store, err := db.Open(dataDir)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	store.UpsertProject(db.Project{ID: "p1", Path: "/p", Name: "demo"})
	store.BulkUpsertSymbols([]db.Symbol{
		{ID: "s1", ProjectID: "p1", FilePath: "a.go", Name: "A", QualifiedName: "a.A", Kind: "Function", Language: "Go"},
		{ID: "s2", ProjectID: "p1", FilePath: "a.go", Name: "B", QualifiedName: "a.B", Kind: "Function", Language: "Go"},
	})
	store.Close()

	// Default output: human-readable banner with row count.
	cmd := exec.Command(bin, "rebuild-fts", "--data-dir", dataDir)
	cmd.Env = pincherCoverEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rebuild-fts: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "Rebuilt FTS indexes: 2 rows") {
		t.Errorf("expected '2 rows' banner, got:\n%s", out)
	}

	// --quiet: row count only, no banner.
	cmd = exec.Command(bin, "rebuild-fts", "--data-dir", dataDir, "--quiet")
	cmd.Env = pincherCoverEnv()
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rebuild-fts --quiet: %v\n%s", err, out)
	}
	got := strings.TrimSpace(string(out))
	if got != "2" {
		t.Errorf("--quiet output = %q, want %q", got, "2")
	}
}

// TestRebuildFTSCLI_BadDataDir asserts that a corrupt / non-existent data
// directory produces a clean error exit, not a panic. We pass a regular
// FILE (not a directory) as the data-dir; `db.Open` will then try to open
// `<file>/pincher.db` which fails identically on every platform — no
// platform-specific magic paths needed (the original /proc/1 hack was
// Linux-only and broke Windows CI).
func TestRebuildFTSCLI_BadDataDir(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping CLI binary build in -short mode")
	}

	bin := buildPincherBinary(t)

	// Create a regular file and use it as --data-dir — Open will try to
	// join it with "pincher.db" and the resulting path is not a valid
	// SQLite database location on any platform.
	notADir := filepath.Join(t.TempDir(), "not_a_dir")
	if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
		t.Fatalf("write notADir: %v", err)
	}

	cmd := exec.Command(bin, "rebuild-fts", "--data-dir", notADir)
	cmd.Env = pincherCoverEnv()
	out, _ := cmd.CombinedOutput()
	// We don't assert exit code (subprocess behavior varies); just that
	// we got a recognisable failure message, not a panic.
	if !strings.Contains(string(out), "failed") {
		t.Errorf("expected 'failed' in stderr, got:\n%s", out)
	}
	if strings.Contains(string(out), "panic:") {
		t.Errorf("rebuild-fts panicked on bad data dir:\n%s", out)
	}
}

func TestRebuildFTSCLI_HelpDoesNotReferenceRemovedSearchCLI(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping CLI binary build in -short mode")
	}

	bin := buildPincherBinary(t)
	cmd := exec.Command(bin, "rebuild-fts", "--help")
	cmd.Env = pincherCoverEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rebuild-fts --help: %v\n%s", err, out)
	}
	got := string(out)
	if strings.Contains(got, "pincher search") || strings.Contains(got, "pincher query") {
		t.Fatalf("help references removed CLI commands:\n%s", got)
	}
	if !strings.Contains(got, "search MCP/HTTP tool") {
		t.Fatalf("help should point to search MCP/HTTP surface:\n%s", got)
	}
}
