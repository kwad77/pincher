// SPDX-License-Identifier: MIT

package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"time"
)

// Closure-table feature flag (#652 phase 1, #403). Off by default in v0.54;
// opt-in via PINCHER_CLOSURE_TABLES=1 at index time. Builder runs at end
// of indexer.Index() when set; trace tool routes to the closure-fast-path
// when the table has rows for the queried project.
//
// Default depth=3 per #639 storage measurement (~325 MB linear-worst-case
// on a 10k-file repo at depth=3, well under the 500 MB phase-1 budget).
// Operators wanting longer reach configure PINCHER_CLOSURE_MAX_DEPTH=5
// after measuring on their own corpus.
const (
	envClosureEnabled  = "PINCHER_CLOSURE_TABLES"
	envClosureMaxDepth = "PINCHER_CLOSURE_MAX_DEPTH"
	defaultClosureDepth = 3
)

// ClosureEnabled reports whether closure-table materialization is on for
// this process. Read at indexer tail-pass and at trace-handler dispatch
// time so toggling the env between calls works without restart.
func ClosureEnabled() bool {
	v := os.Getenv(envClosureEnabled)
	return v == "1" || v == "true" || v == "TRUE"
}

// ClosureMaxDepth returns the configured maximum closure depth. Defaults
// to 3 when the env var is unset or invalid (per #639 measurement,
// depth=3 is the largest depth that fits under the 500 MB budget on a
// 10k-file repo). Hard-clamped to [1, 8] — anything deeper bloats
// storage cubically without buying real trace coverage.
func ClosureMaxDepth() int {
	v := os.Getenv(envClosureMaxDepth)
	if v == "" {
		return defaultClosureDepth
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return defaultClosureDepth
	}
	if n > 8 {
		return 8
	}
	return n
}

// BuildClosure materializes the depth-N transitive closure of `projectID`'s
// edge graph into the `closure` table. Replaces any prior closure rows for
// the project — safe to call repeatedly after re-index (the indexer's
// tail-pass fires this every time so the closure stays in lockstep with
// the edges table).
//
// Approach: BFS from each source node up to maxDepth, inserting (from, to,
// min-depth) per reachable target. INSERT OR IGNORE + ascending depth
// guarantees the first insert wins, which is the shortest path — exactly
// what trace queries want.
//
// Callers must hold the project write lock (cross-process or in-process)
// since this both deletes prior rows and inserts new ones in a single
// transaction.
func (s *Store) BuildClosure(ctx context.Context, projectID string, maxDepth int) error {
	if maxDepth < 1 {
		return fmt.Errorf("BuildClosure: maxDepth must be ≥ 1, got %d", maxDepth)
	}

	// Read edges for the project into an adjacency list. Filter to the
	// default trace kind set so closure semantics match what TraceByID
	// fast-paths through — pre-fix the closure traversed ALL edge kinds
	// (READS, WRITES, IMPORTS, REFERENCES, etc.) while TraceViaCTE
	// filtered to CALLS family by default, so the fast-path returned a
	// superset that silently disagreed with the CTE path (#1162
	// measurement surfaced the divergence; #685 closes the gap).
	//
	// For a 14k-edge project (pincher itself) this is ~500 KB of memory
	// — bounded enough to keep in-process. For 10k-file repos we expect
	// 250k–500k edges = ~10–20 MB; still bounded.
	rows, err := s.db.QueryContext(ctx,
		`SELECT from_id, to_id, kind FROM edges
		  WHERE project_id = ?
		    AND kind IN ('CALLS', 'HTTP_CALLS', 'ASYNC_CALLS')`, projectID)
	if err != nil {
		return fmt.Errorf("BuildClosure: read edges: %w", err)
	}
	// edge holds the target + the kind that connects from→to. Used by the
	// BFS to record via_kind on each closure row.
	type edge struct {
		to   string
		kind string
	}
	adj := make(map[string][]edge)
	for rows.Next() {
		var from, to, kind string
		if err := rows.Scan(&from, &to, &kind); err != nil {
			rows.Close()
			return fmt.Errorf("BuildClosure: scan edge: %w", err)
		}
		adj[from] = append(adj[from], edge{to: to, kind: kind})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("BuildClosure: edge iter: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("BuildClosure: begin: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM closure WHERE project_id = ?", projectID); err != nil {
		tx.Rollback()
		return fmt.Errorf("BuildClosure: delete prior: %w", err)
	}
	stmt, err := tx.PrepareContext(ctx,
		"INSERT OR IGNORE INTO closure (project_id, from_id, to_id, depth, via_kind) VALUES (?, ?, ?, ?, ?)")
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("BuildClosure: prepare: %w", err)
	}

	// BFS per source. seen[v] = depth of first reach (= min depth, since
	// BFS ascends in lockstep). frontier holds (node, last-hop-kind) so
	// the kind of the edge that completed the path can be recorded on
	// the closure row — closes the v0.54 phase-1 Via gap (#685).
	type frontierNode struct {
		id   string
		kind string // kind of the edge that reached this node
	}
	for from := range adj {
		seen := map[string]int{from: 0}
		frontier := []frontierNode{{id: from}}
		for d := 1; d <= maxDepth && len(frontier) > 0; d++ {
			next := frontier[:0:0]
			for _, u := range frontier {
				for _, e := range adj[u.id] {
					if _, ok := seen[e.to]; ok {
						continue
					}
					seen[e.to] = d
					next = append(next, frontierNode{id: e.to, kind: e.kind})
					if _, err := stmt.ExecContext(ctx, projectID, from, e.to, d, e.kind); err != nil {
						stmt.Close()
						tx.Rollback()
						return fmt.Errorf("BuildClosure: insert: %w", err)
					}
				}
			}
			frontier = next
		}
	}
	if err := stmt.Close(); err != nil {
		tx.Rollback()
		return fmt.Errorf("BuildClosure: close stmt: %w", err)
	}
	return tx.Commit()
}

