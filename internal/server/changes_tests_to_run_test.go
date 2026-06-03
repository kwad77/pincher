// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// Tests for #247 #4: tests_to_run on changes. The tool should return
// test functions whose call graphs reach the changed symbols, sorted
// by overlap descending so the agent can pick the top entries first.

// setupChangesGitRepo wires a temp git repo with one committed file
// then mutates it so `git diff` produces output. Returns the repo dir.
func setupChangesGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if out, err := runCmd(t, dir, "git", "init"); err != nil {
		t.Skipf("git not available: %v (%s)", err, out)
	}
	runCmd(t, dir, "git", "config", "user.email", "test@test.com")
	runCmd(t, dir, "git", "config", "user.name", "Test")

	// Commit one file, then modify it so `git diff` returns content.
	target := filepath.Join(dir, "main.go")
	os.WriteFile(target, []byte("package main\nfunc Foo() {}\nfunc Bar() {}\n"), 0o644)
	runCmd(t, dir, "git", "add", ".")
	runCmd(t, dir, "git", "commit", "-m", "init")
	os.WriteFile(target, []byte("package main\nfunc Foo() { return }\nfunc Bar() { return }\n"), 0o644)
	return dir
}

// TestHandleChanges_TestsToRun_OrderedByOverlap is the core feature
// gate. Two changed functions (Foo, Bar). Three test functions:
//   - TestBoth covers Foo AND Bar (overlap=2)
//   - TestFoo covers only Foo (overlap=1)
//   - TestBar covers only Bar (overlap=1)
// The output must rank TestBoth first, then TestBar, then TestFoo
// (the IDs are the lex tiebreaker so TestBar < TestFoo by IDs alpha).
func TestHandleChanges_TestsToRun_OrderedByOverlap(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	repoDir := setupChangesGitRepo(t)
	store.UpsertProject(db.Project{ID: repoDir, Path: repoDir, Name: "tests-to-run", IndexedAt: time.Now()})
	srv.sessionID = repoDir
	srv.sessionRoot = repoDir

	// Seed the symbols: 2 production functions + 3 tests.
	mustUpsertSymbols(t, store, []db.Symbol{
		{ID: "p::main.Foo#Function", ProjectID: repoDir, FilePath: "main.go", Name: "Foo",
			QualifiedName: "main.Foo", Kind: "Function", Language: "Go",
			StartByte: 13, EndByte: 30, StartLine: 2, EndLine: 2, ExtractionConfidence: 1.0},
		{ID: "p::main.Bar#Function", ProjectID: repoDir, FilePath: "main.go", Name: "Bar",
			QualifiedName: "main.Bar", Kind: "Function", Language: "Go",
			StartByte: 31, EndByte: 48, StartLine: 3, EndLine: 3, ExtractionConfidence: 1.0},
		{ID: "p::main.TestBoth#Function", ProjectID: repoDir, FilePath: "main_test.go", Name: "TestBoth",
			QualifiedName: "main.TestBoth", Kind: "Function", Language: "Go",
			StartByte: 0, EndByte: 100, StartLine: 1, EndLine: 5, IsTest: true, ExtractionConfidence: 1.0},
		{ID: "p::main.TestFoo#Function", ProjectID: repoDir, FilePath: "main_test.go", Name: "TestFoo",
			QualifiedName: "main.TestFoo", Kind: "Function", Language: "Go",
			StartByte: 100, EndByte: 200, StartLine: 6, EndLine: 10, IsTest: true, ExtractionConfidence: 1.0},
		{ID: "p::main.TestBar#Function", ProjectID: repoDir, FilePath: "main_test.go", Name: "TestBar",
			QualifiedName: "main.TestBar", Kind: "Function", Language: "Go",
			StartByte: 200, EndByte: 300, StartLine: 11, EndLine: 15, IsTest: true, ExtractionConfidence: 1.0},
	})

	// Edges: tests CALL the production funcs.
	mustUpsertEdges(t, store, repoDir, []db.Edge{
		{FromID: "p::main.TestBoth#Function", ToID: "p::main.Foo#Function", Kind: "CALLS"},
		{FromID: "p::main.TestBoth#Function", ToID: "p::main.Bar#Function", Kind: "CALLS"},
		{FromID: "p::main.TestFoo#Function", ToID: "p::main.Foo#Function", Kind: "CALLS"},
		{FromID: "p::main.TestBar#Function", ToID: "p::main.Bar#Function", Kind: "CALLS"},
	})

	result, err := srv.handleChanges(context.Background(), makeReq(map[string]any{"scope": "all"}))
	if err != nil {
		t.Fatalf("handleChanges: %v", err)
	}
	body := decode(t, result)
	tests, _ := body["tests_to_run"].([]any)
	if len(tests) != 3 {
		t.Fatalf("tests_to_run length = %d, want 3 (TestBoth + TestFoo + TestBar):\n%v", len(tests), body)
	}
	first, _ := tests[0].(map[string]any)
	if first["name"] != "TestBoth" {
		t.Errorf("first tests_to_run entry should be TestBoth (overlap 2); got %v", first["name"])
	}
	if overlap, _ := first["overlap"].(float64); overlap != 2 {
		t.Errorf("first overlap = %v, want 2", overlap)
	}
	// The remaining two tests both have overlap=1; the lex tie-break
	// orders TestBar before TestFoo since their IDs start with the
	// same prefix and "Bar" < "Foo".
	second, _ := tests[1].(map[string]any)
	third, _ := tests[2].(map[string]any)
	if second["name"] != "TestBar" || third["name"] != "TestFoo" {
		t.Errorf("tied-overlap entries not lex-ordered: 2nd=%v 3rd=%v (want TestBar then TestFoo)", second["name"], third["name"])
	}
}

