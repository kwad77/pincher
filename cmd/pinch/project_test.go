package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// ── matchProject ─────────────────────────────────────────────────────────────

func projectsFixture() []db.Project {
	return []db.Project{
		{ID: "abc123", Name: "pincher", Path: "/home/dev/pincher"},
		{ID: "def456", Name: "pincher-fork", Path: "/home/dev/pincher-fork"},
		{ID: "ghi789", Name: "other", Path: "/home/dev/other-project"},
	}
}

func TestMatchProject_ExactID(t *testing.T) {
	hits, status := matchProject(projectsFixture(), "abc123")
	if status != matchExact {
		t.Fatalf("status=%v, want matchExact", status)
	}
	if len(hits) != 1 || hits[0].Name != "pincher" {
		t.Errorf("hits=%v, want exactly [pincher]", hits)
	}
}

func TestMatchProject_ExactNameCaseInsensitive(t *testing.T) {
	hits, status := matchProject(projectsFixture(), "OTHER")
	if status != matchExact {
		t.Fatalf("status=%v, want matchExact", status)
	}
	if len(hits) != 1 || hits[0].ID != "ghi789" {
		t.Errorf("hits=%v, want [other]", hits)
	}
}

func TestMatchProject_AmbiguousSubstring(t *testing.T) {
	// "pinc" is a substring of both "pincher" and "pincher-fork" but
	// matches neither name exactly, so matching falls through to
	// substring-on-name and produces an ambiguity. (When the input IS
	// an exact name like "pincher", exact-match wins over substring —
	// that's covered by TestMatchProject_ExactNameCaseInsensitive's
	// implicit invariant.)
	hits, status := matchProject(projectsFixture(), "pinc")
	if status != matchAmbiguous {
		t.Fatalf("status=%v, want matchAmbiguous (matches both pincher* projects)", status)
	}
	if len(hits) != 2 {
		t.Errorf("hits=%v, want 2 matches", hits)
	}
}

func TestMatchProject_ExactNameWinsOverSubstring(t *testing.T) {
	// "pincher" is the exact name of one project but also a prefix
	// of "pincher-fork". Exact name match should resolve to the one
	// project, not bail with ambiguity.
	hits, status := matchProject(projectsFixture(), "pincher")
	if status != matchExact {
		t.Fatalf("status=%v, want matchExact (exact name beats substring)", status)
	}
	if len(hits) != 1 || hits[0].Name != "pincher" {
		t.Errorf("hits=%v, want exactly [pincher]", hits)
	}
}

func TestMatchProject_UniqueSubstringOnPath(t *testing.T) {
	hits, status := matchProject(projectsFixture(), "other-project")
	if status != matchExact {
		t.Fatalf("status=%v, want matchExact (path substring)", status)
	}
	if len(hits) != 1 || hits[0].Name != "other" {
		t.Errorf("hits=%v, want [other]", hits)
	}
}

func TestMatchProject_NoMatch(t *testing.T) {
	hits, status := matchProject(projectsFixture(), "nonexistent")
	if status != matchNone {
		t.Errorf("status=%v, want matchNone", status)
	}
	if len(hits) != 0 {
		t.Errorf("expected no hits, got %v", hits)
	}
}

func TestMatchProject_EmptyTarget(t *testing.T) {
	_, status := matchProject(projectsFixture(), "")
	if status != matchNone {
		t.Errorf("empty target: status=%v, want matchNone", status)
	}
}

