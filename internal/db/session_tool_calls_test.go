// SPDX-License-Identifier: MIT

package db

import (
	"path/filepath"
	"testing"
	"time"
)

// #635 v0.64: per-call event log substrate for the dashboard
// triangulating panels. Table-from-the-start (#1152):
//
//   - Positive: RecordToolCalls persists + RecentToolCallsForSession
//     reads back, full round-trip with both populated and nil
//     TokensSaved fields.
//   - Negative: empty slice is a clean no-op (must not begin a
//     transaction or fsync).
//   - Control: rows from a different session_id don't leak into the
//     per-session query (the session filter actually filters).
//   - Cross-check: schema migration produced the indexes the
//     dashboard SQL will rely on — failing this guard means the
//     dashboard panels will silently table-scan in v0.64+.

func openTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestRecordToolCalls_RoundTrip(t *testing.T) {
	s := openTestStore(t)
	sid := "sess-roundtrip"
	now := time.Now()

	saved1 := int64(1234)
	pct1 := 67.5
	events := []ToolCallEvent{
		{
			SessionID: sid, Tool: "search", ComplexityTier: "lite",
			ResponseBytes: 512, TokensUsed: 200,
			TokensSaved: &saved1, TokensSavedPct: &pct1,
			TS: now.Add(-2 * time.Second), RequestID: "req-1",
		},
		{
			// "none" baseline tool — TokensSaved/Pct nil. Must
			// land as SQL NULL.
			SessionID: sid, Tool: "architecture", ComplexityTier: "standard",
			ResponseBytes: 980, TokensUsed: 350,
			TokensSaved: nil, TokensSavedPct: nil,
			TS: now.Add(-1 * time.Second), RequestID: "req-2",
		},
	}
	if err := s.RecordToolCalls(events); err != nil {
		t.Fatalf("RecordToolCalls: %v", err)
	}

	got, err := s.RecentToolCallsForSession(sid, 10)
	if err != nil {
		t.Fatalf("RecentToolCallsForSession: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2", len(got))
	}
	// Newest first — req-2 is one second newer than req-1.
	if got[0].RequestID != "req-2" || got[1].RequestID != "req-1" {
		t.Errorf("ordering: got %s,%s; want req-2,req-1",
			got[0].RequestID, got[1].RequestID)
	}
	// req-2 is the architecture call with nil saved fields.
	if got[0].TokensSaved != nil || got[0].TokensSavedPct != nil {
		t.Errorf("architecture row: saved=%v, pct=%v; want both nil",
			got[0].TokensSaved, got[0].TokensSavedPct)
	}
	// req-1 carries the saved fields end-to-end.
	if got[1].TokensSaved == nil || *got[1].TokensSaved != 1234 {
		t.Errorf("search row TokensSaved: got %v, want 1234", got[1].TokensSaved)
	}
	if got[1].TokensSavedPct == nil || *got[1].TokensSavedPct != 67.5 {
		t.Errorf("search row TokensSavedPct: got %v, want 67.5", got[1].TokensSavedPct)
	}
	if got[1].ComplexityTier != "lite" {
		t.Errorf("search row tier: got %q, want lite", got[1].ComplexityTier)
	}
}

// Negative: empty slice is a no-op — no error, no transaction begun.
// Pre-fix a naive implementation might still call Begin() and fsync;
// pinning the empty case keeps the hot path cheap when a flush ticker
// fires with an empty buffer.
func TestRecordToolCalls_EmptyIsNoop(t *testing.T) {
	s := openTestStore(t)
	if err := s.RecordToolCalls(nil); err != nil {
		t.Errorf("nil slice: %v; want clean no-op", err)
	}
	if err := s.RecordToolCalls([]ToolCallEvent{}); err != nil {
		t.Errorf("empty slice: %v; want clean no-op", err)
	}
	// Nothing should have been written.
	got, err := s.RecentToolCallsForSession("any", 10)
	if err != nil {
		t.Fatalf("RecentToolCallsForSession: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d rows after no-op writes; want 0", len(got))
	}
}

// Control: session filter must actually filter — a row written under
// session A must not surface when reading session B's rows. Pre-fix
// a typo in the SELECT predicate could return everything; the
// dashboard panels per-session view would silently aggregate across
// sessions.
func TestRecordToolCalls_SessionIsolation(t *testing.T) {
	s := openTestStore(t)
	now := time.Now()
	saved := int64(100)
	pct := 50.0
	events := []ToolCallEvent{
		{SessionID: "sess-A", Tool: "search", ResponseBytes: 1, TokensUsed: 1,
			TokensSaved: &saved, TokensSavedPct: &pct, TS: now, RequestID: "a-1"},
		{SessionID: "sess-B", Tool: "trace", ResponseBytes: 1, TokensUsed: 1,
			TokensSaved: &saved, TokensSavedPct: &pct, TS: now, RequestID: "b-1"},
	}
	if err := s.RecordToolCalls(events); err != nil {
		t.Fatalf("RecordToolCalls: %v", err)
	}

	a, err := s.RecentToolCallsForSession("sess-A", 10)
	if err != nil {
		t.Fatalf("session A: %v", err)
	}
	if len(a) != 1 || a[0].RequestID != "a-1" {
		t.Errorf("session A: got %v; want exactly [a-1]", a)
	}
	b, err := s.RecentToolCallsForSession("sess-B", 10)
	if err != nil {
		t.Fatalf("session B: %v", err)
	}
	if len(b) != 1 || b[0].RequestID != "b-1" {
		t.Errorf("session B: got %v; want exactly [b-1]", b)
	}
}

// Cross-check: the v27 migration must have created the three indexes
// the dashboard SQL will rely on. Without them, trailing-7d window
// scans on a million-row session_tool_calls table degrade to full
// table scans — a hidden cliff the dashboard would hit silently.
func TestSchemaV27_DashboardIndexesPresent(t *testing.T) {
	s := openTestStore(t)

	wantIndexes := []string{
		"idx_session_tool_calls_session", // per-session lookups
		"idx_session_tool_calls_ts",      // trailing-window scans
		"idx_session_tool_calls_tool_ts", // per-tool aggregation
	}
	rows, err := s.RO().Query(
		`SELECT name FROM sqlite_master WHERE type='index' AND tbl_name='session_tool_calls'`)
	if err != nil {
		t.Fatalf("query indexes: %v", err)
	}
	defer rows.Close()
	got := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[name] = true
	}
	for _, want := range wantIndexes {
		if !got[want] {
			t.Errorf("missing index %q on session_tool_calls; got=%v\n"+
				"dashboard panels will table-scan without this — the migration is incomplete",
				want, got)
		}
	}
}