// Summary count exposed alongside the array so consumers can show the
// count without parsing the array. Keeps the response shape consistent
// with the existing summary fields (changed_files, total_impacted, etc).
func TestHandleChanges_SummaryIncludesTestsToRunCount(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	repoDir := setupChangesGitRepo(t)
	store.UpsertProject(db.Project{ID: repoDir, Path: repoDir, Name: "summary-count", IndexedAt: time.Now()})
	srv.sessionID = repoDir
	srv.sessionRoot = repoDir

	mustUpsertSymbols(t, store, []db.Symbol{
		{ID: "p::main.Foo#Function", ProjectID: repoDir, FilePath: "main.go", Name: "Foo",
			QualifiedName: "main.Foo", Kind: "Function", Language: "Go",
			StartByte: 13, EndByte: 30, StartLine: 2, EndLine: 2, ExtractionConfidence: 1.0},
		{ID: "p::main.Bar#Function", ProjectID: repoDir, FilePath: "main.go", Name: "Bar",
			QualifiedName: "main.Bar", Kind: "Function", Language: "Go",
			StartByte: 31, EndByte: 48, StartLine: 3, EndLine: 3, ExtractionConfidence: 1.0},
		{ID: "p::main.TestFoo#Function", ProjectID: repoDir, FilePath: "main_test.go", Name: "TestFoo",
			QualifiedName: "main.TestFoo", Kind: "Function", Language: "Go",
			StartByte: 0, EndByte: 100, StartLine: 1, EndLine: 5, IsTest: true, ExtractionConfidence: 1.0},
	})
	mustUpsertEdges(t, store, repoDir, []db.Edge{
		{FromID: "p::main.TestFoo#Function", ToID: "p::main.Foo#Function", Kind: "CALLS"},
	})

	result, err := srv.handleChanges(context.Background(), makeReq(map[string]any{"scope": "all"}))
	if err != nil {
		t.Fatalf("handleChanges: %v", err)
	}
	body := decode(t, result)
	summary, _ := body["summary"].(map[string]any)
	count, _ := summary["tests_to_run"].(float64)
	if count != 1 {
		t.Errorf("summary.tests_to_run = %v, want 1", count)
	}
}

