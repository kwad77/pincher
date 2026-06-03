// SPDX-License-Identifier: MIT

package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// runRebuildFTSCLI implements `pincher rebuild-fts [--data-dir DIR] [--yes]`.
//
// Drops the FTS5 vtab + sync triggers and recreates them from the symbols
// table. The escape hatch for FTS5 corruption — situations where the
// trigger-driven index has drifted from `symbols`, where the user's search
// results are missing rows that exist in the graph, or where a future
// version-shift on the FTS5 module produces incompatible shadow tables.
//
// Also the safety net for the upcoming per-corpus FTS5 split (#32). If
// the migration produces a degraded index, users can recover without
// re-indexing source files.
//
// Quiet by default — the operation modifies a single SQLite database
// the user already owns, so a confirmation prompt would be friction
// without value. Bash callers can pipe `yes` or use --yes to silence
// even the summary banner.
func runRebuildFTSCLI(args []string) {
	log.SetOutput(io.Discard)

	fs := flag.NewFlagSet("rebuild-fts", flag.ExitOnError)
	dataDir := fs.String("data-dir", "", "Override data directory")
	quiet := fs.Bool("quiet", false, "Suppress the human-readable banner; print only the row count on success")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: pincher rebuild-fts [--data-dir DIR] [--quiet]")
		fmt.Fprintln(os.Stderr, "  Drops and rebuilds the FTS5 search index from the canonical symbols table.")
		fmt.Fprintln(os.Stderr, "  Use this if the search MCP/HTTP tool returns results inconsistent with query.")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	dir := *dataDir
	if dir == "" {
		var err error
		dir, err = db.DataDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "pincher: failed to determine data directory: %v\n", err)
			os.Exit(1)
		}
	}

	store, err := db.Open(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pincher: failed to open database: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	start := time.Now()
	rows, err := store.RebuildFTS()
	elapsed := time.Since(start)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pincher: rebuild failed: %v\n", err)
		os.Exit(1)
	}

	if *quiet {
		fmt.Println(rows)
		return
	}
	fmt.Printf("Rebuilt FTS indexes: %d rows in %s\n", rows, elapsed.Round(time.Millisecond))
}