func TestOpenStoreReadOnlyOrCreate_MigratesStaleSchema(t *testing.T) {
	if db.CurrentSchemaVersion() < 36 {
		t.Skip("v36 project index-state migration not present")
	}
	dir := t.TempDir()
	store, err := db.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := store.DB().Exec(`DROP TABLE projects`); err != nil {
		t.Fatalf("drop projects: %v", err)
	}
	if _, err := store.DB().Exec(`
		CREATE TABLE projects (
			id TEXT PRIMARY KEY,
			path TEXT NOT NULL,
			name TEXT NOT NULL,
			indexed_at INTEGER,
			file_count INTEGER DEFAULT 0,
			sym_count INTEGER DEFAULT 0,
			edge_count INTEGER DEFAULT 0,
			schema_version_at_index INTEGER,
			binary_version TEXT NOT NULL DEFAULT '',
			current_branch TEXT NOT NULL DEFAULT ''
		)`); err != nil {
		t.Fatalf("create stale projects: %v", err)
	}
	if _, err := store.DB().Exec(`
		INSERT INTO projects(id, path, name, indexed_at, file_count, sym_count, edge_count, schema_version_at_index, binary_version, current_branch)
		VALUES('stale', '/tmp/stale', 'stale', 1, 2, 3, 4, 35, '0.95.0', 'master')`); err != nil {
		t.Fatalf("insert stale project: %v", err)
	}
	if _, err := store.DB().Exec(`UPDATE schema_version SET version = 35`); err != nil {
		t.Fatalf("downgrade schema_version: %v", err)
	}
	store.Close()

	reopened, _, err := openStoreReadOnlyOrCreate(dir)
	if err != nil {
		t.Fatalf("openStoreReadOnlyOrCreate: %v", err)
	}
	defer reopened.Close()
	if got := dbSchemaVersion(reopened); got != db.CurrentSchemaVersion() {
		t.Fatalf("schema_version = %d, want %d", got, db.CurrentSchemaVersion())
	}
	projects, err := reopened.ListProjects()
	if err != nil {
		t.Fatalf("ListProjects after migration: %v", err)
	}
	if len(projects) != 1 || projects[0].ID != "stale" {
		t.Fatalf("projects after migration = %+v, want stale project", projects)
	}
	if projects[0].IndexState != "complete" {
		t.Fatalf("IndexState = %q, want complete default from v36 migration", projects[0].IndexState)
	}
}

// ── confirmYesFrom ───────────────────────────────────────────────────────────