// Changed code with no inbound test edges: tests_to_run is an empty
// (non-nil) array so JSON consumers don't have to handle null. This
// is the correct UX signal — "no test exercises this change; consider
// writing one" — but the existing next_steps logic already covers
// the recommendation; we just need the array to be safely consumable.
func TestHandleChanges_TestsToRun_EmptyWhenNoTestEdges(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	repoDir := setupChangesGitRepo(t)
	store.UpsertProject(db.Project{ID: repoDir, Path: repoDir, Name: "no-tests", IndexedAt: time.Now()})
	srv.sessionID = repoDir
	srv.sessionRoot = repoDir

	mustUpsertSymbols(t, store, []db.Symbol{
		{ID: "p::main.Foo#Function", ProjectID: repoDir, FilePath: "main.go", Name: "Foo",
			QualifiedName: "main.Foo", Kind: "Function", Language: "Go",
			StartByte: 13, EndByte: 30, StartLine: 2, EndLine: 2, ExtractionConfidence: 1.0},
	})
	// No edges → no callers → no tests reach Foo.

	result, err := srv.handleChanges(context.Background(), makeReq(map[string]any{"scope": "all"}))
	if err != nil {
		t.Fatalf("handleChanges: %v", err)
	}
	body := decode(t, result)
	tests, ok := body["tests_to_run"].([]any)
	if !ok {
		t.Fatalf("tests_to_run missing or wrong type:\n%v", body)
	}
	if len(tests) != 0 {
		t.Errorf("tests_to_run should be empty when no test reaches the change; got %v", tests)
	}
}

// mustUpsertEdges is local because no shared helper exists yet — the
// bulk-insert error is fatal since a broken fixture invalidates the
// whole test. Stamps ProjectID on each edge for caller convenience.
// (mustUpsertSymbols is already defined in project_scoping_test.go.)
func mustUpsertEdges(t *testing.T, store *db.Store, projectID string, edges []db.Edge) {
	t.Helper()
	for i := range edges {
		edges[i].ProjectID = projectID
	}
	if err := store.BulkUpsertEdges(edges); err != nil {
		t.Fatalf("BulkUpsertEdges: %v", err)
	}
}

// #729 follow-up: a change to a hot file blast-radiuses into 100+
// impacted symbols; the full `changes` response then exceeds the MCP
// token limit and the tool fails by default. The `impacted` list is
// now capped at changesMaxList, the summary keeps the true total, and
// a _meta.warnings entry names the trim.
func TestHandleChanges_ImpactedListTrimmedWhenHuge(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	repoDir := setupChangesGitRepo(t)
	store.UpsertProject(db.Project{ID: repoDir, Path: repoDir, Name: "trim", IndexedAt: time.Now()})
	srv.sessionID = repoDir
	srv.sessionRoot = repoDir

	// Changed function Foo + (changesMaxList + 10) callers, all inbound.
	syms := []db.Symbol{
		{ID: "p::main.Foo#Function", ProjectID: repoDir, FilePath: "main.go", Name: "Foo",
			QualifiedName: "main.Foo", Kind: "Function", Language: "Go",
			StartByte: 13, EndByte: 30, StartLine: 2, EndLine: 2, ExtractionConfidence: 1.0},
	}
	edges := []db.Edge{}
	nCallers := changesMaxList + 10
	for i := 0; i < nCallers; i++ {
		id := "p::main.C" + itoaPad(i) + "#Function"
		syms = append(syms, db.Symbol{
			ID: id, ProjectID: repoDir, FilePath: "callers.go",
			Name: "C" + itoaPad(i), QualifiedName: "main.C" + itoaPad(i),
			Kind: "Function", Language: "Go", ExtractionConfidence: 1.0,
		})
		edges = append(edges, db.Edge{FromID: id, ToID: "p::main.Foo#Function", Kind: "CALLS"})
	}
	mustUpsertSymbols(t, store, syms)
	mustUpsertEdges(t, store, repoDir, edges)

	result, err := srv.handleChanges(context.Background(), makeReq(map[string]any{"scope": "all"}))
	if err != nil {
		t.Fatalf("handleChanges: %v", err)
	}
	body := decode(t, result)
	impacted, _ := body["impacted"].([]any)
	if len(impacted) != changesMaxList {
		t.Errorf("impacted list = %d, want capped at %d", len(impacted), changesMaxList)
	}
	// Summary keeps the TRUE total, not the trimmed length.
	summary, _ := body["summary"].(map[string]any)
	if total, _ := summary["total_impacted"].(float64); int(total) != nCallers {
		t.Errorf("summary.total_impacted = %v, want %d (true count, not trimmed)", total, nCallers)
	}
	// The trim is surfaced, not silent.
	meta, _ := body["_meta"].(map[string]any)
	warns, _ := meta["warnings"].([]any)
	if len(warns) == 0 {
		t.Fatalf("expected a _meta.warnings entry naming the trim; got _meta %v", meta)
	}
}

