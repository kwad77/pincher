// SPDX-License-Identifier: MIT

package db

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestCanonicalProjectPath_Exists pins the happy path: an existing
// directory canonicalizes deterministically. On case-insensitive
// filesystems the result is lowercased; on case-sensitive ones the
// result preserves casing. Either way, two invocations against the
// same physical directory MUST return the same string.
func TestCanonicalProjectPath_Exists(t *testing.T) {
	dir := t.TempDir()

	a := CanonicalProjectPath(dir)
	b := CanonicalProjectPath(dir)
	if a != b {
		t.Errorf("CanonicalProjectPath not idempotent: %q vs %q", a, b)
	}
}

// TestCanonicalProjectPath_DoesNotExist asserts a non-existent path
// falls back to the cleaned absolute form rather than erroring or
// returning empty. The fallback preserves pre-fix behaviour for
// callers that pass paths to be created later.
func TestCanonicalProjectPath_DoesNotExist(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-dir")
	got := CanonicalProjectPath(missing)
	if got == "" {
		t.Fatalf("missing path returned empty string")
	}

	// The canonical form may differ from the literal abs path because
	// the parent's symlinks resolve (e.g. /var/folders/... → /private/
	// var/folders/... on macOS) even though the leaf doesn't exist.
	// Build acceptable forms by combining literal/resolved parent with
	// the missing leaf, then accept any of them, optionally lowercased.
	literal, _ := filepath.Abs(missing)
	resolvedParent, err := filepath.EvalSymlinks(filepath.Dir(missing))
	if err != nil {
		// Couldn't resolve parent either; the literal form is the only
		// option.
		resolvedParent = filepath.Dir(literal)
	}
	resolved := filepath.Join(resolvedParent, filepath.Base(missing))

	candidates := []string{literal, resolved, strings.ToLower(literal), strings.ToLower(resolved)}
	for _, c := range candidates {
		if got == c {
			return
		}
	}
	t.Errorf("got %q, want one of %v", got, candidates)
}

// TestCanonicalProjectPath_CaseInsensitiveFolding asserts that on a
// case-insensitive filesystem, two casings of the same physical
// directory canonicalize to the same string. The test self-detects
// whether the FS at t.TempDir() is case-insensitive and only asserts
// the folding when it is — case-sensitive FSes (typical Linux) skip
// the assertion since the flipped-case path doesn't physically exist.
//
// We deliberately avoid asserting on the SHAPE of the canonical form
// (e.g. "is it lowercased?", "does it end with 'mixedcase'?") because
// the t.TempDir() prefix on different OSes contains different
// path-component cases and the lowercasing happens on the WHOLE path,
// not just the leaf. The contract that matters: same physical dir →
// same canonical string.
func TestCanonicalProjectPath_CaseInsensitiveFolding(t *testing.T) {
	parent := t.TempDir()
	mixedDir := filepath.Join(parent, "MixedCase")
	if err := os.Mkdir(mixedDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	flipped, ok := flipFirstLetterCase(mixedDir)
	if !ok {
		t.Skip("path has no flippable letter; cannot probe FS")
	}
	if !isCaseInsensitiveFS(mixedDir) {
		t.Skip("filesystem at t.TempDir() is case-sensitive; folding assertion N/A")
	}

	// The contract: both casings produce the same canonical string.
	canon := CanonicalProjectPath(mixedDir)
	flippedCanon := CanonicalProjectPath(flipped)
	if canon != flippedCanon {
		t.Errorf("case-insensitive FS but canonical paths differ:\n  canon(orig) = %q\n  canon(flipped) = %q",
			canon, flippedCanon)
	}
}

// TestProjectIDFromPath_Idempotent — two invocations against the same
// physical directory MUST return identical IDs, regardless of how the
// caller spelled the path (different casing on case-insensitive FS,
// trailing slash, ./ relative form, etc.).
func TestProjectIDFromPath_Idempotent(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Windows path-string variants are tested separately; trailing
		// backslash / drive-letter casing have OS-specific quirks.
		t.Skip("Windows path-spelling variants tested elsewhere")
	}

	dir := t.TempDir()

	id1 := ProjectIDFromPath(dir)
	id2 := ProjectIDFromPath(dir + "/")
	id3 := ProjectIDFromPath(dir + "/.")

	if id1 != id2 {
		t.Errorf("trailing slash changes ID:\n  %q\n  %q", id1, id2)
	}
	if id1 != id3 {
		t.Errorf("/. suffix changes ID:\n  %q\n  %q", id1, id3)
	}
}

