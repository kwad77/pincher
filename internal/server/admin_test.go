package server

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #558 phase 2: doctor / rebuild_fts / self_test as MCP tools, exposed
// via the dynamic /v1/<tool> HTTP dispatcher. These tests cover the
// JSON-shape contracts; HTTP wire-up is covered by the parity test.

func TestHandleDoctor_HealthyEmptyDB(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	srv.version = "0.21.0-test"

	result, err := srv.handleDoctor(context.Background(), makeReq(map[string]any{}))
	if err != nil {
		t.Fatalf("handleDoctor: %v", err)
	}
	body := decode(t, result)

	for _, k := range []string{
		"generated_at", "binary_version", "lookback_hours",
		"schema_version", "db_size_bytes", "wal_size_bytes",
		"projects", "extraction_failures", "slow_queries", "advisories",
	} {
		if _, ok := body[k]; !ok {
			t.Errorf("doctor response missing field %q", k)
		}
	}
	if got := body["binary_version"]; got != "0.21.0-test" {
		t.Errorf("binary_version: got %v want 0.21.0-test", got)
	}
	// Empty DB → empty slices, never nil. (#328 invariant). advisories
	// is #732 — a healthy (tiny) test DB is well under the 1 GiB
	// threshold, so it must come back as [] not nil and not populated.
	for _, k := range []string{"projects", "extraction_failures", "slow_queries", "advisories"} {
		if v, ok := body[k].([]any); !ok || v == nil {
			t.Errorf("%s should be [] not nil; got %T %v", k, body[k], body[k])
		}
	}
	if adv, _ := body["advisories"].([]any); len(adv) != 0 {
		t.Errorf("healthy test DB should produce no advisories; got %v", adv)
	}
}

// #732: largeDBAdvisory turns a bare db_size_bytes number into an
// actionable health warning when the store is pathologically large.
func TestLargeDBAdvisory(t *testing.T) {
	t.Parallel()

	// Under the 1 GiB threshold → no advisory.
	if got := largeDBAdvisory(500<<20, nil); got != "" {
		t.Errorf("500 MB DB should produce no advisory; got %q", got)
	}
	if got := largeDBAdvisory(1<<30, nil); got != "" {
		t.Errorf("exactly 1 GiB should not trip the advisory (threshold is strictly greater); got %q", got)
	}

	// Over the threshold → advisory that names the heaviest project and
	// gives concrete remediation.
	projects := []doctorProjectSummary{
		{Name: "warp_rc", Symbols: 1453923, Files: 4363},
		{Name: "pincher-repo", Symbols: 5169, Files: 394},
	}
	got := largeDBAdvisory(5<<30, projects)
	if got == "" {
		t.Fatal("5 GB DB should produce an advisory")
	}
	for _, want := range []string{"5.0 GB", "warp_rc", "1453923", "prune_dead", "prune-stale", "vacuum"} {
		if !strings.Contains(got, want) {
			t.Errorf("advisory missing %q\n  got: %s", want, got)
		}
	}

	// Over the threshold but no project list → still advises, just
	// without the heaviest-project detail.
	bare := largeDBAdvisory(2<<30, nil)
	if bare == "" || strings.Contains(bare, "Heaviest project") {
		t.Errorf("empty project list should still advise, without the heaviest-project clause; got %q", bare)
	}
}

func TestReclaimableDBAdvisory(t *testing.T) {
	t.Parallel()

	if got := reclaimableDBAdvisory(900<<20, 49<<20); got != "" {
		t.Errorf("below threshold should not advise; got %q", got)
	}
	if got := reclaimableDBAdvisory(0, 80<<20); got != "" {
		t.Errorf("unknown DB size should not advise; got %q", got)
	}

	got := reclaimableDBAdvisory(900<<20, 60<<20)
	if got == "" {
		t.Fatal("reclaimable bytes above threshold should produce an advisory")
	}
	for _, want := range []string{"60.0 MB", "900.0 MB", "reclaimable free pages", "pincher vacuum", "pincher doctor --fix"} {
		if !strings.Contains(got, want) {
			t.Errorf("advisory missing %q\n  got: %s", want, got)
		}
	}
}

