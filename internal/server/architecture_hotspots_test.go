package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #305: hotspots default to production code only. Test helpers
// (newTestServer, makeReq, decode) have huge in-degree because every
// test imports them, but they're not signal for orientation.

// isTestFile is a pure helper — pin its decisions across the
// languages pincher indexes so a future tweak to one extension
// doesn't silently change the cross-language filter.
func TestIsTestFile_RecognisedConventions(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path string
		want bool
	}{
		// Go
		{"server_test.go", true},
		{"internal/server/server_test.go", true},
		{`internal\server\server_test.go`, true}, // Windows-style
		{"server.go", false},
		{"internal/server/server.go", false},

		// JS / TS
		{"foo.test.ts", true},
		{"foo.test.tsx", true},
		{"foo.spec.js", true},
		{"foo.spec.jsx", true},
		{"foo.test.mjs", true},
		{"foo.test.cjs", true},
		{"foo.ts", false},
		{"foo.spec.unrelated", false},

		// Python
		{"test_foo.py", true},
		{"foo_test.py", true},
		{"tests/foo.py", true},
		{"src/foo.py", false},

		// Ruby
		{"foo_spec.rb", true},
		{"foo_test.rb", true},
		{"spec/foo.rb", true},

		// Java / Kotlin / Scala
		{"FooTest.java", true},
		{"FooTest.kt", true},
		{"FooSpec.scala", true},
		{"Foo.java", false},

		// Directory conventions
		{"src/__tests__/foo.js", true},
		{"src/test/Foo.java", true},
		{"tests/foo.py", true},
		{"test/integration/foo.go", true},

		// Negatives — paths that look testy but aren't.
		{"src/featuretest/main.go", false}, // `featuretest` is part of a name, not a directory
		{"specfile.txt", false},            // no recognised extension
		{"testdata/corpus/foo.go", false},  // testdata is fixtures, not tests
		{"contest_winner.go", false},       // `contest` is not `_test`
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			if got := isTestFile(c.path); got != c.want {
				t.Errorf("isTestFile(%q) = %v, want %v", c.path, got, c.want)
			}
		})
	}
}

// End-to-end: seed a project with a mix of production and test
// hotspots; default architecture call drops the test ones.
func TestHandleArchitecture_HotspotsExcludeTestFilesByDefault(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p1"
	store.UpsertProject(db.Project{ID: "p1", Path: "/tmp/p1", Name: "p1", IndexedAt: time.Now()})

	// Seed 4 callers + 2 callees: one production callee (Compute),
	// one test-helper callee (newTestServer). Each gets the same
	// number of inbound CALLS edges.
	syms := []db.Symbol{
		{ID: "s::pkg.Compute#Function", ProjectID: "p1", FilePath: "internal/svc/svc.go",
			Name: "Compute", QualifiedName: "pkg.Compute", Kind: "Function", Language: "Go",
			ExtractionConfidence: 1},
		{ID: "s::pkg.newTestServer#Function", ProjectID: "p1", FilePath: "internal/server/server_test.go",
			Name: "newTestServer", QualifiedName: "pkg.newTestServer", Kind: "Function", Language: "Go",
			ExtractionConfidence: 1},
	}
	for i := 0; i < 5; i++ {
		syms = append(syms,
			db.Symbol{ID: "s::caller_p" + string(rune('0'+i)) + "#Function",
				ProjectID: "p1", FilePath: "internal/svc/svc.go",
				Name: "callerP" + string(rune('0'+i)), QualifiedName: "pkg.callerP" + string(rune('0'+i)),
				Kind: "Function", Language: "Go", ExtractionConfidence: 1},
			db.Symbol{ID: "s::caller_t" + string(rune('0'+i)) + "#Function",
				ProjectID: "p1", FilePath: "internal/server/server_test.go",
				Name: "callerT" + string(rune('0'+i)), QualifiedName: "pkg.callerT" + string(rune('0'+i)),
				Kind: "Function", Language: "Go", ExtractionConfidence: 1},
		)
	}
	if err := store.BulkUpsertSymbols(syms); err != nil {
		t.Fatalf("BulkUpsertSymbols: %v", err)
	}
	// Each caller calls both callees so they tie on count; only the
	// file_path distinguishes them.
	var edges []db.Edge
	for i := 0; i < 5; i++ {
		edges = append(edges,
			db.Edge{ProjectID: "p1",
				FromID: "s::caller_p" + string(rune('0'+i)) + "#Function",
				ToID:   "s::pkg.Compute#Function", Kind: "CALLS", Confidence: 1},
			db.Edge{ProjectID: "p1",
				FromID: "s::caller_p" + string(rune('0'+i)) + "#Function",
				ToID:   "s::pkg.newTestServer#Function", Kind: "CALLS", Confidence: 1},
		)
	}
	if err := store.BulkUpsertEdges(edges); err != nil {
		t.Fatalf("BulkUpsertEdges: %v", err)
	}

	result, err := srv.handleArchitecture(context.Background(), makeReq(map[string]any{}))
	if err != nil {
		t.Fatalf("handleArchitecture: %v", err)
	}
	body := decode(t, result)
	hotspots, _ := body["hotspots"].([]any)
	for _, h := range hotspots {
		entry, _ := h.(map[string]any)
		fp, _ := entry["file_path"].(string)
		if isTestFile(fp) {
			t.Errorf("hotspot from test file leaked through default filter: %v", entry)
		}
	}
}