// TestFlipFirstLetterCase exercises the helper directly. The first
// flippable letter wins; case is inverted in place.
func TestFlipFirstLetterCase(t *testing.T) {
	cases := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"/Users/foo", "/users/foo", true}, // 'U' → 'u'
		{"foo", "Foo", true},               // 'f' → 'F'
		{"FOO", "fOO", true},               // 'F' → 'f'
		{"123/456", "123/456", false},      // no letters
		{"", "", false},
		{"/path/to/X", "/Path/to/X", true}, // 'p' (the first letter, in "path") wins
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, ok := flipFirstLetterCase(c.in)
			if ok != c.wantOK {
				t.Errorf("ok = %v, want %v", ok, c.wantOK)
			}
			if c.wantOK && got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// dedupProjectsByCanonicalPath migration
// ─────────────────────────────────────────────────────────────────────────────

// TestDedupProjectsByCanonicalPath_NoDuplicates is the no-op gate: a
// DB with one project per canonical path MUST be unchanged after the
// migration runs.
func TestDedupProjectsByCanonicalPath_NoDuplicates(t *testing.T) {
	s := newTestStore(t)
	dir := t.TempDir()
	canon := ProjectIDFromPath(dir)
	if err := s.UpsertProject(Project{
		ID: canon, Path: dir, Name: "x",
	}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}

	if err := s.dedupProjectsByCanonicalPath(); err != nil {
		t.Fatalf("dedup: %v", err)
	}

	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM projects`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("project count = %d, want 1 (no-op gate)", n)
	}
}

// TestDedupProjectsByCanonicalPath_MergesDuplicates simulates the
// nbarari reproducer: two project rows whose paths canonicalize to the
// same form, each with its own symbols. The migration MUST keep one
// row, fold the symbols together, and drop the duplicate.
//
// Strategy: we use a symlink (works on case-sensitive Linux too) to
// force two distinct path strings that canonicalize identically via
// EvalSymlinks. On case-insensitive FSes, the same shape would arise
// from casing differences without a symlink; we test the symlink path
// because it's the universal case.
func TestDedupProjectsByCanonicalPath_MergesDuplicates(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Windows symlink creation requires elevated privileges or
		// developer mode. Skip rather than depend on environment.
		t.Skip("symlink creation requires admin on Windows")
	}
	s := newTestStore(t)

	parent := t.TempDir()
	real := filepath.Join(parent, "real")
	link := filepath.Join(parent, "link")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatalf("mkdir real: %v", err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	// Both paths canonicalize to the real directory (after symlink
	// resolution + any FS-specific case folding).
	canon := CanonicalProjectPath(real)
	if CanonicalProjectPath(link) != canon {
		t.Fatalf("symlink + EvalSymlinks didn't fold paths together: real→%q link→%q",
			canon, CanonicalProjectPath(link))
	}

	// Insert two project rows with the LITERAL pre-canonical paths (the
	// pre-fix bug shape — different strings, same physical directory).
	for i, id := range []string{real, link} {
		if err := s.UpsertProject(Project{
			ID: id, Path: id, Name: "x",
			FileCount: 1, SymCount: 10 * (i + 1),
		}); err != nil {
			t.Fatalf("UpsertProject %s: %v", id, err)
		}
		// Add a unique symbol per project to verify merge.
		if err := s.BulkUpsertSymbols([]Symbol{{
			ID:        id + "::pkg.Foo#Function",
			ProjectID: id, FilePath: "x.go",
			Name: "Foo", QualifiedName: "pkg.Foo",
			Kind: "Function", Language: "Go",
		}}); err != nil {
			t.Fatalf("BulkUpsertSymbols %s: %v", id, err)
		}
	}

	// Sanity: two project rows + two symbols pre-migration.
	var preProj, preSym int
	s.db.QueryRow(`SELECT COUNT(*) FROM projects`).Scan(&preProj)
	s.db.QueryRow(`SELECT COUNT(*) FROM symbols`).Scan(&preSym)
	if preProj != 2 || preSym != 2 {
		t.Fatalf("pre-migration: projects=%d symbols=%d, want 2/2", preProj, preSym)
	}

	if err := s.dedupProjectsByCanonicalPath(); err != nil {
		t.Fatalf("dedup: %v", err)
	}

	var postProj, postSym int
	s.db.QueryRow(`SELECT COUNT(*) FROM projects`).Scan(&postProj)
	s.db.QueryRow(`SELECT COUNT(*) FROM symbols`).Scan(&postSym)

	if postProj != 1 {
		t.Errorf("post-migration projects = %d, want 1 (duplicates merged)", postProj)
	}
	if postSym != 2 {
		t.Errorf("post-migration symbols = %d, want 2 (both unique IDs preserved)", postSym)
	}

	// Surviving project's id MUST equal the canonical form.
	var survivorID string
	s.db.QueryRow(`SELECT id FROM projects LIMIT 1`).Scan(&survivorID)
	if survivorID != canon {
		t.Errorf("survivor id = %q, want canonical %q", survivorID, canon)
	}

	// Both symbols MUST be reachable from the survivor.
	var orphans int
	s.db.QueryRow(`SELECT COUNT(*) FROM symbols WHERE project_id != ?`, survivorID).Scan(&orphans)
	if orphans != 0 {
		t.Errorf("orphan symbols not re-keyed onto survivor: %d", orphans)
	}

	// projects.sym_count MUST reflect post-merge reality, not the
	// winner's pre-merge value. Without recomputeProjectCounts() this
	// stays at the winner's stored sym_count and `pincher list`
	// displays the wrong number until the next full re-index.
	var storedSymCount, actualSymCount int
	s.db.QueryRow(`SELECT sym_count FROM projects WHERE id = ?`, survivorID).Scan(&storedSymCount)
	s.db.QueryRow(`SELECT COUNT(*) FROM symbols WHERE project_id = ?`, survivorID).Scan(&actualSymCount)
	if storedSymCount != actualSymCount {
		t.Errorf("projects.sym_count = %d, want %d (post-merge actual count); denormalised counts not refreshed by dedup",
			storedSymCount, actualSymCount)
	}
}

// TestDedupProjectsByCanonicalPath_RunsViaMigrate is the wiring gate:
// duplicates inserted into a closed DB MUST be merged when the next
// Open() runs. Catches regressions where dedupProjectsByCanonicalPath
// exists but stops being called from migrate().
func TestDedupProjectsByCanonicalPath_RunsViaMigrate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires admin on Windows")
	}

	dataDir := t.TempDir()

	// Open + seed two project rows whose paths fold to the same canonical.
	s, err := Open(dataDir)
	if err != nil {
		t.Fatalf("Open 1: %v", err)
	}
	parent := t.TempDir()
	realDir := filepath.Join(parent, "real")
	linkDir := filepath.Join(parent, "link")
	if err := os.Mkdir(realDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	for _, id := range []string{realDir, linkDir} {
		if err := s.UpsertProject(Project{ID: id, Path: id, Name: "x"}); err != nil {
			t.Fatalf("UpsertProject %s: %v", id, err)
		}
	}
	s.Close()

	// Reopen — migrate() should run the dedup as part of Step 4.5.
	s, err = Open(dataDir)
	if err != nil {
		t.Fatalf("Open 2: %v", err)
	}
	defer s.Close()

	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM projects`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("post-Open() projects = %d, want 1 (migrate() didn't call dedup)", n)
	}
}