// #635 v0.67 follow-up: payload-outlier advisory. Tests pin the two
// gating conditions (ratio + absolute max) so a future tweak doesn't
// silently neuter the advisory by relaxing one but not the other.
func TestPayloadOutlierAdvisory(t *testing.T) {
	t.Parallel()

	// Empty input → silent.
	if got := payloadOutlierAdvisory(nil); got != "" {
		t.Errorf("nil rows should produce no advisory; got %q", got)
	}
	// All rows below ratio threshold → silent even if some are large.
	rows := []db.ToolCallPayloadRow{
		{Tool: "search", AvgBytes: 80_000, MaxBytes: 160_000}, // ratio 2× — below 10× cutoff
	}
	if got := payloadOutlierAdvisory(rows); got != "" {
		t.Errorf("2× spread should not trip; got %q", got)
	}
	// All rows below absolute-max threshold → silent even with big ratio.
	rows = []db.ToolCallPayloadRow{
		{Tool: "search", AvgBytes: 1_000, MaxBytes: 50_000}, // ratio 50× but max < 100 KB
	}
	if got := payloadOutlierAdvisory(rows); got != "" {
		t.Errorf("max <100 KB should not trip; got %q", got)
	}
	// Both conditions crossed → advisory names the tool, prints
	// spread × ratio, and includes remediation pointer.
	rows = []db.ToolCallPayloadRow{
		{Tool: "guide", AvgBytes: 5_000, MaxBytes: 250_000}, // ratio 50×, max 250 KB
		{Tool: "search", AvgBytes: 500, MaxBytes: 25_000},   // ratio 50× but max < 100 KB
	}
	got := payloadOutlierAdvisory(rows)
	if got == "" {
		t.Fatal("guide max=250KB / avg=5KB should produce an advisory")
	}
	for _, want := range []string{"guide", "spread", "Payload outliers", "/v1/tool-payload-stats", "min_confidence"} {
		if !strings.Contains(got, want) {
			t.Errorf("advisory missing %q\n  got: %s", want, got)
		}
	}
	// search should NOT appear — it failed the max threshold.
	if strings.Contains(got, "search") {
		t.Errorf("advisory must skip rows below the max threshold; got: %s", got)
	}

	// Tool-aware remediation: context does not accept min_confidence.
	// Dogfood exposed the old generic text as misleading when context
	// was the outlier; it should point at context's real payload controls.
	got = payloadOutlierAdvisory([]db.ToolCallPayloadRow{
		{Tool: "context", AvgBytes: 12_000, MaxBytes: 180_000},
	})
	for _, want := range []string{"context", "lite=true", "fields=symbol", "fields=symbol,callees"} {
		if !strings.Contains(got, want) {
			t.Errorf("context advisory missing %q\n  got: %s", want, got)
		}
	}
	if strings.Contains(got, "min_confidence") {
		t.Errorf("context advisory must not suggest min_confidence; got: %s", got)
	}
}

// #635 v0.67 follow-up: tool-mix entropy advisory. Tests pin the
// three gating conditions (call volume floor + entropy ceiling +
// top-1 share floor) so any one being relaxed doesn't silently
// over-fire on healthy installs.
func TestToolMixStuckAdvisory(t *testing.T) {
	t.Parallel()

	// Empty input → silent.
	if got := toolMixStuckAdvisory(nil); got != "" {
		t.Errorf("nil rows should produce no advisory; got %q", got)
	}
	// Below call-volume floor → silent even if concentration is extreme.
	rows := []db.ToolCallTallyRow{
		{Tool: "search", CallCount: 50}, // entire workload, single tool, but <100 calls
	}
	if got := toolMixStuckAdvisory(rows); got != "" {
		t.Errorf("low call volume should not trip; got %q", got)
	}
	// Above call-volume floor but healthy spread → silent.
	rows = []db.ToolCallTallyRow{
		{Tool: "search", CallCount: 60},
		{Tool: "symbol", CallCount: 50},
		{Tool: "context", CallCount: 40},
		{Tool: "trace", CallCount: 30},
	}
	if got := toolMixStuckAdvisory(rows); got != "" {
		t.Errorf("balanced mix should not trip; got %q", got)
	}
	// Stuck: one tool >80% over ≥100 calls → fires and names the tool.
	rows = []db.ToolCallTallyRow{
		{Tool: "search", CallCount: 950},
		{Tool: "symbol", CallCount: 30},
		{Tool: "trace", CallCount: 20},
	}
	got := toolMixStuckAdvisory(rows)
	if got == "" {
		t.Fatal("950/1000 calls on search should produce an advisory")
	}
	for _, want := range []string{"search", "entropy", "Tool-Mix Health", "bits"} {
		if !strings.Contains(got, want) {
			t.Errorf("advisory missing %q\n  got: %s", want, got)
		}
	}
}