// include_tests=true restores the legacy mixed list.
func TestHandleArchitecture_IncludeTestsTrue_SurfacesTestHotspots(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p1"
	store.UpsertProject(db.Project{ID: "p1", Path: "/tmp/p1", Name: "p1", IndexedAt: time.Now()})

	syms := []db.Symbol{
		{ID: "s::pkg.helper#Function", ProjectID: "p1", FilePath: "internal/server/server_test.go",
			Name: "helper", QualifiedName: "pkg.helper", Kind: "Function", Language: "Go",
			ExtractionConfidence: 1},
		{ID: "s::caller#Function", ProjectID: "p1", FilePath: "internal/server/server_test.go",
			Name: "caller", QualifiedName: "pkg.caller", Kind: "Function", Language: "Go",
			ExtractionConfidence: 1},
	}
	if err := store.BulkUpsertSymbols(syms); err != nil {
		t.Fatalf("BulkUpsertSymbols: %v", err)
	}
	if err := store.BulkUpsertEdges([]db.Edge{{
		ProjectID: "p1", FromID: "s::caller#Function", ToID: "s::pkg.helper#Function",
		Kind: "CALLS", Confidence: 1,
	}}); err != nil {
		t.Fatalf("BulkUpsertEdges: %v", err)
	}

	result, err := srv.handleArchitecture(context.Background(), makeReq(map[string]any{
		"include_tests": true,
	}))
	if err != nil {
		t.Fatalf("handleArchitecture: %v", err)
	}
	body := decode(t, result)
	hotspots, _ := body["hotspots"].([]any)
	if len(hotspots) == 0 {
		t.Error("include_tests=true should surface test-file hotspots; got none")
	}
}

func TestHandleArchitecture_SurprisingConnectionsExcludeTestsByDefault(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p-surprise-prod"
	store.UpsertProject(db.Project{ID: "p-surprise-prod", Path: "/tmp/p-surprise-prod", Name: "p-surprise-prod", IndexedAt: time.Now()})

	edges := []db.Edge{
		{ProjectID: "p-surprise-prod", FromID: "internal/app/app.go::app.Run#Function", ToID: "internal/db/db.go::db.Open#Function", Kind: "CALLS", Confidence: 1},
		{ProjectID: "p-surprise-prod", FromID: "internal/app/app_test.go::app.TestRun#Function", ToID: "internal/testutil/testutil.go::testutil.Open#Function", Kind: "CALLS", Confidence: 1},
		{ProjectID: "p-surprise-prod", FromID: "testdata/corpus/app.go::fixture.Run#Function", ToID: "internal/db/db.go::db.OpenFixture#Function", Kind: "CALLS", Confidence: 1},
	}
	if err := store.BulkUpsertEdges(edges); err != nil {
		t.Fatalf("BulkUpsertEdges: %v", err)
	}

	result, err := srv.handleArchitecture(context.Background(), makeReq(map[string]any{}))
	if err != nil {
		t.Fatalf("handleArchitecture: %v", err)
	}
	body := decode(t, result)
	connections, _ := body["surprising_connections"].([]any)
	if len(connections) != 1 {
		t.Fatalf("default should keep only production surprising connection; got %+v", connections)
	}
	entry, _ := connections[0].(map[string]any)
	for _, key := range []string{"example_from", "example_to"} {
		id, _ := entry[key].(string)
		if strings.Contains(id, "_test.") || strings.Contains(id, "testdata/") {
			t.Fatalf("test or fixture endpoint leaked into default surprising connection: %+v", entry)
		}
	}
}