// TestRecomputeProjectCounts is a direct unit test for the helper used
// by dedupProjectsByCanonicalPath. We exercise it independently of the
// dedup wrapper so the function gets coverage even on platforms where
// the dedup test itself skips (Windows — symlink creation requires
// admin). Catches regressions in the SQL or the column wiring.
func TestRecomputeProjectCounts(t *testing.T) {
	s := newTestStore(t)
	const projectID = "/test/proj"

	if err := s.UpsertProject(Project{
		ID: projectID, Path: projectID, Name: "x",
		// Stale/wrong values — recompute should overwrite.
		FileCount: 999, SymCount: 999,
	}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}

	// 3 symbols across 2 files. Sym count should land at 3, file count at 2.
	if err := s.BulkUpsertSymbols([]Symbol{
		{ID: "a", ProjectID: projectID, FilePath: "a.go", Name: "A", QualifiedName: "p.A", Kind: "Function", Language: "Go"},
		{ID: "b", ProjectID: projectID, FilePath: "a.go", Name: "B", QualifiedName: "p.B", Kind: "Function", Language: "Go"},
		{ID: "c", ProjectID: projectID, FilePath: "b.go", Name: "C", QualifiedName: "p.C", Kind: "Function", Language: "Go"},
	}); err != nil {
		t.Fatalf("BulkUpsertSymbols: %v", err)
	}

	if err := s.recomputeProjectCounts(projectID); err != nil {
		t.Fatalf("recomputeProjectCounts: %v", err)
	}

	var symCount, fileCount, edgeCount int
	if err := s.db.QueryRow(
		`SELECT sym_count, file_count, edge_count FROM projects WHERE id = ?`,
		projectID,
	).Scan(&symCount, &fileCount, &edgeCount); err != nil {
		t.Fatalf("scan post-recompute: %v", err)
	}
	if symCount != 3 {
		t.Errorf("sym_count = %d, want 3", symCount)
	}
	// file_count is computed from the files table (not symbols.file_path),
	// which BulkUpsertSymbols doesn't populate. So files-table count is 0.
	if fileCount != 0 {
		t.Errorf("file_count = %d, want 0 (no rows in files table)", fileCount)
	}
	if edgeCount != 0 {
		t.Errorf("edge_count = %d, want 0 (no edges)", edgeCount)
	}
}