func TestConfirmYesFrom(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"y\n", true},
		{"Y\n", true},
		{"yes\n", true},
		{"n\n", false},
		{"N\n", false},
		{"\n", false},
		{"", false},
		{"maybe\n", false},
	}
	for _, c := range cases {
		got := confirmYesFrom(strings.NewReader(c.input))
		if got != c.want {
			t.Errorf("confirmYesFrom(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}

// ── formatProjectList ────────────────────────────────────────────────────────

func TestFormatProjectList_Empty(t *testing.T) {
	got := formatProjectList(nil)
	if !strings.Contains(got, "No projects indexed.") {
		t.Errorf("empty list output should mention 'No projects indexed.', got: %s", got)
	}
}

func TestFormatProjectList_RendersTable(t *testing.T) {
	got := formatProjectList(projectsFixture())
	for _, want := range []string{"PROJECT", "FILES", "SYMBOLS", "EDGES", "PATH", "pincher", "other", "3 project(s)"} {
		if !strings.Contains(got, want) {
			t.Errorf("table output missing %q in:\n%s", want, got)
		}
	}
}

// TestStaleness_FreshNonNil covers the happy path: a project indexed
// at the current schema version isn't stale.
func TestStaleness_FreshNonNil(t *testing.T) {
	current := db.CurrentSchemaVersion()
	at := current
	stale, reason := staleness(&at, current)
	if stale {
		t.Errorf("stale=true for current version (%d); reason=%q", current, reason)
	}
}

// TestStaleness_OlderNonNil covers a project indexed at an older
// schema — must report stale with the precise version pinpoint.
func TestStaleness_OlderNonNil(t *testing.T) {
	at := 12
	stale, reason := staleness(&at, 15)
	if !stale {
		t.Errorf("stale=false for v12 against current v15")
	}
	if !strings.Contains(reason, "v12") || !strings.Contains(reason, "v15") {
		t.Errorf("reason should name both versions; got %q", reason)
	}
}

// TestStaleness_NilIsStale covers the pre-v15 row case: NULL means
// the row pre-dates the column itself, treated as stale (unknown)
// because we can't know how much older.
func TestStaleness_NilIsStale(t *testing.T) {
	stale, reason := staleness(nil, 15)
	if !stale {
		t.Errorf("stale=false for nil")
	}
	if !strings.Contains(reason, "predates") {
		t.Errorf("reason should mention 'predates'; got %q", reason)
	}
}

// TestFormatProjectList_StaleMarker covers the user-visible surface:
// a stale project gets a `[stale]` suffix appended to its name in the
// rendered table.
func TestFormatProjectList_StaleMarker(t *testing.T) {
	current := db.CurrentSchemaVersion()
	older := current - 1
	projects := []db.Project{
		{ID: "fresh", Name: "fresh-proj", Path: "/p1", SchemaVersionAtIndex: &current},
		{ID: "old", Name: "old-proj", Path: "/p2", SchemaVersionAtIndex: &older},
		{ID: "unk", Name: "unk-proj", Path: "/p3"}, // nil → pre-v15
	}
	got := formatProjectList(projects)
	if !strings.Contains(got, "old-proj [stale]") {
		t.Errorf("expected `old-proj [stale]` in output; got:\n%s", got)
	}
	if !strings.Contains(got, "unk-proj [stale]") {
		t.Errorf("expected `unk-proj [stale]` (nil/pre-v15); got:\n%s", got)
	}
	if strings.Contains(got, "fresh-proj [stale]") {
		t.Errorf("did not expect `fresh-proj [stale]`; got:\n%s", got)
	}
	if !strings.Contains(got, "2 stale") {
		t.Errorf("footer should report stale count; got:\n%s", got)
	}
}

// ── prunableStale ────────────────────────────────────────────────────────────

// prunableStale requires BOTH conditions: schema-stale AND idle past the
// cutoff. Each test case isolates one axis so a future change that
// collapses the AND into an OR is caught.
func TestPrunableStale(t *testing.T) {
	current := db.CurrentSchemaVersion()
	older := current - 1
	cutoff := time.Now().AddDate(0, 0, -30)
	recent := time.Now().AddDate(0, 0, -1) // touched yesterday
	ancient := time.Now().AddDate(0, 0, -90)

	cases := []struct {
		name string
		p    db.Project
		want bool
	}{
		{"stale schema + idle → prunable",
			db.Project{SchemaVersionAtIndex: &older, IndexedAt: ancient}, true},
		{"nil schema (pre-v15) + idle → prunable",
			db.Project{SchemaVersionAtIndex: nil, IndexedAt: ancient}, true},
		{"stale schema but touched recently → keep",
			db.Project{SchemaVersionAtIndex: &older, IndexedAt: recent}, false},
		{"fresh schema even if idle → keep (just needs no action)",
			db.Project{SchemaVersionAtIndex: &current, IndexedAt: ancient}, false},
		{"fresh schema + recent → keep",
			db.Project{SchemaVersionAtIndex: &current, IndexedAt: recent}, false},
	}
	for _, c := range cases {
		if got := prunableStale(c.p, current, cutoff); got != c.want {
			t.Errorf("%s: prunableStale = %v, want %v", c.name, got, c.want)
		}
	}
}

// seedStaleProject writes a project row and then raw-SQL-downgrades its
// schema_version_at_index and back-dates indexed_at so it satisfies
// prunableStale. UpsertProject always stamps the *current* schema, so a
// stale row can only be constructed by a direct UPDATE — which is fine
// for a test fixture (it mirrors exactly the real-world state #732
// describes: a project indexed by an old binary, never touched since).
func seedStaleProject(t *testing.T, dataDir, name, path string, schemaAt int, indexedAt time.Time) string {
	t.Helper()
	id := seedProject(t, dataDir, name, path)
	store, err := db.Open(dataDir)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()
	if _, err := store.DB().Exec(
		`UPDATE projects SET schema_version_at_index=?, indexed_at=? WHERE id=?`,
		schemaAt, indexedAt.Unix(), id); err != nil {
		t.Fatalf("downgrade project: %v", err)
	}
	return id
}

func TestProjectCLI_PruneStale_RemovesStaleKeepsFresh(t *testing.T) {
	exe := buildPincherBinary(t)
	dataDir := t.TempDir()
	current := db.CurrentSchemaVersion()

	stalePath := filepath.Join(t.TempDir(), "stale")
	freshPath := filepath.Join(t.TempDir(), "fresh")
	for _, p := range []string{stalePath, freshPath} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	staleID := seedStaleProject(t, dataDir, "stale-proj", stalePath, current-1, time.Now().AddDate(0, 0, -90))
	freshID := seedProject(t, dataDir, "fresh-proj", freshPath) // current schema, indexed now

	cmd := exec.Command(exe, "project", "prune-stale", "--force", "--json", "--data-dir", dataDir)
	cmd.Env = pincherCoverEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("prune-stale failed: %v\n%s", err, out)
	}
	var receipt map[string]any
	if err := json.Unmarshal(out, &receipt); err != nil {
		t.Fatalf("output not valid JSON: %v\n%s", err, out)
	}
	if cnt, _ := receipt["count"].(float64); cnt != 1 {
		t.Errorf("count=%v, want 1", receipt["count"])
	}

	store, _ := db.Open(dataDir)
	defer store.Close()
	if got, _ := store.GetProject(staleID); got != nil {
		t.Errorf("stale project still exists after prune-stale")
	}
	if got, _ := store.GetProject(freshID); got == nil {
		t.Errorf("fresh project was pruned — should have been kept")
	}
}

func TestProjectCLI_PruneStale_RecentStaleKept(t *testing.T) {
	exe := buildPincherBinary(t)
	dataDir := t.TempDir()
	current := db.CurrentSchemaVersion()

	p := filepath.Join(t.TempDir(), "recent-stale")
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	// Schema-stale, but indexed yesterday — must NOT be pruned at the
	// default 30-day cutoff (the user is clearly still using it).
	id := seedStaleProject(t, dataDir, "recent-stale", p, current-1, time.Now().AddDate(0, 0, -1))

	cmd := exec.Command(exe, "project", "prune-stale", "--force", "--json", "--data-dir", dataDir)
	cmd.Env = pincherCoverEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("prune-stale failed: %v\n%s", err, out)
	}
	var receipt map[string]any
	if err := json.Unmarshal(out, &receipt); err != nil {
		t.Fatalf("output not valid JSON: %v\n%s", err, out)
	}
	if cnt, _ := receipt["count"].(float64); cnt != 0 {
		t.Errorf("count=%v, want 0 (recently-indexed stale project must be kept)", receipt["count"])
	}

	store, _ := db.Open(dataDir)
	defer store.Close()
	if got, _ := store.GetProject(id); got == nil {
		t.Errorf("recently-indexed stale project was pruned — should have been kept")
	}
}

func TestProjectCLI_PruneStale_JSONRequiresForce(t *testing.T) {
	exe := buildPincherBinary(t)
	dataDir := t.TempDir()
	cmd := exec.Command(exe, "project", "prune-stale", "--json", "--data-dir", dataDir)
	cmd.Env = pincherCoverEnv()
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit for --json without --force; got: %s", out)
	}
	if !strings.Contains(string(out), "--json requires --force") {
		t.Errorf("expected '--json requires --force' message, got: %s", out)
	}
}