func TestHandleArchitecture_IncludeTestsTrue_SurfacesTestSurprisingConnections(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p-surprise-test"
	store.UpsertProject(db.Project{ID: "p-surprise-test", Path: "/tmp/p-surprise-test", Name: "p-surprise-test", IndexedAt: time.Now()})

	edges := []db.Edge{
		{ProjectID: "p-surprise-test", FromID: "internal/app/app.go::app.Run#Function", ToID: "internal/db/db.go::db.Open#Function", Kind: "CALLS", Confidence: 1},
		{ProjectID: "p-surprise-test", FromID: "internal/app/app_test.go::app.TestRun#Function", ToID: "internal/testutil/testutil.go::testutil.Open#Function", Kind: "CALLS", Confidence: 1},
		{ProjectID: "p-surprise-test", FromID: "testdata/corpus/app.go::fixture.Run#Function", ToID: "internal/db/db.go::db.OpenFixture#Function", Kind: "CALLS", Confidence: 1},
	}
	if err := store.BulkUpsertEdges(edges); err != nil {
		t.Fatalf("BulkUpsertEdges: %v", err)
	}

	result, err := srv.handleArchitecture(context.Background(), makeReq(map[string]any{
		"include_tests": true,
	}))
	if err != nil {
		t.Fatalf("handleArchitecture: %v", err)
	}
	body := decode(t, result)
	connections, _ := body["surprising_connections"].([]any)
	if len(connections) != 2 {
		t.Fatalf("include_tests=true should keep production and test surprising connections, excluding fixtures; got %+v", connections)
	}
	foundTest := false
	for _, raw := range connections {
		entry, _ := raw.(map[string]any)
		from, _ := entry["example_from"].(string)
		to, _ := entry["example_to"].(string)
		if strings.Contains(from, "testdata/") || strings.Contains(to, "testdata/") {
			t.Fatalf("fixture endpoint leaked with include_tests=true: %+v", entry)
		}
		if strings.Contains(from, "_test.") || strings.Contains(to, "_test.") {
			foundTest = true
		}
	}
	if !foundTest {
		t.Fatalf("include_tests=true did not surface test surprising connection: %+v", connections)
	}
}

// #380: hotspots filter out non-code kinds (Variable, Setting, Section).
// Pure helper — pin the kind decisions so a future tweak doesn't
// silently let JS-script `var result` accumulators back into the list.
func TestIsHotspotKind(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind string
		want bool
	}{
		// Code surfaces — yes.
		{"Function", true},
		{"Method", true},
		{"Class", true},
		{"Interface", true},
		{"Type", true},
		{"Module", true},
		// Data / config / docs — no, even when their in-degree is high.
		{"Variable", false},
		{"Local", false},
		{"Setting", false},
		{"Section", false},
		{"Document", false},
		{"Heading", false},
		// Terraform-specific — not code-as-target either.
		{"Resource", false},
		{"DataSource", false},
		{"Output", false},
		{"Provider", false},
		{"Block", false},
		// Unknown / typo — conservative no.
		{"", false},
		{"function", false}, // case-sensitive intentionally
		{"FunctionDecl", false},
	}
	for _, c := range cases {
		t.Run(c.kind, func(t *testing.T) {
			if got := isHotspotKind(c.kind); got != c.want {
				t.Errorf("isHotspotKind(%q) = %v, want %v", c.kind, got, c.want)
			}
		})
	}
}