// ClosureRowCount returns the number of materialized closure rows for the
// given project. The trace handler reads this to decide whether to take
// the fast-path (single indexed SELECT against closure) or fall through
// to the recursive-CTE path. A return of 0 means closure is either off
// or hasn't been built yet for this project.
func (s *Store) ClosureRowCount(projectID string) (int64, error) {
	var n int64
	// Reader pool (#1824/#1826): a pure SELECT must not run on the
	// single-writer connection — it would serialize behind index writes.
	err := s.ro.QueryRow(
		"SELECT COUNT(*) FROM closure WHERE project_id = ?", projectID).Scan(&n)
	return n, err
}

// TraceViaClosure is the fast-path trace lookup against the materialized
// closure table. Returns the same shape as TraceViaCTE — and as of
// schema v30 / #685 the `via` (edge-kind) field is populated from the
// closure row's via_kind column (the last-hop kind recorded at build
// time). Pre-v30 rows have via_kind='' which surfaces as an empty Via,
// matching the v0.54 phase-1 behaviour until a closure rebuild fires.
//
// Direction:
//   - "outbound": all symbols reachable FROM startID
//   - "inbound":  all symbols that reach TO startID
//   - "both":     union of the above
//
// maxDepth filters to results at depth ≤ maxDepth. The closure was built
// up to its own max-depth (PINCHER_CLOSURE_MAX_DEPTH); requests beyond
// that ceiling are silently capped — caller can probe ClosureRowCount or
// the new closure_max_depth capability to know the ceiling.
func (s *Store) TraceViaClosure(projectID, startID, direction string, maxDepth int) ([]TraceResult, error) {
	out := make([]TraceResult, 0, 64)
	emit := func(rows *sql.Rows) error {
		for rows.Next() {
			var r TraceResult
			if err := rows.Scan(&r.SymbolID, &r.Depth, &r.ViaKind); err != nil {
				return err
			}
			out = append(out, r)
		}
		return rows.Err()
	}

	if direction == "outbound" || direction == "both" {
		rows, err := s.ro.Query( // reader pool — pure SELECT (#1824/#1826)
			"SELECT to_id, depth, via_kind FROM closure WHERE project_id = ? AND from_id = ? AND depth <= ? ORDER BY depth, to_id LIMIT 500",
			projectID, startID, maxDepth)
		if err != nil {
			return nil, fmt.Errorf("TraceViaClosure outbound: %w", err)
		}
		err = emit(rows)
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	if direction == "inbound" || direction == "both" {
		rows, err := s.ro.Query( // reader pool — pure SELECT (#1824/#1826)
			"SELECT from_id, depth, via_kind FROM closure WHERE project_id = ? AND to_id = ? AND depth <= ? ORDER BY depth, from_id LIMIT 500",
			projectID, startID, maxDepth)
		if err != nil {
			return nil, fmt.Errorf("TraceViaClosure inbound: %w", err)
		}
		err = emit(rows)
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// closureBuildAt is the wall-clock build time accumulator for the current
// indexer pass — surfaced to operators via index summary metrics in a
// future revision; for now the indexer logs the duration so operators can
// see how much the closure pass costs on their corpus.
type closureBuildMetric struct {
	Project   string
	Rows      int64
	Duration  time.Duration
	MaxDepth  int
}