// TestJoinEqClauses_BuildsCorrectSQL pins the helper that builds
// the conflict-detection subquery's equality joins. Pure-string
// helper, runs on every platform.
func TestJoinEqClauses_BuildsCorrectSQL(t *testing.T) {
	cases := []struct {
		cols, table, want string
	}{
		{"id", "symbols", "w.id = symbols.id"},
		{"from_id, to_id, kind", "edges",
			"w.from_id = edges.from_id AND w.to_id = edges.to_id AND w.kind = edges.kind"},
		{"path", "files", "w.path = files.path"},
	}
	for _, c := range cases {
		got := joinEqClauses(c.cols, c.table)
		if got != c.want {
			t.Errorf("joinEqClauses(%q, %q) = %q, want %q", c.cols, c.table, got, c.want)
		}
	}
}

// TestMergeProjectInto_DirectMerge is a platform-independent direct test
// for the merge primitive. Inserts symbols on a "loser" project that don't
// conflict with the winner, then asserts they're re-keyed onto the winner
// after merge. Distinct from the symlink-driven dedup integration test:
// that one is the wiring test, this one is the unit test.
func TestMergeProjectInto_DirectMerge(t *testing.T) {
	s := newTestStore(t)
	const winner, loser = "/winner", "/loser"

	for _, id := range []string{winner, loser} {
		if err := s.UpsertProject(Project{ID: id, Path: id, Name: "x"}); err != nil {
			t.Fatalf("UpsertProject %s: %v", id, err)
		}
	}
	// One symbol per project, distinct IDs (no conflict).
	if err := s.BulkUpsertSymbols([]Symbol{
		{ID: "w-sym", ProjectID: winner, FilePath: "w.go", Name: "W", QualifiedName: "p.W", Kind: "Function", Language: "Go"},
		{ID: "l-sym", ProjectID: loser, FilePath: "l.go", Name: "L", QualifiedName: "p.L", Kind: "Function", Language: "Go"},
	}); err != nil {
		t.Fatalf("BulkUpsertSymbols: %v", err)
	}

	if err := s.mergeProjectInto(loser, winner); err != nil {
		t.Fatalf("mergeProjectInto: %v", err)
	}

	// Loser project row gone.
	var loserRows int
	s.db.QueryRow(`SELECT COUNT(*) FROM projects WHERE id = ?`, loser).Scan(&loserRows)
	if loserRows != 0 {
		t.Errorf("loser project row not deleted: %d remain", loserRows)
	}
	// Both symbols now keyed to winner.
	var winnerSyms int
	s.db.QueryRow(`SELECT COUNT(*) FROM symbols WHERE project_id = ?`, winner).Scan(&winnerSyms)
	if winnerSyms != 2 {
		t.Errorf("winner symbols = %d, want 2 (both moved)", winnerSyms)
	}
}

// TestMergeProjectInto_ConflictPath exercises the lossy path: when a
// loser symbol shares an ID with a winner symbol, the loser row gets
// dropped (not duplicated). Documents the recoverable-by-re-indexing
// trade-off.
func TestMergeProjectInto_ConflictPath(t *testing.T) {
	s := newTestStore(t)
	const winner, loser = "/winner", "/loser"

	for _, id := range []string{winner, loser} {
		if err := s.UpsertProject(Project{ID: id, Path: id, Name: "x"}); err != nil {
			t.Fatalf("UpsertProject %s: %v", id, err)
		}
	}
	// Both projects own a symbol with ID "shared" — winner wins, loser drops.
	// Plus a unique symbol on the loser that should successfully move.
	if err := s.BulkUpsertSymbols([]Symbol{
		{ID: "shared", ProjectID: winner, FilePath: "w.go", Name: "Shared", QualifiedName: "p.Shared", Kind: "Function", Language: "Go"},
		{ID: "shared", ProjectID: loser, FilePath: "w.go", Name: "Shared", QualifiedName: "p.Shared", Kind: "Function", Language: "Go"},
		{ID: "uniq", ProjectID: loser, FilePath: "u.go", Name: "Uniq", QualifiedName: "p.Uniq", Kind: "Function", Language: "Go"},
	}); err != nil {
		t.Fatalf("BulkUpsertSymbols: %v", err)
	}

	if err := s.mergeProjectInto(loser, winner); err != nil {
		t.Fatalf("mergeProjectInto: %v", err)
	}

	// Winner now has 2 symbols: original "shared" + moved "uniq".
	var winnerSyms int
	s.db.QueryRow(`SELECT COUNT(*) FROM symbols WHERE project_id = ?`, winner).Scan(&winnerSyms)
	if winnerSyms != 2 {
		t.Errorf("winner symbols = %d, want 2 (shared kept, uniq moved)", winnerSyms)
	}
	// No symbols left on loser.
	var loserSyms int
	s.db.QueryRow(`SELECT COUNT(*) FROM symbols WHERE project_id = ?`, loser).Scan(&loserSyms)
	if loserSyms != 0 {
		t.Errorf("loser symbols = %d, want 0", loserSyms)
	}
}

