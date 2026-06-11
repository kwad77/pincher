// SPDX-License-Identifier: MIT

package db

import "time"

// ─────────────────────────────────────────────────────────────────────────────
// Loop ledger operations (loop-substrate PR-8/9)
//
// Append-only work-state for multi-iteration agent loops. The ledger is
// what survives the context window: each checkpoint records one EGDL
// iteration's {claim, decision, confidence, reopen_trigger, evidence},
// and `loop resume` composes the tail into a bounded brief a fresh
// session — or a different model — can pick up from in one call.
// ─────────────────────────────────────────────────────────────────────────────

// LoopCheckpoint is one append-only ledger row.
type LoopCheckpoint struct {
	ID            int64  `json:"-"`
	ProjectID     string `json:"-"`
	LoopName      string `json:"loop"`
	Seq           int    `json:"seq"`
	CreatedAt     string `json:"created_at"`
	Claim         string `json:"claim,omitempty"`
	Decision      string `json:"decision,omitempty"`
	Confidence    string `json:"confidence,omitempty"`
	ReopenTrigger string `json:"reopen_trigger,omitempty"`
	Evidence      string `json:"evidence,omitempty"`
	Watermark     string `json:"watermark,omitempty"`
}

// LoopSummary is the per-loop aggregate returned by ListLoops.
type LoopSummary struct {
	LoopName      string `json:"loop"`
	Checkpoints   int    `json:"checkpoints"`
	LastCreatedAt string `json:"last_created_at"`
}

// AppendLoopCheckpoint appends one ledger row, allocating the next seq
// for (project, loop) atomically via a subselect. Returns the seq.
func (s *Store) AppendLoopCheckpoint(cp LoopCheckpoint) (int, error) {
	if cp.CreatedAt == "" {
		cp.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	res, err := s.db.Exec(
		`INSERT INTO loop_checkpoints
			(project_id, loop_name, seq, created_at, claim, decision, confidence, reopen_trigger, evidence, watermark)
		 VALUES (?,?,
			(SELECT COALESCE(MAX(seq),0)+1 FROM loop_checkpoints WHERE project_id=? AND loop_name=?),
			?,?,?,?,?,?,?)`,
		cp.ProjectID, cp.LoopName, cp.ProjectID, cp.LoopName,
		cp.CreatedAt, cp.Claim, cp.Decision, cp.Confidence, cp.ReopenTrigger, cp.Evidence, cp.Watermark)
	if err != nil {
		return 0, err
	}
	rowID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	var seq int
	// Writer handle on purpose: the row was just written through s.db
	// and a WAL reader may not see it yet.
	err = s.db.QueryRow(`SELECT seq FROM loop_checkpoints WHERE id=?`, rowID).Scan(&seq)
	return seq, err
}

// ListLoopCheckpoints returns up to limit rows for one loop, newest
// (highest seq) first.
func (s *Store) ListLoopCheckpoints(projectID, loopName string, limit int) ([]LoopCheckpoint, error) {
	if limit <= 0 {
		limit = 10
	}
	// Reader pool (#51).
	rows, err := s.ro.Query(
		`SELECT id, project_id, loop_name, seq, created_at, claim, decision, confidence, reopen_trigger, evidence, watermark
		 FROM loop_checkpoints WHERE project_id=? AND loop_name=?
		 ORDER BY seq DESC LIMIT ?`, projectID, loopName, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LoopCheckpoint
	for rows.Next() {
		var cp LoopCheckpoint
		if err := rows.Scan(&cp.ID, &cp.ProjectID, &cp.LoopName, &cp.Seq, &cp.CreatedAt,
			&cp.Claim, &cp.Decision, &cp.Confidence, &cp.ReopenTrigger, &cp.Evidence, &cp.Watermark); err != nil {
			return nil, err
		}
		out = append(out, cp)
	}
	return out, rows.Err()
}

// ListLoops returns per-loop summaries for a project, most recently
// touched first.
func (s *Store) ListLoops(projectID string, limit int) ([]LoopSummary, error) {
	if limit <= 0 {
		limit = 20
	}
	// Reader pool (#51).
	rows, err := s.ro.Query(
		`SELECT loop_name, COUNT(*), MAX(created_at)
		 FROM loop_checkpoints WHERE project_id=?
		 GROUP BY loop_name ORDER BY MAX(created_at) DESC LIMIT ?`, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LoopSummary
	for rows.Next() {
		var ls LoopSummary
		if err := rows.Scan(&ls.LoopName, &ls.Checkpoints, &ls.LastCreatedAt); err != nil {
			return nil, err
		}
		out = append(out, ls)
	}
	return out, rows.Err()
}