func TestProjectCLI_PruneStale_NoCandidates(t *testing.T) {
	exe := buildPincherBinary(t)
	dataDir := t.TempDir()
	cmd := exec.Command(exe, "project", "prune-stale", "--data-dir", dataDir)
	cmd.Env = pincherCoverEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("prune-stale on empty DB should succeed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "No stale projects to prune") {
		t.Errorf("expected 'No stale projects to prune', got: %s", out)
	}
}

func TestProjectCLI_PruneDead_RemovesMissingKeepsLive(t *testing.T) {
	exe := buildPincherBinary(t)
	dataDir := t.TempDir()

	livePath := filepath.Join(t.TempDir(), "live")
	if err := os.MkdirAll(livePath, 0o755); err != nil {
		t.Fatal(err)
	}
	deadPath := filepath.Join(t.TempDir(), "missing")

	liveID := seedProject(t, dataDir, "live-proj", livePath)
	deadID := seedProject(t, dataDir, "dead-proj", deadPath)

	cmd := exec.Command(exe, "project", "prune-dead", "--force", "--json", "--data-dir", dataDir)
	cmd.Env = pincherCoverEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("prune-dead failed: %v\n%s", err, out)
	}
	var receipt map[string]any
	if err := json.Unmarshal(out, &receipt); err != nil {
		t.Fatalf("output not valid JSON: %v\n%s", err, out)
	}
	if cnt, _ := receipt["count"].(float64); cnt != 1 {
		t.Errorf("count=%v, want 1", receipt["count"])
	}

	store, _ := db.Open(dataDir)
	defer store.Close()
	if got, _ := store.GetProject(deadID); got != nil {
		t.Errorf("dead project still exists after prune-dead")
	}
	if got, _ := store.GetProject(liveID); got == nil {
		t.Errorf("live project was pruned")
	}
}