// TestRenameProjectID exercises the rename primitive directly.
func TestRenameProjectID(t *testing.T) {
	s := newTestStore(t)
	const oldID, newID = "/old/path", "/new/path"

	if err := s.UpsertProject(Project{ID: oldID, Path: oldID, Name: "x"}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	if err := s.BulkUpsertSymbols([]Symbol{
		{ID: "s1", ProjectID: oldID, FilePath: "f.go", Name: "F", QualifiedName: "p.F", Kind: "Function", Language: "Go"},
	}); err != nil {
		t.Fatalf("BulkUpsertSymbols: %v", err)
	}

	if err := s.renameProjectID(oldID, newID); err != nil {
		t.Fatalf("renameProjectID: %v", err)
	}

	// Project row keyed to newID.
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM projects WHERE id = ?`, newID).Scan(&n)
	if n != 1 {
		t.Errorf("project at newID = %d, want 1", n)
	}
	s.db.QueryRow(`SELECT COUNT(*) FROM projects WHERE id = ?`, oldID).Scan(&n)
	if n != 0 {
		t.Errorf("project at oldID = %d, want 0", n)
	}
	// Symbol re-keyed.
	s.db.QueryRow(`SELECT COUNT(*) FROM symbols WHERE project_id = ?`, newID).Scan(&n)
	if n != 1 {
		t.Errorf("symbols at newID = %d, want 1", n)
	}
}

// TestDedupProjectsByCanonicalPath_FallbackWinnerPick exercises the
// "no row already at canonical form, pick by sym_count + age" branch.
// Both projects use the SAME real path string but distinct IDs (the
// pre-fix pattern where path-based ProjectIDFromPath produced two
// stored IDs neither equal to the canonical form). Works on every
// platform — no symlink needed.
func TestDedupProjectsByCanonicalPath_FallbackWinnerPick(t *testing.T) {
	s := newTestStore(t)

	// One real existing dir; both project rows reference it.
	dir := t.TempDir()
	canon := CanonicalProjectPath(dir)

	// Insert two distinct IDs that BOTH canonicalize to `canon` but
	// neither equals `canon` (we synthesise non-canonical IDs by
	// appending a tag so they group on canonical(path) but neither
	// trips the "winner.id == canon" fast-path).
	idA := dir + "#a"
	idB := dir + "#b"

	for i, id := range []string{idA, idB} {
		if err := s.UpsertProject(Project{
			ID: id, Path: dir, Name: "x",
			SymCount: 10 * (i + 1), // idB is winner by sym_count
		}); err != nil {
			t.Fatalf("UpsertProject %s: %v", id, err)
		}
		if err := s.BulkUpsertSymbols([]Symbol{{
			ID:        id + "::pkg.S#Function",
			ProjectID: id, FilePath: "f.go",
			Name: "S", QualifiedName: "pkg.S",
			Kind: "Function", Language: "Go",
		}}); err != nil {
			t.Fatalf("BulkUpsertSymbols %s: %v", id, err)
		}
	}

	if err := s.dedupProjectsByCanonicalPath(); err != nil {
		t.Fatalf("dedup: %v", err)
	}

	// One project row remains, keyed at the canonical form (rename path
	// fired because neither stored ID matched canonical).
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM projects`).Scan(&n)
	if n != 1 {
		t.Errorf("post-dedup projects = %d, want 1", n)
	}
	var survivorID string
	s.db.QueryRow(`SELECT id FROM projects`).Scan(&survivorID)
	if survivorID != canon {
		t.Errorf("survivor id = %q, want %q (canonical, after rename path)", survivorID, canon)
	}
}
