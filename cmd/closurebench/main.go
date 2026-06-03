// SPDX-License-Identifier: MIT

// closurebench measures the storage cost of materializing a transitive-closure
// table over the pincher edges graph for a given project + max depth (#639).
//
// The decision-blocking question: at depth=3 and depth=5 on a real corpus, how
// big does closure(from_id, to_id, depth, project_id) get? If a 10k-file repo
// lands above ~500 MB on closure storage, v0.54's #652 phase-1 implementation
// needs a smaller default depth or an on-demand strategy.
//
// Approach: connect to the existing pincher DB, materialize the closure into a
// FRESH side-database (so the size measurement is just-the-closure, no other
// rows polluting the file), stat the file, drop. No schema changes to the
// source DB; safe to run against a production index.
//
// Output is a markdown row suitable for pasting into the #639 results table.
//
// Usage:
//
//	closurebench [-db <path>] [-project <id>] [-depth 3,5] [-md]
//
// Defaults: db=$HOME/.pincher/pincher.db (Linux/macOS) or %USERPROFILE%/.pincher/pincher.db (Windows),
// project=first project found, depth=3,5.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

// run is main's testable body. Returns the process exit code; never calls
// os.Exit directly (so tests can call run() repeatedly without tearing
// down the test process). errOut receives any error message; stdout
// receives the result table via printSummary / printMarkdownRow.
func run(args []string, errOut io.Writer) int {
	fs := flag.NewFlagSet("closurebench", flag.ContinueOnError)
	fs.SetOutput(errOut)
	dbPath := fs.String("db", defaultDBPath(), "Path to pincher.db")
	projectID := fs.String("project", "", "Project ID to measure (default: first project in DB)")
	depths := fs.String("depth", "3,5", "Comma-separated max-depth values to measure")
	mdRow := fs.Bool("md", false, "Print one Markdown table row instead of multi-line summary")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	src, err := sql.Open("sqlite", "file:"+*dbPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		fmt.Fprintf(errOut, "closurebench: open source db: %v\n", err)
		return 1
	}
	defer src.Close()

	pid, err := resolveProject(src, *projectID)
	if err != nil {
		fmt.Fprintf(errOut, "closurebench: %v\n", err)
		return 1
	}

	srcSize, err := fileSize(*dbPath)
	if err != nil {
		fmt.Fprintf(errOut, "closurebench: stat source db: %v\n", err)
		return 1
	}

	files, edges, err := projectStats(src, pid)
	if err != nil {
		fmt.Fprintf(errOut, "closurebench: project stats: %v\n", err)
		return 1
	}

	results := make(map[int]closureMeasurement)
	for _, ds := range strings.Split(*depths, ",") {
		ds = strings.TrimSpace(ds)
		if ds == "" {
			continue
		}
		d, err := strconv.Atoi(ds)
		if err != nil || d < 1 {
			fmt.Fprintf(errOut, "closurebench: invalid depth %q (must be positive integer)\n", ds)
			return 1
		}
		m, err := measureClosure(src, pid, d)
		if err != nil {
			fmt.Fprintf(errOut, "closurebench: depth=%d: %v\n", d, err)
			return 1
		}
		results[d] = m
	}

	if *mdRow {
		printMarkdownRow(*dbPath, pid, files, edges, srcSize, results)
		return 0
	}
	printSummary(*dbPath, pid, files, edges, srcSize, results)
	return 0
}

func defaultDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "pincher.db"
	}
	return filepath.Join(home, ".pincher", "pincher.db")
}

func resolveProject(db *sql.DB, want string) (string, error) {
	if want != "" {
		var got string
		err := db.QueryRow("SELECT id FROM projects WHERE id = ?", want).Scan(&got)
		if err != nil {
			return "", fmt.Errorf("project %q not found in DB: %w", want, err)
		}
		return got, nil
	}
	var got string
	err := db.QueryRow("SELECT id FROM projects ORDER BY indexed_at DESC LIMIT 1").Scan(&got)
	if err != nil {
		return "", fmt.Errorf("no projects in DB (run `pincher index` first): %w", err)
	}
	return got, nil
}

func projectStats(db *sql.DB, pid string) (files, edges int64, err error) {
	if err = db.QueryRow("SELECT COUNT(DISTINCT file_path) FROM symbols WHERE project_id = ?", pid).Scan(&files); err != nil {
		return
	}
	err = db.QueryRow("SELECT COUNT(*) FROM edges WHERE project_id = ?", pid).Scan(&edges)
	return
}

type closureMeasurement struct {
	Depth     int
	Rows      int64
	BytesOnly int64 // size of a fresh side-DB containing just the closure table
	BuildMS   int64
}