func TestProjectCLI_PruneDead_JSONRequiresForce(t *testing.T) {
	exe := buildPincherBinary(t)
	dataDir := t.TempDir()
	cmd := exec.Command(exe, "project", "prune-dead", "--json", "--data-dir", dataDir)
	cmd.Env = pincherCoverEnv()
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit for --json without --force; got: %s", out)
	}
	if !strings.Contains(string(out), "--json requires --force") {
		t.Errorf("expected '--json requires --force' message, got: %s", out)
	}
}

func TestProjectCLI_PruneDead_NoCandidates(t *testing.T) {
	exe := buildPincherBinary(t)
	dataDir := t.TempDir()
	livePath := filepath.Join(t.TempDir(), "live")
	if err := os.MkdirAll(livePath, 0o755); err != nil {
		t.Fatal(err)
	}
	seedProject(t, dataDir, "live-proj", livePath)

	cmd := exec.Command(exe, "project", "prune-dead", "--force", "--json", "--data-dir", dataDir)
	cmd.Env = pincherCoverEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("prune-dead with no candidates should succeed: %v\n%s", err, out)
	}
	var receipt map[string]any
	if err := json.Unmarshal(out, &receipt); err != nil {
		t.Fatalf("output not valid JSON: %v\n%s", err, out)
	}
	if cnt, _ := receipt["count"].(float64); cnt != 0 {
		t.Errorf("count=%v, want 0", receipt["count"])
	}
}

func TestProjectCLI_PruneDead_NoCandidatesText(t *testing.T) {
	exe := buildPincherBinary(t)
	dataDir := t.TempDir()

	cmd := exec.Command(exe, "project", "prune-dead", "--data-dir", dataDir)
	cmd.Env = pincherCoverEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("prune-dead with no candidates should succeed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "No dead-path projects to prune") {
		t.Errorf("expected no-candidates text, got: %s", out)
	}
}

func TestProjectCLI_PruneDead_ConfirmRejects(t *testing.T) {
	exe := buildPincherBinary(t)
	dataDir := t.TempDir()
	deadPath := filepath.Join(t.TempDir(), "missing")
	deadID := seedProject(t, dataDir, "dead-proj", deadPath)

	cmd := exec.Command(exe, "project", "prune-dead", "--data-dir", dataDir)
	cmd.Env = pincherCoverEnv()
	cmd.Stdin = strings.NewReader("n\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("prune-dead reject should exit cleanly: %v\n%s", err, out)
	}
	got := string(out)
	if !strings.Contains(got, "eligible for prune") || !strings.Contains(got, "Aborted") {
		t.Errorf("expected prompt and abort text, got: %s", out)
	}

	store, _ := db.Open(dataDir)
	defer store.Close()
	if p, _ := store.GetProject(deadID); p == nil {
		t.Errorf("dead project was removed despite rejected confirmation")
	}
}