// End-to-end: seed a project where the highest in-degree symbol is a
// Variable (mirroring the real-world repro: `plugin/scripts/install.js`'s
// `var result` accumulator dominated pincher-repo's hotspots before
// #380). The Variable must NOT appear in hotspots; the lower-ranked
// Function must.
func TestHandleArchitecture_HotspotsExcludeVariableKind(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p380v"
	store.UpsertProject(db.Project{ID: "p380v", Path: "/tmp/p380v", Name: "p380v", IndexedAt: time.Now()})

	syms := []db.Symbol{
		// The polluting Variable — high in-degree from accumulator reads.
		{ID: "s::scripts.install.result#Variable", ProjectID: "p380v",
			FilePath: "plugin/scripts/install.js",
			Name:     "result", QualifiedName: "scripts.install.result",
			Kind: "Variable", Language: "JavaScript", ExtractionConfidence: 1},
		// The legitimate Function — lower in-degree, but the only hotspot
		// the agent should orient around.
		{ID: "s::pkg.RealHotspot#Function", ProjectID: "p380v",
			FilePath: "internal/svc/svc.go",
			Name:     "RealHotspot", QualifiedName: "pkg.RealHotspot",
			Kind: "Function", Language: "Go", ExtractionConfidence: 1},
	}
	for i := 0; i < 8; i++ {
		syms = append(syms, db.Symbol{
			ID:        "s::caller" + string(rune('0'+i)) + "#Function",
			ProjectID: "p380v", FilePath: "plugin/scripts/install.js",
			Name:          "caller" + string(rune('0'+i)),
			QualifiedName: "scripts.install.caller" + string(rune('0'+i)),
			Kind:          "Function", Language: "JavaScript", ExtractionConfidence: 1,
		})
	}
	if err := store.BulkUpsertSymbols(syms); err != nil {
		t.Fatalf("BulkUpsertSymbols: %v", err)
	}
	// 8 callers all reference the Variable; only 2 reference the Function.
	// Without the kind filter, the Variable wins on raw count.
	var edges []db.Edge
	for i := 0; i < 8; i++ {
		edges = append(edges, db.Edge{
			ProjectID: "p380v",
			FromID:    "s::caller" + string(rune('0'+i)) + "#Function",
			ToID:      "s::scripts.install.result#Variable",
			Kind:      "READS", Confidence: 1,
		})
	}
	for i := 0; i < 2; i++ {
		edges = append(edges, db.Edge{
			ProjectID: "p380v",
			FromID:    "s::caller" + string(rune('0'+i)) + "#Function",
			ToID:      "s::pkg.RealHotspot#Function",
			Kind:      "CALLS", Confidence: 1,
		})
	}
	if err := store.BulkUpsertEdges(edges); err != nil {
		t.Fatalf("BulkUpsertEdges: %v", err)
	}

	result, err := srv.handleArchitecture(context.Background(), makeReq(map[string]any{}))
	if err != nil {
		t.Fatalf("handleArchitecture: %v", err)
	}
	body := decode(t, result)
	hotspots, _ := body["hotspots"].([]any)
	for _, h := range hotspots {
		entry, _ := h.(map[string]any)
		kind, _ := entry["kind"].(string)
		if !isHotspotKind(kind) {
			t.Errorf("hotspot with non-code kind %q leaked through filter: %v", kind, entry)
		}
	}
}

// include_tests=true still applies the kind filter (#380). The legacy
// option only opts back into test-file hotspots; it does NOT also let
// Variables / Settings back in — that would bring back the JS-script
// accumulator pollution.
func TestHandleArchitecture_IncludeTestsTrue_StillFiltersNonCodeKinds(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p380vt"
	store.UpsertProject(db.Project{ID: "p380vt", Path: "/tmp/p380vt", Name: "p380vt", IndexedAt: time.Now()})

	syms := []db.Symbol{
		{ID: "s::scripts.result#Variable", ProjectID: "p380vt",
			FilePath: "plugin/scripts/install.js",
			Name:     "result", QualifiedName: "scripts.result",
			Kind: "Variable", Language: "JavaScript", ExtractionConfidence: 1},
		{ID: "s::pkg.testHelper#Function", ProjectID: "p380vt",
			FilePath: "internal/server/server_test.go",
			Name:     "testHelper", QualifiedName: "pkg.testHelper",
			Kind: "Function", Language: "Go", ExtractionConfidence: 1},
		{ID: "s::caller#Function", ProjectID: "p380vt",
			FilePath: "internal/svc/svc.go",
			Name:     "caller", QualifiedName: "pkg.caller",
			Kind: "Function", Language: "Go", ExtractionConfidence: 1},
	}
	if err := store.BulkUpsertSymbols(syms); err != nil {
		t.Fatalf("BulkUpsertSymbols: %v", err)
	}
	if err := store.BulkUpsertEdges([]db.Edge{
		{ProjectID: "p380vt", FromID: "s::caller#Function",
			ToID: "s::scripts.result#Variable", Kind: "READS", Confidence: 1},
		{ProjectID: "p380vt", FromID: "s::caller#Function",
			ToID: "s::pkg.testHelper#Function", Kind: "CALLS", Confidence: 1},
	}); err != nil {
		t.Fatalf("BulkUpsertEdges: %v", err)
	}

	result, err := srv.handleArchitecture(context.Background(), makeReq(map[string]any{
		"include_tests": true,
	}))
	if err != nil {
		t.Fatalf("handleArchitecture: %v", err)
	}
	body := decode(t, result)
	hotspots, _ := body["hotspots"].([]any)
	if len(hotspots) == 0 {
		t.Fatal("include_tests=true should surface the test-file Function helper")
	}
	for _, h := range hotspots {
		entry, _ := h.(map[string]any)
		kind, _ := entry["kind"].(string)
		if !isHotspotKind(kind) {
			t.Errorf("include_tests=true must NOT bring back non-code kinds; got %q: %v", kind, entry)
		}
	}
}