// #740: #730 capped `impacted` and `tests_to_run` but missed
// `changed_symbols`. On a large diff (or scope=all over a tree with
// many untracked multi-symbol files) `changed_symbols` was unbounded,
// reopening the response-bloat problem #730 closed. It must be capped
// the same way: trimmed to changesMaxList, true count kept in
// summary.changed_symbols, trim surfaced in _meta.warnings.
func TestHandleChanges_ChangedSymbolsListTrimmedWhenHuge(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	repoDir := setupChangesGitRepo(t)
	store.UpsertProject(db.Project{ID: repoDir, Path: repoDir, Name: "trim-cs", IndexedAt: time.Now()})
	srv.sessionID = repoDir
	srv.sessionRoot = repoDir

	// An untracked file has no diff hunks, so handleChanges falls back
	// to "all symbols in file" — the cheapest way to manufacture a huge
	// changed_symbols list. Put changesMaxList + 10 symbols in it.
	untracked := filepath.Join(repoDir, "untracked.go")
	os.WriteFile(untracked, []byte("package main\n"), 0o644)

	nSyms := changesMaxList + 10
	syms := make([]db.Symbol, 0, nSyms)
	for i := 0; i < nSyms; i++ {
		syms = append(syms, db.Symbol{
			ID: "p::main.U" + itoaPad(i) + "#Function", ProjectID: repoDir,
			FilePath: "untracked.go", Name: "U" + itoaPad(i),
			QualifiedName: "main.U" + itoaPad(i), Kind: "Function", Language: "Go",
			ExtractionConfidence: 1.0,
		})
	}
	mustUpsertSymbols(t, store, syms)

	result, err := srv.handleChanges(context.Background(), makeReq(map[string]any{"scope": "all"}))
	if err != nil {
		t.Fatalf("handleChanges: %v", err)
	}
	body := decode(t, result)

	changed, _ := body["changed_symbols"].([]any)
	if len(changed) != changesMaxList {
		t.Errorf("changed_symbols list = %d, want capped at %d", len(changed), changesMaxList)
	}
	// Summary keeps the TRUE total, not the trimmed length.
	summary, _ := body["summary"].(map[string]any)
	if total, _ := summary["changed_symbols"].(float64); int(total) != nSyms {
		t.Errorf("summary.changed_symbols = %v, want %d (true count, not trimmed)", total, nSyms)
	}
	// The trim is surfaced, not silent.
	meta, _ := body["_meta"].(map[string]any)
	warns, _ := meta["warnings"].([]any)
	foundTrimWarning := false
	for _, w := range warns {
		if s, _ := w.(string); strings.Contains(s, "changed_symbols trimmed") {
			foundTrimWarning = true
		}
	}
	if !foundTrimWarning {
		t.Errorf("expected a _meta.warnings entry naming the changed_symbols trim; got %v", warns)
	}
}

// itoaPad zero-pads to 3 digits so caller symbol IDs sort stably and
// the risk-then-id ordering in handleChanges is deterministic.
func itoaPad(n int) string {
	s := ""
	for _, d := range []int{100, 10, 1} {
		s += string(rune('0' + (n/d)%10))
	}
	return s
}