// measureClosure builds the depth-N closure for project `pid` into a fresh
// side-DB, stats the file, removes it, and returns the measurement.
func measureClosure(src *sql.DB, pid string, maxDepth int) (closureMeasurement, error) {
	var m closureMeasurement
	m.Depth = maxDepth

	tmp, err := os.CreateTemp("", "closurebench-*.db")
	if err != nil {
		return m, fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmp.Name()
	tmp.Close()
	// Best-effort cleanup; -wal/-shm sidecars too.
	defer func() {
		os.Remove(tmpPath)
		os.Remove(tmpPath + "-wal")
		os.Remove(tmpPath + "-shm")
	}()

	dst, err := sql.Open("sqlite", "file:"+tmpPath+"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return m, fmt.Errorf("open temp: %w", err)
	}

	if _, err := dst.Exec(`CREATE TABLE closure (
		from_id TEXT NOT NULL,
		to_id TEXT NOT NULL,
		depth INTEGER NOT NULL,
		project_id TEXT NOT NULL,
		PRIMARY KEY (project_id, from_id, to_id)
	) WITHOUT ROWID`); err != nil {
		dst.Close()
		return m, fmt.Errorf("create closure: %w", err)
	}

	// Fetch all (from_id, to_id) edges for the project from src; build the
	// closure in-process using BFS so we don't rely on src side's recursive
	// CTE (which would also need to round-trip every intermediate row).
	// In-process is portable and lets us bound depth precisely.
	start := time.Now()
	rows, err := src.Query("SELECT from_id, to_id FROM edges WHERE project_id = ?", pid)
	if err != nil {
		dst.Close()
		return m, fmt.Errorf("read edges: %w", err)
	}
	adj := map[string][]string{}
	for rows.Next() {
		var from, to string
		if err := rows.Scan(&from, &to); err != nil {
			rows.Close()
			dst.Close()
			return m, fmt.Errorf("scan edge: %w", err)
		}
		adj[from] = append(adj[from], to)
	}
	rows.Close()

	// BFS from each source node to depth maxDepth. Insert (from, to, min-depth)
	// into closure for every (from_id, reachable_to) pair. Min-depth wins because
	// shorter paths are what actually matter for trace queries.
	tx, err := dst.Begin()
	if err != nil {
		dst.Close()
		return m, fmt.Errorf("begin tx: %w", err)
	}
	stmt, err := tx.Prepare("INSERT OR IGNORE INTO closure (project_id, from_id, to_id, depth) VALUES (?, ?, ?, ?)")
	if err != nil {
		tx.Rollback()
		dst.Close()
		return m, fmt.Errorf("prepare: %w", err)
	}

	for from := range adj {
		// Each starting node: BFS up to maxDepth, tracking min-depth per reachable.
		seen := map[string]int{from: 0}
		frontier := []string{from}
		for d := 1; d <= maxDepth && len(frontier) > 0; d++ {
			next := []string{}
			for _, u := range frontier {
				for _, v := range adj[u] {
					if _, ok := seen[v]; !ok {
						seen[v] = d
						next = append(next, v)
						if _, err := stmt.Exec(pid, from, v, d); err != nil {
							stmt.Close()
							tx.Rollback()
							dst.Close()
							return m, fmt.Errorf("insert: %w", err)
						}
					}
				}
			}
			frontier = next
		}
	}
	stmt.Close()
	if err := tx.Commit(); err != nil {
		dst.Close()
		return m, fmt.Errorf("commit: %w", err)
	}
	m.BuildMS = time.Since(start).Milliseconds()

	// Force a checkpoint so WAL bytes flush into the main file before stat.
	if _, err := dst.ExecContext(context.Background(), "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		dst.Close()
		return m, fmt.Errorf("wal_checkpoint: %w", err)
	}
	if err := dst.QueryRow("SELECT COUNT(*) FROM closure").Scan(&m.Rows); err != nil {
		dst.Close()
		return m, fmt.Errorf("count closure: %w", err)
	}
	dst.Close()

	sz, err := fileSize(tmpPath)
	if err != nil {
		return m, fmt.Errorf("stat tmp: %w", err)
	}
	m.BytesOnly = sz
	return m, nil
}

func fileSize(p string) (int64, error) {
	st, err := os.Stat(p)
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}

func mb(b int64) string {
	return fmt.Sprintf("%.1f", float64(b)/(1024*1024))
}

func printSummary(dbPath, pid string, files, edges int64, srcSize int64, results map[int]closureMeasurement) {
	fmt.Printf("Source DB:        %s (%s MB)\n", dbPath, mb(srcSize))
	fmt.Printf("Project:          %s\n", pid)
	fmt.Printf("Files indexed:    %d\n", files)
	fmt.Printf("Edges (project):  %d\n", edges)
	fmt.Printf("Platform:         %s/%s\n\n", runtime.GOOS, runtime.GOARCH)
	fmt.Println("Closure measurements (closure-only side-DB, vacuumed):")
	fmt.Println()
	for _, d := range sortedDepths(results) {
		m := results[d]
		fmt.Printf("  depth=%d:  rows=%-10d  size=%s MB  build=%dms\n",
			m.Depth, m.Rows, mb(m.BytesOnly), m.BuildMS)
		if edges > 0 {
			factor := float64(m.Rows) / float64(edges)
			fmt.Printf("            inflation vs edges: %.1fx rows\n", factor)
		}
	}
}

func printMarkdownRow(dbPath, pid string, files, edges int64, srcSize int64, results map[int]closureMeasurement) {
	// | Repo | Files | Edges | Edges DB MB | Closure d=3 rows | d=3 MB | d=5 rows | d=5 MB | Build d=3 ms | Build d=5 ms |
	d3 := results[3]
	d5 := results[5]
	// Use the project path's basename as the repo label (more meaningful
	// than the DB-file path, which always lands at $HOME/.pincher or
	// $APPDATA/pincherMCP regardless of which repo is being measured).
	repo := filepath.Base(pid)
	if repo == "." || repo == "" {
		repo = pid
	}
	fmt.Printf("| %s | %d | %d | %s | %d | %s | %d | %s | %d | %d |\n",
		repo, files, edges, mb(srcSize),
		d3.Rows, mb(d3.BytesOnly),
		d5.Rows, mb(d5.BytesOnly),
		d3.BuildMS, d5.BuildMS,
	)
}

func sortedDepths(m map[int]closureMeasurement) []int {
	out := make([]int, 0, len(m))
	for d := range m {
		out = append(out, d)
	}
	// Tiny n; bubble.
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