func TestProjectCLI_PruneDead_ConfirmAccepts(t *testing.T) {
	exe := buildPincherBinary(t)
	dataDir := t.TempDir()
	deadPath := filepath.Join(t.TempDir(), "missing")
	deadID := seedProject(t, dataDir, "dead-proj", deadPath)

	cmd := exec.Command(exe, "project", "prune-dead", "--data-dir", dataDir)
	cmd.Env = pincherCoverEnv()
	cmd.Stdin = strings.NewReader("y\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("prune-dead accept should exit cleanly: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "Removed 1 dead-path project") {
		t.Errorf("expected removal text, got: %s", out)
	}

	store, _ := db.Open(dataDir)
	defer store.Close()
	if p, _ := store.GetProject(deadID); p != nil {
		t.Errorf("dead project still exists after accepted prune-dead")
	}
}

// ── vacuum ───────────────────────────────────────────────────────────────────

func TestVacuumCLI_EmitsJSONReceipt(t *testing.T) {
	exe := buildPincherBinary(t)
	dataDir := t.TempDir()
	p := filepath.Join(t.TempDir(), "v")
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	seedProject(t, dataDir, "v", p)

	cmd := exec.Command(exe, "vacuum", "--json", "--data-dir", dataDir)
	cmd.Env = pincherCoverEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("vacuum failed: %v\n%s", err, out)
	}
	var receipt map[string]any
	if err := json.Unmarshal(out, &receipt); err != nil {
		t.Fatalf("output not valid JSON: %v\n%s", err, out)
	}
	if vacuumed, _ := receipt["vacuumed"].(bool); !vacuumed {
		t.Errorf("vacuumed=%v, want true", receipt["vacuumed"])
	}
	for _, k := range []string{"bytes_before", "bytes_after", "bytes_reclaimed"} {
		if _, ok := receipt[k]; !ok {
			t.Errorf("receipt missing %q", k)
		}
	}
}

// ── end-to-end via test binary ───────────────────────────────────────────────

// (project tests use buildPincherBinary from coverbuild_test.go so
// integration-style runs contribute to the merged coverage profile —
// see comment there for the full #185 rationale.)

func TestProjectCLI_NoArgsShowsUsage(t *testing.T) {
	exe := buildPincherBinary(t)
	cmd := exec.Command(exe, "project")
	cmd.Env = pincherCoverEnv()
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected non-zero exit when no verb given")
	}
	if !strings.Contains(string(out), "usage: pincher project") {
		t.Errorf("expected usage banner, got: %s", out)
	}
}

func TestProjectCLI_UnknownVerbExits2(t *testing.T) {
	exe := buildPincherBinary(t)
	cmd := exec.Command(exe, "project", "frobnicate")
	cmd.Env = pincherCoverEnv()
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected non-zero exit for unknown verb")
	}
	if !strings.Contains(string(out), "unknown verb") {
		t.Errorf("expected 'unknown verb' message, got: %s", out)
	}
}

// #801: `project rm --json` printed plain text on the no-match /
// ambiguous / store-error paths, so a scripted caller got unparseable
// output on exactly the cases it most needs to branch on. Every error
// path must emit a JSON error object under --json.
func TestProjectCLI_RmJSONError_NoMatch(t *testing.T) {
	exe := buildPincherBinary(t)
	dataDir := t.TempDir()
	cmd := exec.Command(exe, "project", "rm", "no-such-project-xyz", "--json", "--force", "--data-dir", dataDir)
	cmd.Env = pincherCoverEnv()
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit for no-match; got success\n%s", out)
	}
	var obj map[string]any
	if jsonErr := json.Unmarshal(out, &obj); jsonErr != nil {
		t.Fatalf("--json error output is not valid JSON: %v\n%s", jsonErr, out)
	}
	if obj["removed"] != false {
		t.Errorf("expected removed=false, got %v", obj["removed"])
	}
	if s, _ := obj["error"].(string); !strings.Contains(s, "no project matches") {
		t.Errorf("expected a no-match error string, got %q", s)
	}
}