// isTestFixturePath is a pure helper — pin its decisions so that a
// future broadening of the directory list doesn't accidentally swallow
// a real entry point. The motivating bug: `architecture` returned
// `testdata/corpus/go-project/cmd/cli/main.go` as an entry point of
// pincher-repo because Go's `is_entry_point=1` flag fires on any
// `package main` declaration, including hand-crafted fixtures used by
// the pinned-corpus snapshot tests.
func TestIsTestFixturePath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path string
		want bool
	}{
		// Positives — fixtures.
		{"testdata/corpus/go-project/cmd/cli/main.go", true},
		{"internal/foo/testdata/snapshot.json", true},
		{"src/__fixtures__/sample.json", true},
		{"test-fixtures/payload.bin", true},
		{"src/test_fixtures/x.go", true},
		{"fixtures/db.sql", true},
		{`internal\foo\testdata\snapshot.json`, true}, // Windows path separator
		// Negatives — real production code, real tests, lookalikes.
		{"internal/server/server.go", false},
		{"server_test.go", false},           // tests, not fixtures — handled by isTestFile
		{"testdataloader/loader.go", false}, // `testdata` prefix without `/`
		{"src/fixturesly/main.go", false},   // `fixtures` prefix without trailing `/`
		{"cmd/main.go", false},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			if got := isTestFixturePath(c.path); got != c.want {
				t.Errorf("isTestFixturePath(%q) = %v, want %v", c.path, got, c.want)
			}
		})
	}
}

// End-to-end: a fixture-shaped entry point (`testdata/.../main.go`,
// is_entry_point=1) must NOT appear in the architecture entry_points
// list. Real entry points still surface.
func TestHandleArchitecture_EntryPointsExcludeTestFixtures(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	srv.sessionID = "pfix"
	store.UpsertProject(db.Project{ID: "pfix", Path: "/tmp/pfix", Name: "pfix", IndexedAt: time.Now()})

	syms := []db.Symbol{
		// Real entry point — must surface.
		{ID: "s::cmd.main#Function", ProjectID: "pfix", FilePath: "cmd/pinch/main.go",
			Name: "main", QualifiedName: "main.main", Kind: "Function", Language: "Go",
			IsEntryPoint: true, ExtractionConfidence: 1},
		// Fixture entry point — must be filtered.
		{ID: "s::corpus.main#Function", ProjectID: "pfix",
			FilePath: "testdata/corpus/go-project/cmd/cli/main.go",
			Name:     "main", QualifiedName: "main.main", Kind: "Function", Language: "Go",
			IsEntryPoint: true, ExtractionConfidence: 1},
	}
	if err := store.BulkUpsertSymbols(syms); err != nil {
		t.Fatalf("BulkUpsertSymbols: %v", err)
	}

	result, err := srv.handleArchitecture(context.Background(), makeReq(map[string]any{}))
	if err != nil {
		t.Fatalf("handleArchitecture: %v", err)
	}
	body := decode(t, result)
	entryPoints, _ := body["entry_points"].([]any)
	if len(entryPoints) != 1 {
		t.Fatalf("expected 1 entry point (real one), got %d: %v", len(entryPoints), entryPoints)
	}
	entry, _ := entryPoints[0].(map[string]any)
	fp, _ := entry["file_path"].(string)
	if isTestFixturePath(fp) {
		t.Errorf("fixture entry point leaked through filter: %v", entry)
	}
}

// #306: Project struct uses snake_case JSON keys.
func TestProject_JSONUsesSnakeCase(t *testing.T) {
	t.Parallel()
	p := db.Project{
		ID: "x", Path: "/p", Name: "p",
		IndexedAt: time.Now(), FileCount: 1, SymCount: 2, EdgeCount: 3,
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	wantKeys := []string{`"id":`, `"path":`, `"name":`, `"indexed_at":`, `"file_count":`, `"symbol_count":`, `"edge_count":`}
	for _, k := range wantKeys {
		if !strings.Contains(got, k) {
			t.Errorf("Project JSON missing %q; got %s", k, got)
		}
	}
	// Ensure the legacy PascalCase keys are NOT present.
	for _, k := range []string{`"ID":`, `"Path":`, `"Name":`, `"FileCount":`, `"IndexedAt":`} {
		if strings.Contains(got, k) {
			t.Errorf("Project JSON still has PascalCase key %q; got %s", k, got)
		}
	}
}
