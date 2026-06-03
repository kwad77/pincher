// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// fixtureDB creates an in-temp-dir SQLite db pre-populated with a tiny
// projects/symbols/edges schema matching pincher's real schema, plus N
// synthetic projects + edges. Returns DB path; caller cleans up via t.Cleanup.
//
// Graph shape:
//   project=p1: 3 nodes A→B→C plus A→C (small DAG)
//   project=p2: optional, only when wantTwo
//
// Just enough for the closurebench measurement code to exercise
// resolveProject, projectStats, and measureClosure end-to-end.
func fixtureDB(t *testing.T, wantTwo bool) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "pincher.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := db.Exec(`
		CREATE TABLE projects (id TEXT PRIMARY KEY, path TEXT, name TEXT, indexed_at INTEGER);
		CREATE TABLE symbols (id TEXT PRIMARY KEY, project_id TEXT, file_path TEXT, name TEXT);
		CREATE TABLE edges (id INTEGER PRIMARY KEY AUTOINCREMENT, project_id TEXT, from_id TEXT, to_id TEXT, kind TEXT);
		INSERT INTO projects VALUES ('p1', '/repo/p1', 'p1', 1000);
		INSERT INTO symbols VALUES ('A', 'p1', 'a.go', 'A'), ('B', 'p1', 'b.go', 'B'), ('C', 'p1', 'c.go', 'C');
		INSERT INTO edges (project_id, from_id, to_id, kind) VALUES
			('p1', 'A', 'B', 'CALLS'),
			('p1', 'B', 'C', 'CALLS'),
			('p1', 'A', 'C', 'CALLS');
	`); err != nil {
		t.Fatalf("schema: %v", err)
	}
	if wantTwo {
		if _, err := db.Exec(`
			INSERT INTO projects VALUES ('p2', '/repo/p2', 'p2', 2000);
			INSERT INTO symbols VALUES ('X', 'p2', 'x.go', 'X');
			INSERT INTO edges (project_id, from_id, to_id, kind) VALUES ('p2', 'X', 'X', 'CALLS');
		`); err != nil {
			t.Fatalf("p2 schema: %v", err)
		}
	}
	return path
}

func TestDefaultDBPath(t *testing.T) {
	got := defaultDBPath()
	if !strings.HasSuffix(filepath.ToSlash(got), ".pincher/pincher.db") && got != "pincher.db" {
		t.Errorf("defaultDBPath unexpected: %s", got)
	}
}

func TestResolveProject_PicksLatestWhenWantEmpty(t *testing.T) {
	path := fixtureDB(t, true)
	db, _ := sql.Open("sqlite", "file:"+path)
	defer db.Close()

	got, err := resolveProject(db, "")
	if err != nil {
		t.Fatalf("resolveProject: %v", err)
	}
	if got != "p2" {
		t.Errorf("expected latest project p2 (indexed_at=2000); got %s", got)
	}
}

func TestResolveProject_RespectsExplicit(t *testing.T) {
	path := fixtureDB(t, true)
	db, _ := sql.Open("sqlite", "file:"+path)
	defer db.Close()

	got, err := resolveProject(db, "p1")
	if err != nil {
		t.Fatalf("resolveProject: %v", err)
	}
	if got != "p1" {
		t.Errorf("expected p1; got %s", got)
	}
}

func TestResolveProject_MissingProject(t *testing.T) {
	path := fixtureDB(t, false)
	db, _ := sql.Open("sqlite", "file:"+path)
	defer db.Close()

	if _, err := resolveProject(db, "nonexistent"); err == nil {
		t.Errorf("expected error for nonexistent project; got nil")
	}
}

func TestResolveProject_EmptyDB(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pincher.db")
	db, _ := sql.Open("sqlite", "file:"+path)
	defer db.Close()
	db.Exec(`CREATE TABLE projects (id TEXT PRIMARY KEY, path TEXT, name TEXT, indexed_at INTEGER);`)

	if _, err := resolveProject(db, ""); err == nil {
		t.Errorf("expected error on empty projects table; got nil")
	}
}

func TestProjectStats_CountsFilesAndEdges(t *testing.T) {
	path := fixtureDB(t, false)
	db, _ := sql.Open("sqlite", "file:"+path)
	defer db.Close()

	files, edges, err := projectStats(db, "p1")
	if err != nil {
		t.Fatalf("projectStats: %v", err)
	}
	if files != 3 {
		t.Errorf("expected 3 files; got %d", files)
	}
	if edges != 3 {
		t.Errorf("expected 3 edges; got %d", edges)
	}
}

func TestMeasureClosure_BuildsExpectedRows(t *testing.T) {
	path := fixtureDB(t, false)
	db, _ := sql.Open("sqlite", "file:"+path)
	defer db.Close()

	// Graph: A→B, B→C, A→C. Closure at depth=3:
	//   from A: B (d1), C (d1). Reach via A→B→C is d2 but C already at d1; min wins.
	//   from B: C (d1).
	// → 3 rows total: (A,B,1), (A,C,1), (B,C,1).
	m, err := measureClosure(db, "p1", 3)
	if err != nil {
		t.Fatalf("measureClosure: %v", err)
	}
	if m.Rows != 3 {
		t.Errorf("expected 3 closure rows; got %d", m.Rows)
	}
	if m.BytesOnly <= 0 {
		t.Errorf("expected positive byte size; got %d", m.BytesOnly)
	}
	if m.Depth != 3 {
		t.Errorf("Depth field not propagated; got %d", m.Depth)
	}
}

func TestMeasureClosure_DepthOneOnlyDirectEdges(t *testing.T) {
	path := fixtureDB(t, false)
	db, _ := sql.Open("sqlite", "file:"+path)
	defer db.Close()

	m, err := measureClosure(db, "p1", 1)
	if err != nil {
		t.Fatalf("measureClosure d=1: %v", err)
	}
	// At depth=1, BFS only traverses the immediate frontier — same 3 edges.
	// (At d=1 every direct edge is the only thing reachable.)
	if m.Rows != 3 {
		t.Errorf("depth=1: expected 3 rows; got %d", m.Rows)
	}
}

func TestMeasureClosure_TransitiveAddsRowsViaSecondHop(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pincher.db")
	db, _ := sql.Open("sqlite", "file:"+path)
	defer db.Close()

	// Linear chain A→B→C→D. depth=1: 3 rows. depth=3: 6 rows
	// (A,B), (A,C), (A,D), (B,C), (B,D), (C,D).
	if _, err := db.Exec(`
		CREATE TABLE projects (id TEXT PRIMARY KEY, path TEXT, name TEXT, indexed_at INTEGER);
		CREATE TABLE symbols (id TEXT PRIMARY KEY, project_id TEXT, file_path TEXT, name TEXT);
		CREATE TABLE edges (id INTEGER PRIMARY KEY AUTOINCREMENT, project_id TEXT, from_id TEXT, to_id TEXT, kind TEXT);
		INSERT INTO projects VALUES ('chain', '/x', 'x', 1);
		INSERT INTO edges (project_id, from_id, to_id, kind) VALUES
			('chain', 'A', 'B', 'CALLS'),
			('chain', 'B', 'C', 'CALLS'),
			('chain', 'C', 'D', 'CALLS');
	`); err != nil {
		t.Fatalf("setup: %v", err)
	}

	m1, _ := measureClosure(db, "chain", 1)
	if m1.Rows != 3 {
		t.Errorf("depth=1 chain: expected 3; got %d", m1.Rows)
	}
	m3, _ := measureClosure(db, "chain", 3)
	if m3.Rows != 6 {
		t.Errorf("depth=3 chain: expected 6 (3 direct + 2 d=2 + 1 d=3); got %d", m3.Rows)
	}
}

func TestFileSize_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(p, []byte("hello world"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := fileSize(p)
	if err != nil {
		t.Fatalf("fileSize: %v", err)
	}
	if got != int64(len("hello world")) {
		t.Errorf("expected 11; got %d", got)
	}

	if _, err := fileSize(filepath.Join(dir, "missing")); err == nil {
		t.Errorf("expected error on missing file")
	}
}

func TestMB_FormatsToOneDecimal(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0.0"},
		{1024 * 1024, "1.0"},
		{1024*1024 + 512*1024, "1.5"},
	}
	for _, c := range cases {
		if got := mb(c.in); got != c.want {
			t.Errorf("mb(%d) = %s; want %s", c.in, got, c.want)
		}
	}
}

func TestSortedDepths_AscendingOrder(t *testing.T) {
	got := sortedDepths(map[int]closureMeasurement{5: {}, 1: {}, 3: {}})
	if len(got) != 3 || got[0] != 1 || got[1] != 3 || got[2] != 5 {
		t.Errorf("sortedDepths: %v; want [1 3 5]", got)
	}
}

// captureStdout swaps os.Stdout, runs fn, and returns what fn wrote.
// Used to test the printX functions without changing their signature.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	fn()
	w.Close()
	os.Stdout = orig
	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.String()
}

func TestPrintMarkdownRow_Shape(t *testing.T) {
	results := map[int]closureMeasurement{
		3: {Depth: 3, Rows: 100, BytesOnly: 1024 * 1024, BuildMS: 50},
		5: {Depth: 5, Rows: 250, BytesOnly: 3 * 1024 * 1024, BuildMS: 130},
	}
	out := captureStdout(t, func() {
		printMarkdownRow("/some/repo/.pincher/pincher.db", "myrepo", 100, 200, 5*1024*1024, results)
	})
	out = strings.TrimSpace(out)
	// Spec: | repo | files | edges | srcMB | d3rows | d3MB | d5rows | d5MB | d3ms | d5ms |
	want := "| myrepo | 100 | 200 | 5.0 | 100 | 1.0 | 250 | 3.0 | 50 | 130 |"
	if out != want {
		t.Errorf("markdown row mismatch:\n got: %s\nwant: %s", out, want)
	}
}

func TestRun_HappyPath_DefaultSummary(t *testing.T) {
	path := fixtureDB(t, false)
	var stderr bytes.Buffer
	out := captureStdout(t, func() {
		code := run([]string{"-db", path, "-depth", "1,3"}, &stderr)
		if code != 0 {
			t.Fatalf("run exit=%d stderr=%s", code, stderr.String())
		}
	})
	for _, want := range []string{"Project:", "p1", "depth=1:", "depth=3:"} {
		if !strings.Contains(out, want) {
			t.Errorf("run summary output missing %q:\n%s", want, out)
		}
	}
}

func TestRun_MarkdownMode(t *testing.T) {
	path := fixtureDB(t, false)
	var stderr bytes.Buffer
	out := captureStdout(t, func() {
		code := run([]string{"-db", path, "-md", "-depth", "3,5"}, &stderr)
		if code != 0 {
			t.Fatalf("run -md exit=%d stderr=%s", code, stderr.String())
		}
	})
	out = strings.TrimSpace(out)
	if !strings.HasPrefix(out, "| ") || !strings.HasSuffix(out, " |") {
		t.Errorf("expected single markdown row; got: %s", out)
	}
}

func TestRun_MissingDB_ExitsNonZero(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "does-not-exist.db")
	var stderr bytes.Buffer
	code := run([]string{"-db", missing}, &stderr)
	if code == 0 {
		t.Errorf("expected non-zero exit on missing DB")
	}
	if !strings.Contains(stderr.String(), "closurebench:") {
		t.Errorf("expected error message; got: %s", stderr.String())
	}
}

func TestRun_InvalidDepth(t *testing.T) {
	path := fixtureDB(t, false)
	var stderr bytes.Buffer
	code := run([]string{"-db", path, "-depth", "not-a-number"}, &stderr)
	if code == 0 {
		t.Errorf("expected non-zero exit on invalid depth")
	}
	if !strings.Contains(stderr.String(), "invalid depth") {
		t.Errorf("expected 'invalid depth' message; got: %s", stderr.String())
	}
}

func TestRun_BadFlag_ParseError(t *testing.T) {
	var stderr bytes.Buffer
	code := run([]string{"-not-a-flag"}, &stderr)
	if code != 2 {
		t.Errorf("expected exit code 2 on flag parse error; got %d", code)
	}
}

func TestRun_NegativeDepth(t *testing.T) {
	path := fixtureDB(t, false)
	var stderr bytes.Buffer
	code := run([]string{"-db", path, "-depth", "0"}, &stderr)
	if code == 0 {
		t.Errorf("expected non-zero exit on depth=0")
	}
}

func TestRun_NonexistentProject(t *testing.T) {
	path := fixtureDB(t, false)
	var stderr bytes.Buffer
	code := run([]string{"-db", path, "-project", "nope"}, &stderr)
	if code == 0 {
		t.Errorf("expected non-zero exit on missing project")
	}
}

func TestPrintSummary_NoEdges_SkipsInflationLine(t *testing.T) {
	results := map[int]closureMeasurement{
		3: {Depth: 3, Rows: 0, BytesOnly: 1024, BuildMS: 5},
	}
	out := captureStdout(t, func() {
		printSummary("/x/pincher.db", "p1", 0, 0, 1024, results)
	})
	if strings.Contains(out, "inflation vs edges") {
		t.Errorf("inflation line should not print when edges=0; got:\n%s", out)
	}
}

func TestPrintSummary_WithEdges_IncludesInflationLine(t *testing.T) {
	results := map[int]closureMeasurement{
		3: {Depth: 3, Rows: 30, BytesOnly: 1024, BuildMS: 5},
	}
	out := captureStdout(t, func() {
		printSummary("/x/pincher.db", "p1", 5, 10, 1024, results)
	})
	if !strings.Contains(out, "inflation vs edges") {
		t.Errorf("inflation line should print when edges>0; got:\n%s", out)
	}
}

func TestRun_BigFixtureMultipleDepths_BuildsBoth(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pincher.db")
	db, _ := sql.Open("sqlite", "file:"+path)
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TABLE projects (id TEXT PRIMARY KEY, path TEXT, name TEXT, indexed_at INTEGER);
		CREATE TABLE symbols (id TEXT PRIMARY KEY, project_id TEXT, file_path TEXT, name TEXT);
		CREATE TABLE edges (id INTEGER PRIMARY KEY AUTOINCREMENT, project_id TEXT, from_id TEXT, to_id TEXT, kind TEXT);
		INSERT INTO projects VALUES ('chain', '/x', 'chain', 1);
	`); err != nil {
		t.Fatalf("schema: %v", err)
	}
	for i := 0; i < 10; i++ {
		if _, err := db.Exec(`INSERT INTO symbols VALUES (?, 'chain', 'f.go', ?)`,
			fmt.Sprintf("N%d", i), fmt.Sprintf("N%d", i)); err != nil {
			t.Fatalf("symbol: %v", err)
		}
	}
	for i := 0; i < 9; i++ {
		if _, err := db.Exec(`INSERT INTO edges (project_id, from_id, to_id, kind) VALUES ('chain', ?, ?, 'CALLS')`,
			fmt.Sprintf("N%d", i), fmt.Sprintf("N%d", i+1)); err != nil {
			t.Fatalf("edge: %v", err)
		}
	}

	var stderr bytes.Buffer
	out := captureStdout(t, func() {
		code := run([]string{"-db", path, "-depth", "3,5"}, &stderr)
		if code != 0 {
			t.Fatalf("run exit=%d stderr=%s", code, stderr.String())
		}
	})
	for _, want := range []string{"depth=3:", "depth=5:", "inflation vs edges"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in summary:\n%s", want, out)
		}
	}
}

func TestRun_EmptyDepthInList_Skipped(t *testing.T) {
	path := fixtureDB(t, false)
	var stderr bytes.Buffer
	code := run([]string{"-db", path, "-depth", "1, ,3,"}, &stderr)
	if code != 0 {
		t.Errorf("expected success with empty depth tokens; got %d, stderr=%s", code, stderr.String())
	}
}

func TestPrintSummary_ContainsKeyFields(t *testing.T) {
	results := map[int]closureMeasurement{
		3: {Depth: 3, Rows: 100, BytesOnly: 1024 * 1024, BuildMS: 50},
	}
	out := captureStdout(t, func() {
		printSummary("/x/pincher.db", "p1", 10, 20, 1024*1024, results)
	})
	for _, want := range []string{"Source DB:", "Project:", "Files indexed:", "Edges (project):", "depth=3:"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary missing %q in output:\n%s", want, out)
		}
	}
}