// #575: pre-fix the handler iterated every project and pulled `top`
// failures per project, so a 125-project install ballooned the
// response past the MCP token cap. `top` now caps the projects
// list AND the global failure list; truncation surfaces in
// `projects_truncated` / `extraction_failures_truncated` so the
// caller knows.
func TestHandleDoctor_CapsProjectsAndFailuresGlobally(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	// Seed 50 projects to repro the multi-project bloat shape.
	for i := 0; i < 50; i++ {
		id := fmt.Sprintf("p%02d", i)
		store.UpsertProject(db.Project{
			ID: id, Path: "/tmp/" + id, Name: id,
			IndexedAt: time.Now(),
			SymCount:  i, // sort by symbols desc → p49 first
		})
	}

	result, err := srv.handleDoctor(context.Background(), makeReq(map[string]any{"top": 5}))
	if err != nil {
		t.Fatalf("handleDoctor: %v", err)
	}
	body := decode(t, result)

	projects, _ := body["projects"].([]any)
	if len(projects) != 5 {
		t.Errorf("expected 5 projects (capped by top=5), got %d", len(projects))
	}
	if truncated, ok := body["projects_truncated"].(float64); !ok || truncated != 45 {
		t.Errorf("expected projects_truncated=45, got %v", body["projects_truncated"])
	}
	// Sorted by symbols desc → top should be p49.
	if len(projects) > 0 {
		first := projects[0].(map[string]any)
		if first["name"] != "p49" {
			t.Errorf("expected first project p49 (largest), got %v", first["name"])
		}
	}
}

func TestHandleDoctor_WithProject(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	store.UpsertProject(db.Project{
		ID: "p1", Path: "/tmp/p1", Name: "p1",
		IndexedAt: time.Now(), BinaryVersion: "0.21.0",
		FileCount: 3, SymCount: 42, EdgeCount: 17,
	})

	result, err := srv.handleDoctor(context.Background(), makeReq(map[string]any{"top": 5}))
	if err != nil {
		t.Fatalf("handleDoctor: %v", err)
	}
	body := decode(t, result)
	projects, ok := body["projects"].([]any)
	if !ok || len(projects) != 1 {
		t.Fatalf("expected 1 project, got %v", body["projects"])
	}
	p := projects[0].(map[string]any)
	if p["name"] != "p1" || p["symbols"].(float64) != 42 {
		t.Errorf("project shape wrong: %v", p)
	}
	if got, ok := p["stale"].(bool); !ok || got {
		t.Errorf("fresh MCP doctor project must carry stale=false as a JSON boolean; got %T %v in %v", p["stale"], p["stale"], p)
	}
	if _, present := p["stale_reason"]; present {
		t.Errorf("fresh MCP doctor project should omit stale_reason; got %v", p)
	}
}

func TestHandleDoctor_DotProjectUsesSessionProject(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p-current"
	store.UpsertProject(db.Project{
		ID: "p-current", Path: "/tmp/current", Name: "current",
		IndexedAt: time.Now(), FileCount: 2, SymCount: 7, EdgeCount: 1,
	})
	store.UpsertProject(db.Project{
		ID: "/tmp/.hermes/hermes-agent", Path: "/tmp/.hermes/hermes-agent", Name: "hermes-agent",
		IndexedAt: time.Now(), FileCount: 3, SymCount: 42, EdgeCount: 17,
	})

	result, err := srv.handleDoctor(context.Background(), makeReq(map[string]any{"project": ".", "top": 5}))
	if err != nil {
		t.Fatalf("handleDoctor: %v", err)
	}
	body := decode(t, result)
	projects, ok := body["projects"].([]any)
	if !ok || len(projects) != 1 {
		t.Fatalf("expected one session project for project='.', got %v", body["projects"])
	}
	p := projects[0].(map[string]any)
	if p["id"] != "p-current" {
		t.Fatalf("project='.' resolved to %v, want session project", p)
	}
}

