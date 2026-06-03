// SPDX-License-Identifier: MIT

package db

import (
	"context"
	"os"
	"testing"
)

// closureFixture creates a temp Store with a tiny synthetic graph for the
// closure-table tests. Graph: project=p1 with chain A→B→C→D plus A→C.
//   d=1 outbound from A: {B, C} (A→B direct, A→C direct)
//   d=2 outbound from A: + {D}  (via A→B→C? no, via A→C→D since A reaches C at d=1)
// Wait: BFS from A — A→B (d=1), A→C (d=1). From those: B→C is to a node already
// at d=1 (C seen earlier), C→D is new at d=2. So closure(A,*) = {B:1, C:1, D:2}.
func closureFixture(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	pid := "p1"
	if err := store.UpsertProject(Project{ID: pid, Path: dir, Name: "p1"}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	// Insert raw symbols so foreign-key references hold.
	for _, id := range []string{"A", "B", "C", "D"} {
		if _, err := store.DB().Exec(
			`INSERT INTO symbols (id, project_id, file_path, name, qualified_name, kind, language, start_byte, end_byte, start_line, end_line) VALUES (?, ?, ?, ?, ?, 'Function', 'Go', 0, 1, 1, 1)`,
			id, pid, id+".go", id, id); err != nil {
			t.Fatalf("insert symbol %s: %v", id, err)
		}
	}
	for _, e := range []struct{ from, to string }{
		{"A", "B"}, {"B", "C"}, {"C", "D"}, {"A", "C"},
	} {
		if _, err := store.DB().Exec(
			`INSERT INTO edges (project_id, from_id, to_id, kind, confidence) VALUES (?, ?, ?, 'CALLS', 1.0)`,
			pid, e.from, e.to); err != nil {
			t.Fatalf("insert edge %s→%s: %v", e.from, e.to, err)
		}
	}
	return store, pid
}

func TestBuildClosure_BasicGraph(t *testing.T) {
	store, pid := closureFixture(t)
	if err := store.BuildClosure(context.Background(), pid, 3); err != nil {
		t.Fatalf("BuildClosure: %v", err)
	}
	n, err := store.ClosureRowCount(pid)
	if err != nil {
		t.Fatalf("ClosureRowCount: %v", err)
	}
	// Outbound BFS:
	//   from A: B(d=1), C(d=1), D(d=2) → 3 rows
	//   from B: C(d=1), D(d=2) → 2 rows
	//   from C: D(d=1) → 1 row
	//   from D: nothing → 0 rows
	// Total: 6 rows.
	if n != 6 {
		t.Errorf("expected 6 closure rows; got %d", n)
	}
}

func TestBuildClosure_DepthBound(t *testing.T) {
	store, pid := closureFixture(t)
	if err := store.BuildClosure(context.Background(), pid, 1); err != nil {
		t.Fatalf("BuildClosure d=1: %v", err)
	}
	n, _ := store.ClosureRowCount(pid)
	// d=1 only: 4 direct edges (A→B, A→C, B→C, C→D) → 4 rows.
	if n != 4 {
		t.Errorf("depth=1: expected 4 rows; got %d", n)
	}
}

func TestBuildClosure_RebuildsCleanly(t *testing.T) {
	store, pid := closureFixture(t)
	if err := store.BuildClosure(context.Background(), pid, 3); err != nil {
		t.Fatalf("first build: %v", err)
	}
	first, _ := store.ClosureRowCount(pid)

	// Rebuild without changing the graph — count must match.
	if err := store.BuildClosure(context.Background(), pid, 3); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	second, _ := store.ClosureRowCount(pid)
	if first != second {
		t.Errorf("rebuild changed count: %d → %d", first, second)
	}
}

func TestBuildClosure_RejectsBadDepth(t *testing.T) {
	store, pid := closureFixture(t)
	if err := store.BuildClosure(context.Background(), pid, 0); err == nil {
		t.Errorf("expected error on depth=0")
	}
	if err := store.BuildClosure(context.Background(), pid, -1); err == nil {
		t.Errorf("expected error on depth=-1")
	}
}

func TestTraceViaClosure_OutboundFromA(t *testing.T) {
	store, pid := closureFixture(t)
	if err := store.BuildClosure(context.Background(), pid, 3); err != nil {
		t.Fatalf("BuildClosure: %v", err)
	}
	results, err := store.TraceViaClosure(pid, "A", "outbound", 3)
	if err != nil {
		t.Fatalf("TraceViaClosure: %v", err)
	}
	gotIDs := make(map[string]int)
	for _, r := range results {
		gotIDs[r.SymbolID] = r.Depth
	}
	if gotIDs["B"] != 1 || gotIDs["C"] != 1 || gotIDs["D"] != 2 {
		t.Errorf("outbound from A: expected B:1 C:1 D:2; got %v", gotIDs)
	}
}

func TestTraceViaClosure_InboundToD(t *testing.T) {
	store, pid := closureFixture(t)
	if err := store.BuildClosure(context.Background(), pid, 3); err != nil {
		t.Fatalf("BuildClosure: %v", err)
	}
	results, err := store.TraceViaClosure(pid, "D", "inbound", 3)
	if err != nil {
		t.Fatalf("TraceViaClosure inbound: %v", err)
	}
	gotIDs := make(map[string]int)
	for _, r := range results {
		gotIDs[r.SymbolID] = r.Depth
	}
	// D is reached from C(d=1), B(d=2), A(d=2 via A→C→D, A reaches C at d=1 then C→D at d=2 → A reaches D at d=2)
	if gotIDs["C"] != 1 || gotIDs["B"] != 2 || gotIDs["A"] != 2 {
		t.Errorf("inbound to D: expected C:1 B:2 A:2; got %v", gotIDs)
	}
}

func TestTraceViaClosure_BothMerges(t *testing.T) {
	store, pid := closureFixture(t)
	if err := store.BuildClosure(context.Background(), pid, 3); err != nil {
		t.Fatalf("BuildClosure: %v", err)
	}
	results, err := store.TraceViaClosure(pid, "B", "both", 3)
	if err != nil {
		t.Fatalf("TraceViaClosure both: %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("expected at least one result for B/both")
	}
}

func TestTraceViaClosure_RespectsDepthCeiling(t *testing.T) {
	store, pid := closureFixture(t)
	if err := store.BuildClosure(context.Background(), pid, 3); err != nil {
		t.Fatalf("BuildClosure: %v", err)
	}
	results, err := store.TraceViaClosure(pid, "A", "outbound", 1)
	if err != nil {
		t.Fatalf("TraceViaClosure d=1: %v", err)
	}
	// d=1 caps results: A→B(1), A→C(1) — D excluded (d=2)
	for _, r := range results {
		if r.Depth > 1 {
			t.Errorf("depth filter leaked: got result at depth=%d", r.Depth)
		}
	}
}

func TestBuildClosure_EmptyProject(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()
	if err := store.UpsertProject(Project{ID: "empty", Path: dir, Name: "empty"}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	// No edges at all — BuildClosure must succeed and produce 0 rows.
	if err := store.BuildClosure(context.Background(), "empty", 3); err != nil {
		t.Fatalf("BuildClosure: %v", err)
	}
	n, _ := store.ClosureRowCount("empty")
	if n != 0 {
		t.Errorf("empty project: expected 0 closure rows; got %d", n)
	}
}

func TestBuildClosure_ContextCancellation(t *testing.T) {
	store, pid := closureFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancelled
	if err := store.BuildClosure(ctx, pid, 3); err == nil {
		t.Errorf("expected error on cancelled context")
	}
}

func TestBuildClosure_OnClosedStore(t *testing.T) {
	store, pid := closureFixture(t)
	store.Close()
	if err := store.BuildClosure(context.Background(), pid, 3); err == nil {
		t.Errorf("expected error after store.Close()")
	}
}

func TestTraceViaClosure_UnknownDirection_NoOp(t *testing.T) {
	store, pid := closureFixture(t)
	if err := store.BuildClosure(context.Background(), pid, 3); err != nil {
		t.Fatalf("BuildClosure: %v", err)
	}
	results, err := store.TraceViaClosure(pid, "A", "sideways", 3)
	if err != nil {
		t.Errorf("unexpected error on unknown direction: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for unknown direction; got %d", len(results))
	}
}

func TestClosureEnabled_ReadsEnv(t *testing.T) {
	t.Setenv(envClosureEnabled, "1")
	if !ClosureEnabled() {
		t.Errorf("ClosureEnabled() = false with PINCHER_CLOSURE_TABLES=1")
	}
	t.Setenv(envClosureEnabled, "true")
	if !ClosureEnabled() {
		t.Errorf("ClosureEnabled() = false with PINCHER_CLOSURE_TABLES=true")
	}
	t.Setenv(envClosureEnabled, "0")
	if ClosureEnabled() {
		t.Errorf("ClosureEnabled() = true with PINCHER_CLOSURE_TABLES=0")
	}
	os.Unsetenv(envClosureEnabled)
	if ClosureEnabled() {
		t.Errorf("ClosureEnabled() = true with env unset")
	}
}

func TestClosureMaxDepth_DefaultsAndClamps(t *testing.T) {
	os.Unsetenv(envClosureMaxDepth)
	if got := ClosureMaxDepth(); got != defaultClosureDepth {
		t.Errorf("default: got %d want %d", got, defaultClosureDepth)
	}
	t.Setenv(envClosureMaxDepth, "5")
	if got := ClosureMaxDepth(); got != 5 {
		t.Errorf("env=5: got %d", got)
	}
	t.Setenv(envClosureMaxDepth, "99")
	if got := ClosureMaxDepth(); got != 8 {
		t.Errorf("env=99 should clamp to 8; got %d", got)
	}
	t.Setenv(envClosureMaxDepth, "0")
	if got := ClosureMaxDepth(); got != defaultClosureDepth {
		t.Errorf("env=0 should fall back to default; got %d", got)
	}
	t.Setenv(envClosureMaxDepth, "not-a-number")
	if got := ClosureMaxDepth(); got != defaultClosureDepth {
		t.Errorf("env=garbage should fall back to default; got %d", got)
	}
}