func TestProjectCLI_ListEmptyJSON(t *testing.T) {
	exe := buildPincherBinary(t)
	dataDir := t.TempDir()
	cmd := exec.Command(exe, "project", "list", "--json", "--data-dir", dataDir)
	cmd.Env = pincherCoverEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("list --json on empty DB failed: %v\n%s", err, out)
	}
	var report projectListReport
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if report.Count != 0 {
		t.Errorf("count=%d, want 0", report.Count)
	}
}

func TestProjectCLI_ListEmptyText(t *testing.T) {
	exe := buildPincherBinary(t)
	dataDir := t.TempDir()
	cmd := exec.Command(exe, "project", "list", "--data-dir", dataDir)
	cmd.Env = pincherCoverEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("list on empty DB failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "No projects indexed") {
		t.Errorf("expected 'No projects indexed' in empty list, got: %s", out)
	}
}

// ── rm via real DB ───────────────────────────────────────────────────────────

// seedProject opens a real DB at dataDir and writes one project row.
// Returns the project ID for later assertions.
func seedProject(t *testing.T, dataDir, name, path string) string {
	t.Helper()
	store, err := db.Open(dataDir)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	id := db.ProjectIDFromPath(path)
	p := db.Project{ID: id, Path: path, Name: name, FileCount: 1, SymCount: 5, EdgeCount: 3}
	if err := store.UpsertProject(p); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	return id
}

func TestProjectCLI_RmForceJSON(t *testing.T) {
	exe := buildPincherBinary(t)
	dataDir := t.TempDir()
	projectPath := filepath.Join(t.TempDir(), "myproj")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatal(err)
	}
	id := seedProject(t, dataDir, "myproj", projectPath)

	cmd := exec.Command(exe, "project", "rm", "--force", "--json", "--data-dir", dataDir, "myproj")
	cmd.Env = pincherCoverEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rm failed: %v\n%s", err, out)
	}
	var receipt map[string]any
	if err := json.Unmarshal(out, &receipt); err != nil {
		t.Fatalf("output not valid JSON: %v\n%s", err, out)
	}
	if removed, _ := receipt["removed"].(bool); !removed {
		t.Errorf("removed=%v, want true", receipt["removed"])
	}
	if got, _ := receipt["id"].(string); got != id {
		t.Errorf("receipt id=%q, want %q", got, id)
	}

	// Verify the project is gone from the DB.
	store, err := db.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	got, _ := store.GetProject(id)
	if got != nil {
		t.Errorf("project %q still exists after rm", id)
	}
}

func TestProjectCLI_RmNoMatchExitsNonZero(t *testing.T) {
	exe := buildPincherBinary(t)
	dataDir := t.TempDir()
	cmd := exec.Command(exe, "project", "rm", "--force", "--data-dir", dataDir, "nope")
	cmd.Env = pincherCoverEnv()
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("rm on empty DB should error; got output: %s", out)
	}
	if !strings.Contains(string(out), "no project matches") {
		t.Errorf("expected 'no project matches' in stderr, got: %s", out)
	}
}