func TestHandleDoctor_ProjectStalenessFields(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)

	current := db.CurrentSchemaVersion()
	old := current - 1
	if err := store.UpsertProject(db.Project{
		ID: "old", Path: "/tmp/old", Name: "old",
		IndexedAt: time.Now(), FileCount: 4, SymCount: 5, EdgeCount: 6,
	}); err != nil {
		t.Fatalf("UpsertProject old: %v", err)
	}
	if _, err := store.DB().Exec(`UPDATE projects SET schema_version_at_index = ? WHERE id = ?`, old, "old"); err != nil {
		t.Fatalf("backdate old project schema: %v", err)
	}

	result, err := srv.handleDoctor(context.Background(), makeReq(map[string]any{"top": 5}))
	if err != nil {
		t.Fatalf("handleDoctor: %v", err)
	}
	body := decode(t, result)
	projects, ok := body["projects"].([]any)
	if !ok || len(projects) != 1 {
		t.Fatalf("expected 1 project, got %v", body["projects"])
	}
	p := projects[0].(map[string]any)
	if got := p["schema_version_at_index"]; got != float64(old) {
		t.Fatalf("schema_version_at_index = %v, want %d; project=%v", got, old, p)
	}
	if got := p["stale"]; got != true {
		t.Fatalf("stale = %v, want true; project=%v", got, p)
	}
	reason, _ := p["stale_reason"].(string)
	for _, want := range []string{"indexed at v", fmt.Sprintf("current is v%d", current)} {
		if !strings.Contains(reason, want) {
			t.Errorf("stale_reason missing %q; got %q", want, reason)
		}
	}
}

func TestStaleIndexRunningAdvisory(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 27, 2, 30, 0, 0, time.UTC)
	recent := now.Add(-10 * time.Minute).Format(time.RFC3339)
	old := now.Add(-2 * time.Hour).Format(time.RFC3339)

	if got := staleIndexRunningAdvisory([]doctorProjectSummary{
		{Name: "active", IndexState: "running", IndexStartedAt: recent},
	}, now); got != "" {
		t.Fatalf("recent running marker should stay silent, got %q", got)
	}
	if got := staleIndexRunningAdvisory([]doctorProjectSummary{
		{Name: "done", IndexState: "complete", IndexStartedAt: old},
	}, now); got != "" {
		t.Fatalf("complete marker should stay silent, got %q", got)
	}

	got := staleIndexRunningAdvisory([]doctorProjectSummary{
		{Name: "hermes-agent", IndexState: "running", IndexStartedAt: old},
	}, now)
	if got == "" {
		t.Fatal("old running marker should produce an advisory")
	}
	for _, want := range []string{"hermes-agent", "running", "pincher index --force", "/v1/index", "#1905"} {
		if !strings.Contains(got, want) {
			t.Errorf("advisory missing %q\n  got: %s", want, got)
		}
	}
}

func TestProjectSchemaDriftAdvisory(t *testing.T) {
	t.Parallel()
	current := 38
	atCurrent := 38
	atOld := 35

	if got := projectSchemaDriftAdvisory([]doctorProjectSummary{
		{Name: "fresh", SchemaVersionAtIndex: &atCurrent},
	}, current); got != "" {
		t.Fatalf("fresh schema should stay silent, got %q", got)
	}

	got := projectSchemaDriftAdvisory([]doctorProjectSummary{
		{Name: "pincher", SchemaVersionAtIndex: &atOld},
	}, current)
	if got == "" {
		t.Fatal("old schema should produce an advisory")
	}
	for _, want := range []string{"pincher", "v35 < v38", "pincher index --force", "/v1/index"} {
		if !strings.Contains(got, want) {
			t.Errorf("advisory missing %q\n  got: %s", want, got)
		}
	}

	got = projectSchemaDriftAdvisory([]doctorProjectSummary{
		{Name: "dup", Path: "/repo/a", SchemaVersionAtIndex: &atOld},
		{Name: "dup", Path: "/repo/b", SchemaVersionAtIndex: &atOld},
	}, current)
	for _, want := range []string{"dup at /repo/a", "dup at /repo/b"} {
		if !strings.Contains(got, want) {
			t.Errorf("duplicate-name advisory should include path %q\n  got: %s", want, got)
		}
	}
}

func TestHandleDoctor_IndexStateRunningAdvisory(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	if err := store.UpsertProject(db.Project{
		ID: "p1", Path: "/tmp/p1", Name: "hermes-agent",
		IndexedAt: time.Now(), FileCount: 3, SymCount: 42, EdgeCount: 17,
	}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	started := time.Now().Add(-2 * time.Hour)
	if err := store.MarkProjectIndexStarted("p1", started); err != nil {
		t.Fatalf("MarkProjectIndexStarted: %v", err)
	}

	result, err := srv.handleDoctor(context.Background(), makeReq(map[string]any{"top": 5}))
	if err != nil {
		t.Fatalf("handleDoctor: %v", err)
	}
	body := decode(t, result)
	projects, ok := body["projects"].([]any)
	if !ok || len(projects) != 1 {
		t.Fatalf("expected 1 project, got %v", body["projects"])
	}
	p := projects[0].(map[string]any)
	if got := p["index_state"]; got != "running" {
		t.Fatalf("index_state = %v, want running; project=%v", got, p)
	}
	if got, ok := p["index_started_at"].(string); !ok || got == "" {
		t.Fatalf("index_started_at missing: %v", p)
	}
	advisories, _ := body["advisories"].([]any)
	var joined []string
	for _, a := range advisories {
		joined = append(joined, fmt.Sprint(a))
	}
	got := strings.Join(joined, "\n")
	for _, want := range []string{"hermes-agent", "pincher index --force", "/v1/index"} {
		if !strings.Contains(got, want) {
			t.Errorf("advisories missing %q\n  got: %s", want, got)
		}
	}
}

// TestHandleDoctor_ProjectFilter (#1401) — `project` arg restricts the
// projects + extraction_failures sections to projects whose name or id
// contains the case-insensitive substring. Database-level advisories
// stay project-wide (asserted indirectly: response shape stays normal,
// and no error from the unfiltered advisory walks).
func TestHandleDoctor_ProjectFilter(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	for _, c := range []struct{ id, name string }{
		{"alpha", "alpha-proj"},
		{"beta", "beta-proj"},
	} {
		store.UpsertProject(db.Project{
			ID: c.id, Path: "/tmp/" + c.id, Name: c.name,
			IndexedAt: time.Now(), SymCount: 10,
		})
		_ = store.RecordExtractionFailure(c.id, c.id+".go", "Go", "extractor_panicked", "boom")
	}
	store.UpsertProject(db.Project{
		ID: "ghost", Path: "/tmp/ghost", Name: "ghost-proj",
		IndexedAt: time.Now(), FileCount: 200, SymCount: 2_000, EdgeCount: 0,
	})

	// Filter to "alpha" — only alpha-proj + its failure row visible.
	result, err := srv.handleDoctor(context.Background(), makeReq(map[string]any{
		"top": 50, "project": "alpha",
	}))
	if err != nil {
		t.Fatalf("handleDoctor: %v", err)
	}
	body := decode(t, result)
	if body["project_filter"] != "alpha" {
		t.Errorf("project_filter echo missing: %v", body["project_filter"])
	}
	projects, _ := body["projects"].([]any)
	if len(projects) != 1 {
		t.Fatalf("filter alpha matched %d projects, want 1: %v", len(projects), projects)
	}
	if got := projects[0].(map[string]any)["name"]; got != "alpha-proj" {
		t.Errorf("filtered project name = %v, want alpha-proj", got)
	}
	failures, _ := body["extraction_failures"].([]any)
	if len(failures) != 1 {
		t.Fatalf("filter alpha extraction_failures = %d, want 1: %v", len(failures), failures)
	}
	if got := failures[0].(map[string]any)["file"]; got != "alpha.go" {
		t.Errorf("filtered failure file = %v, want alpha.go", got)
	}
	advisories, _ := body["advisories"].([]any)
	joinedAdvisories := fmt.Sprint(advisories)
	if !strings.Contains(joinedAdvisories, "ghost-proj") {
		t.Errorf("store-wide ghost advisory should survive project filter; got %v", advisories)
	}

	// Case-insensitive substring: "BETA" matches beta-proj.
	resultB, err := srv.handleDoctor(context.Background(), makeReq(map[string]any{
		"top": 50, "project": "BETA",
	}))
	if err != nil {
		t.Fatalf("handleDoctor BETA: %v", err)
	}
	bodyB := decode(t, resultB)
	projectsB, _ := bodyB["projects"].([]any)
	if len(projectsB) != 1 || projectsB[0].(map[string]any)["name"] != "beta-proj" {
		t.Errorf("case-insensitive BETA matched %v, want one beta-proj", projectsB)
	}

	// Empty filter = unchanged behavior (no project_filter key emitted).
	resultAll, err := srv.handleDoctor(context.Background(), makeReq(map[string]any{"top": 50}))
	if err != nil {
		t.Fatalf("handleDoctor (no filter): %v", err)
	}
	bodyAll := decode(t, resultAll)
	if _, ok := bodyAll["project_filter"]; ok {
		t.Errorf("project_filter must be absent when filter unset; got %v", bodyAll["project_filter"])
	}
	projectsAll, _ := bodyAll["projects"].([]any)
	if len(projectsAll) != 3 {
		t.Errorf("unfiltered call returned %d projects, want 3", len(projectsAll))
	}
}

// TestHandleDoctor_ProjectFilter_TieredMatch (#1404) — regression
// guard for the original bug: substring filter "pincher-repo" used
// to over-match every nested corpus project whose id contained the
// parent path. With the tiered fix an exact-name hit short-circuits
// the substring fallback.
func TestHandleDoctor_ProjectFilter_TieredMatch(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)

	// Parent project + two nested corpus projects under the parent's
	// path. All three IDs contain the substring "pincher-repo".
	for _, c := range []struct{ id, name string }{
		{"d:\\claudecode\\pincher-repo", "pincher-repo"},
		{"d:\\claudecode\\pincher-repo\\testdata\\corpus\\k8s-ops", "k8s-ops"},
		{"d:\\claudecode\\pincher-repo\\testdata\\corpus\\python-web", "python-web"},
	} {
		store.UpsertProject(db.Project{
			ID: c.id, Path: c.id, Name: c.name,
			IndexedAt: time.Now(), SymCount: 10,
		})
	}

	// Exact name match — pincher-repo wins, k8s-ops + python-web stay
	// invisible despite their IDs containing the substring.
	result, err := srv.handleDoctor(context.Background(), makeReq(map[string]any{
		"top": 50, "project": "pincher-repo",
	}))
	if err != nil {
		t.Fatalf("handleDoctor: %v", err)
	}
	body := decode(t, result)
	projects, _ := body["projects"].([]any)
	if len(projects) != 1 {
		t.Fatalf("exact name 'pincher-repo' matched %d projects, want 1 (nested corpora must not be dragged in by substring): %v", len(projects), projects)
	}
	if name := projects[0].(map[string]any)["name"]; name != "pincher-repo" {
		t.Errorf("matched project name = %v, want pincher-repo", name)
	}

	// Substring fallback — "pincher" doesn't exact-name-match anything,
	// so it falls through to substring and hits all three.
	resultSub, err := srv.handleDoctor(context.Background(), makeReq(map[string]any{
		"top": 50, "project": "pincher",
	}))
	if err != nil {
		t.Fatalf("handleDoctor pincher: %v", err)
	}
	bodySub := decode(t, resultSub)
	projectsSub, _ := bodySub["projects"].([]any)
	if len(projectsSub) != 3 {
		t.Errorf("substring 'pincher' should hit all 3 (pincher-repo + 2 corpora), got %d: %v", len(projectsSub), projectsSub)
	}
}