func TestProjectCLI_RmAmbiguousErrorsWithList(t *testing.T) {
	exe := buildPincherBinary(t)
	dataDir := t.TempDir()
	a := filepath.Join(t.TempDir(), "a")
	b := filepath.Join(t.TempDir(), "b")
	if err := os.MkdirAll(a, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(b, 0o755); err != nil {
		t.Fatal(err)
	}
	seedProject(t, dataDir, "myproj-a", a)
	seedProject(t, dataDir, "myproj-b", b)

	cmd := exec.Command(exe, "project", "rm", "--force", "--data-dir", dataDir, "myproj")
	cmd.Env = pincherCoverEnv()
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("rm with ambiguous match should error; output: %s", out)
	}
	s := string(out)
	if !strings.Contains(s, "matches multiple") {
		t.Errorf("expected ambiguity message, got: %s", s)
	}
	if !strings.Contains(s, "myproj-a") || !strings.Contains(s, "myproj-b") {
		t.Errorf("expected both candidates listed, got: %s", s)
	}
}

func TestProjectCLI_RmJSONRequiresForce(t *testing.T) {
	exe := buildPincherBinary(t)
	dataDir := t.TempDir()
	projectPath := filepath.Join(t.TempDir(), "myproj")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatal(err)
	}
	id := seedProject(t, dataDir, "myproj", projectPath)

	// --json without --force should refuse rather than hang on a y/N
	// prompt nobody can answer in a JSON workflow.
	cmd := exec.Command(exe, "project", "rm", "--json", "--data-dir", dataDir, "myproj")
	cmd.Env = pincherCoverEnv()
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit; output: %s", out)
	}
	if !strings.Contains(string(out), "--json requires --force") {
		t.Errorf("expected '--json requires --force' message, got: %s", out)
	}

	// And the project should still exist (no delete happened).
	store, _ := db.Open(dataDir)
	defer store.Close()
	got, _ := store.GetProject(id)
	if got == nil {
		t.Error("project was deleted despite the refusal — should still exist")
	}
}

func TestProjectCLI_RmConfirmAccepts(t *testing.T) {
	exe := buildPincherBinary(t)
	dataDir := t.TempDir()
	projectPath := filepath.Join(t.TempDir(), "myproj")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatal(err)
	}
	seedProject(t, dataDir, "myproj", projectPath)

	cmd := exec.Command(exe, "project", "rm", "--data-dir", dataDir, "myproj")
	cmd.Stdin = strings.NewReader("y\n")
	cmd.Env = pincherCoverEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rm with y confirmation failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "Removed project") {
		t.Errorf("expected 'Removed project' confirmation, got: %s", out)
	}
}

func TestProjectCLI_RmConfirmRejects(t *testing.T) {
	exe := buildPincherBinary(t)
	dataDir := t.TempDir()
	projectPath := filepath.Join(t.TempDir(), "myproj")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatal(err)
	}
	id := seedProject(t, dataDir, "myproj", projectPath)

	cmd := exec.Command(exe, "project", "rm", "--data-dir", dataDir, "myproj")
	cmd.Stdin = strings.NewReader("n\n")
	cmd.Env = pincherCoverEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rm with n confirmation should NOT error: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "Aborted") {
		t.Errorf("expected 'Aborted' message, got: %s", out)
	}
	// Project should still exist.
	store, _ := db.Open(dataDir)
	defer store.Close()
	got, _ := store.GetProject(id)
	if got == nil {
		t.Error("project should still exist after declined confirmation")
	}
}

func TestProjectCLI_ListPopulated(t *testing.T) {
	exe := buildPincherBinary(t)
	dataDir := t.TempDir()
	projectPath := filepath.Join(t.TempDir(), "alpha")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatal(err)
	}
	seedProject(t, dataDir, "alpha", projectPath)

	cmd := exec.Command(exe, "project", "list", "--json", "--data-dir", dataDir)
	cmd.Env = pincherCoverEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("list: %v\n%s", err, out)
	}
	var report projectListReport
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if report.Count != 1 {
		t.Errorf("count=%d, want 1", report.Count)
	}
	if len(report.Projects) != 1 || report.Projects[0].Name != "alpha" {
		t.Errorf("expected one project named 'alpha', got: %v", report.Projects)
	}
	if report.Projects[0].Symbols != 5 {
		t.Errorf("expected symbols=5, got %d", report.Projects[0].Symbols)
	}
}