func TestHandleRebuildFTS_DryRunByDefault(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	result, err := srv.handleRebuildFTS(context.Background(), makeReq(map[string]any{}))
	if err != nil {
		t.Fatalf("handleRebuildFTS: %v", err)
	}
	body := decode(t, result)
	if body["dry_run"] != true {
		t.Errorf("default call must be dry_run=true; got %v", body["dry_run"])
	}
	if _, ok := body["would_reindex_symbols"]; !ok {
		t.Errorf("dry-run response must include would_reindex_symbols; got %v", body)
	}
}

func TestHandleRebuildFTS_ConfirmedRebuilds(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	result, err := srv.handleRebuildFTS(context.Background(), makeReq(map[string]any{"confirm": true}))
	if err != nil {
		t.Fatalf("handleRebuildFTS confirmed: %v", err)
	}
	body := decode(t, result)
	if body["dry_run"] != false {
		t.Errorf("confirmed call must report dry_run=false; got %v", body["dry_run"])
	}
	if _, ok := body["rebuilt_rows"]; !ok {
		t.Errorf("confirmed response must include rebuilt_rows; got %v", body)
	}
	if _, ok := body["duration_ms"]; !ok {
		t.Errorf("confirmed response must include duration_ms; got %v", body)
	}
}

func TestHandleSelfTest_HealthyInstall(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	result, err := srv.handleSelfTest(context.Background(), makeReq(map[string]any{}))
	if err != nil {
		t.Fatalf("handleSelfTest: %v", err)
	}
	body := decode(t, result)
	if body["ok"] != true {
		t.Errorf("self_test on a clean install must report ok=true; got %v\nfull body: %v", body["ok"], body)
	}
	steps, ok := body["steps"].([]any)
	if !ok || len(steps) != 5 {
		t.Fatalf("expected 5 steps, got %v", body["steps"])
	}
	for i, raw := range steps {
		step := raw.(map[string]any)
		if step["ok"] != true {
			t.Errorf("step %d (%v) failed: %v", i, step["label"], step["error"])
		}
	}
}
