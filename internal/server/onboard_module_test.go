// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"

	"github.com/kwad77/pincher/internal/index"
)

// #1391 v0.84 Phase 4 audit suite for onboard_module. Positive +
// negative + control + cross-check shape per the composite-tool
// roadmap contract.

const onboardGoMod = `module example.com/onboard

go 1.22
`

// Fixture: two packages in two subdirectories. `core` imports nothing
// external; `consumers` imports + calls `core`. The composite scoped
// to `core/` should show:
//   - scope: core/ as the directory
//   - external_consumers: consumer calls in (boundary inbound)
//   - external_dependencies: none (core depends on nothing external)
//   - module_summary: language=Go, exported surface counting Foo + Bar
const onboardCoreSrc = `package core

// Foo is exported. Counts toward exported_surface_count.
func Foo() int { return helper() }

// Bar is exported.
func Bar() string { return "bar" }

// helper is unexported. Used by Foo, not by anything outside.
func helper() int { return 42 }
`

const onboardConsumerSrc = `package consumer

import "example.com/onboard/core"

// UseCore calls into core — produces an external_consumer boundary
// edge when the composite is scoped to core/.
func UseCore() int {
	return core.Foo()
}
`

func setupOnboardTestServer(t *testing.T) (*Server, string, string) {
	t.Helper()
	srv, store, root := newTestServer(t)
	srv.sessionRoot = root
	writeGoFile(t, root, "go.mod", onboardGoMod)
	writeGoFile(t, root, "core/core.go", onboardCoreSrc)
	writeGoFile(t, root, "consumers/consumer.go", onboardConsumerSrc)
	idx := index.New(store)
	res, err := idx.Index(context.Background(), root, false)
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	srv.sessionID = res.ProjectID
	return srv, root, res.ProjectID
}

// TestOnboardModule_MissingDirectory — negative-control: empty
// directory yields a rich error with next-step suggestions.
func TestOnboardModule_MissingDirectory(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	res, err := srv.handleOnboardModule(context.Background(), makeReq(map[string]any{}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res == nil || !res.IsError {
		t.Fatal("expected IsError=true for missing directory")
	}
	text := textOf(t, res)
	if !strings.Contains(text, "onboard_module requires `directory`") {
		t.Errorf("expected rich-error message; got %s", text)
	}
}

// TestOnboardModule_UnknownDirectory_EmptyReason — empty-path:
// directory that doesn't match any indexed file stamps empty_reason.
func TestOnboardModule_UnknownDirectory_EmptyReason(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupOnboardTestServer(t)

	res, err := srv.handleOnboardModule(context.Background(), makeReq(map[string]any{
		"directory": "no_such_path/",
		"project":   projectID,
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	body := decode(t, res)
	meta, ok := body["_meta"].(map[string]any)
	if !ok {
		t.Fatal("missing _meta")
	}
	if meta["empty_reason"] != EmptyReasonNoResultsInCorpus {
		t.Errorf("empty_reason = %v; want %s", meta["empty_reason"], EmptyReasonNoResultsInCorpus)
	}
	scope, _ := body["scope"].(map[string]any)
	if scope["symbol_count"].(float64) != 0 {
		t.Errorf("symbol_count = %v; want 0", scope["symbol_count"])
	}
}

// TestOnboardModule_ScopeCountsCoreDirectory — positive happy path:
// scope counts match the fixture.
func TestOnboardModule_ScopeCountsCoreDirectory(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupOnboardTestServer(t)

	res, err := srv.handleOnboardModule(context.Background(), makeReq(map[string]any{
		"directory": "core/",
		"project":   projectID,
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	body := decode(t, res)
	scope, _ := body["scope"].(map[string]any)
	if scope["directory"] != "core/" {
		t.Errorf("directory = %v; want core/", scope["directory"])
	}
	// core/core.go has at least Foo, Bar, helper — symbol_count ≥ 3.
	if sc, _ := scope["symbol_count"].(float64); sc < 3 {
		t.Errorf("symbol_count = %v; expected ≥ 3 (Foo, Bar, helper)", sc)
	}
	if fc, _ := scope["file_count"].(float64); fc != 1 {
		t.Errorf("file_count = %v; expected 1 (just core.go)", fc)
	}
}

// TestOnboardModule_ExternalConsumersFromOutside — positive: scoping
// to core/ surfaces the consumers/ side as an external_consumer.
func TestOnboardModule_ExternalConsumersFromOutside(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupOnboardTestServer(t)

	res, err := srv.handleOnboardModule(context.Background(), makeReq(map[string]any{
		"directory": "core/",
		"project":   projectID,
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	body := decode(t, res)
	consumers, _ := body["external_consumers"].([]any)
	if len(consumers) == 0 {
		t.Errorf("expected at least one external_consumer (UseCore → core.Foo); got %v", consumers)
	}
	foundUseCore := false
	for _, c := range consumers {
		m := c.(map[string]any)
		if m["from_name"] == "UseCore" {
			foundUseCore = true
			if to := m["to_name"].(string); to != "Foo" {
				t.Errorf("external_consumer to_name = %v; want Foo", to)
			}
			if fromIn, _ := m["from_in_scope"].(bool); fromIn {
				t.Error("from_in_scope should be false for an external consumer")
			}
			if toIn, _ := m["to_in_scope"].(bool); !toIn {
				t.Error("to_in_scope should be true for an external consumer")
			}
		}
	}
	if !foundUseCore {
		t.Errorf("UseCore not in external_consumers; got %v", consumers)
	}
}

// TestOnboardModule_ExternalDependenciesFromInside — cross-check:
// scoping to consumers/ surfaces the boundary edge in the OTHER
// direction (external_dependency on core.Foo).
func TestOnboardModule_ExternalDependenciesFromInside(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupOnboardTestServer(t)

	res, err := srv.handleOnboardModule(context.Background(), makeReq(map[string]any{
		"directory": "consumers/",
		"project":   projectID,
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	body := decode(t, res)
	deps, _ := body["external_dependencies"].([]any)
	if len(deps) == 0 {
		t.Errorf("expected at least one external_dependency (UseCore → core.Foo); got %v", deps)
	}
	foundFoo := false
	for _, d := range deps {
		m := d.(map[string]any)
		if m["to_name"] == "Foo" {
			foundFoo = true
			if fromIn, _ := m["from_in_scope"].(bool); !fromIn {
				t.Error("from_in_scope should be true for an external dependency")
			}
			if toIn, _ := m["to_in_scope"].(bool); toIn {
				t.Error("to_in_scope should be false for an external dependency")
			}
		}
	}
	if !foundFoo {
		t.Errorf("Foo not in external_dependencies; got %v", deps)
	}
}

// TestDedupBoundaryEdges_1808 — onboard_module reports a SET of distinct
// boundary dependencies, not a multiset of call-sites. A live probe saw
// the same (from_id,to_id) pair surface 6× and evict genuine distinct
// deps past the maxEdgesPerList cap (#1808).
func TestDedupBoundaryEdges_1808(t *testing.T) {
	t.Parallel()

	// Positive: a pair repeated 6× collapses to one; a distinct pair
	// survives. First occurrence wins, so input order is preserved.
	in := []onboardEdgeRef{
		{FromID: "a", ToID: "x", FromName: "A", ToName: "X", Kind: "CALLS"},
		{FromID: "a", ToID: "x", FromName: "A", ToName: "X", Kind: "CALLS"},
		{FromID: "a", ToID: "x", FromName: "A", ToName: "X", Kind: "CALLS"},
		{FromID: "b", ToID: "y", FromName: "B", ToName: "Y", Kind: "CALLS"},
		{FromID: "a", ToID: "x", FromName: "A", ToName: "X", Kind: "CALLS"},
		{FromID: "a", ToID: "x", FromName: "A", ToName: "X", Kind: "CALLS"},
		{FromID: "a", ToID: "x", FromName: "A", ToName: "X", Kind: "CALLS"},
	}
	got := dedupBoundaryEdges(in)
	if len(got) != 2 {
		t.Fatalf("expected 2 distinct boundary pairs; got %d (%v)", len(got), got)
	}
	if got[0].FromID != "a" || got[0].ToID != "x" {
		t.Errorf("first surviving pair should be a→x (first occurrence wins); got %v", got[0])
	}
	if got[1].FromID != "b" || got[1].ToID != "y" {
		t.Errorf("second surviving pair should be b→y; got %v", got[1])
	}

	// Control: a→x and x→a are distinct boundary edges (direction
	// matters) — both must survive.
	dir := dedupBoundaryEdges([]onboardEdgeRef{
		{FromID: "a", ToID: "x"},
		{FromID: "x", ToID: "a"},
	})
	if len(dir) != 2 {
		t.Errorf("a→x and x→a are distinct edges; want 2, got %d", len(dir))
	}

	// Negative / boundary: nil and already-unique inputs pass through
	// unchanged.
	if got := dedupBoundaryEdges(nil); len(got) != 0 {
		t.Errorf("nil input should yield empty; got %v", got)
	}
	uniq := []onboardEdgeRef{{FromID: "a", ToID: "x"}, {FromID: "b", ToID: "y"}}
	if got := dedupBoundaryEdges(uniq); len(got) != 2 {
		t.Errorf("already-unique input should pass through; want 2, got %d", len(got))
	}
}

// TestOnboardModule_BoundaryEdgesProjectScoped — onboard_module's
// edge query joins `symbols` to resolve from/to names. The symbols
// primary key is (project_id, id), so the same symbol id can exist
// in several indexed projects (forks, nested registrations). Pre-fix
// the joins matched on id alone, so every boundary edge was
// multiplied by the count of projects sharing that id — a module
// dependency surfaced once per colliding project. Found dogfooding
// onboard_module against a multi-project install.
func TestOnboardModule_BoundaryEdgesProjectScoped(t *testing.T) {
	t.Parallel()
	srv, store, rootA := newTestServer(t)
	srv.sessionRoot = rootA

	const mod = "module example.com/scoped\n\ngo 1.22\n"
	const libSrc = `package lib

func Target() int { return 1 }
`
	const appSrc = `package app

import "example.com/scoped/lib"

func CallsTarget() int { return lib.Target() }
`
	writeGoFile(t, rootA, "go.mod", mod)
	writeGoFile(t, rootA, "lib/lib.go", libSrc)
	writeGoFile(t, rootA, "app/app.go", appSrc)
	idx := index.New(store)
	resA, err := idx.Index(context.Background(), rootA, false)
	if err != nil {
		t.Fatalf("index A: %v", err)
	}
	srv.sessionID = resA.ProjectID

	// Project B — identical relative paths + symbols, so every symbol
	// id collides with project A's. A correct project-scoped join
	// ignores B entirely when onboard_module runs against A.
	rootB := t.TempDir()
	writeGoFile(t, rootB, "go.mod", mod)
	writeGoFile(t, rootB, "lib/lib.go", libSrc)
	writeGoFile(t, rootB, "app/app.go", appSrc)
	if _, err := idx.Index(context.Background(), rootB, false); err != nil {
		t.Fatalf("index B: %v", err)
	}

	out, err := srv.handleOnboardModule(context.Background(), makeReq(map[string]any{
		"directory": "app/",
		"project":   resA.ProjectID,
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	body := decode(t, out)
	deps, _ := body["external_dependencies"].([]any)
	targetCount := 0
	for _, d := range deps {
		if m, ok := d.(map[string]any); ok && m["to_name"] == "Target" {
			targetCount++
		}
	}
	if targetCount == 0 {
		t.Fatalf("lib.Target not in external_dependencies — fixture/resolver regression; got %v", deps)
	}
	if targetCount != 1 {
		t.Errorf("lib.Target listed %d× in external_dependencies; expected 1 — "+
			"cross-project JOIN leak: the symbol joins matched project B's identical "+
			"ids. The joins must be scoped to e.project_id.", targetCount)
	}
}

// TestOnboardModule_ModuleSummaryShape — control: module_summary
// is populated with language breakdown + exported count + ratio.
func TestOnboardModule_ModuleSummaryShape(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupOnboardTestServer(t)

	res, err := srv.handleOnboardModule(context.Background(), makeReq(map[string]any{
		"directory": "core/",
		"project":   projectID,
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	body := decode(t, res)
	summary, ok := body["module_summary"].(map[string]any)
	if !ok {
		t.Fatal("missing module_summary")
	}
	langs, _ := summary["language_breakdown"].(map[string]any)
	if _, hasGo := langs["Go"]; !hasGo {
		t.Errorf("language_breakdown missing Go; got %v", langs)
	}
	if expSurface, _ := summary["exported_surface_count"].(float64); int(expSurface) < 2 {
		t.Errorf("exported_surface_count = %v; expected ≥ 2 (Foo + Bar)", expSurface)
	}
	if _, ok := summary["test_to_code_ratio"]; !ok {
		t.Error("test_to_code_ratio missing")
	}
}

// TestOnboardModule_PathPrefixDoesNotMatchSubstring — control:
// `core` (without trailing slash) only matches files in core/, NOT
// files in sibling dirs that happen to start with "core".
func TestOnboardModule_PathPrefixDoesNotMatchSubstring(t *testing.T) {
	t.Parallel()
	srv, store, root := newTestServer(t)
	srv.sessionRoot = root
	writeGoFile(t, root, "go.mod", onboardGoMod)
	writeGoFile(t, root, "core/core.go", onboardCoreSrc)
	// `core_extra/` shares the prefix but is NOT the same package.
	writeGoFile(t, root, "core_extra/extra.go", "package core_extra\n\nfunc Extra() {}\n")

	idx := index.New(store)
	res, err := idx.Index(context.Background(), root, false)
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	srv.sessionID = res.ProjectID

	out, err := srv.handleOnboardModule(context.Background(), makeReq(map[string]any{
		"directory": "core",
		"project":   res.ProjectID,
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	body := decode(t, out)
	scope, _ := body["scope"].(map[string]any)
	// Should match core/ (1 file), NOT core_extra/ (also 1 file).
	if fc, _ := scope["file_count"].(float64); int(fc) != 1 {
		t.Errorf("file_count = %v; expected 1 (substring should not match core_extra/)", fc)
	}
}

// TestOnboardModule_NoEntryPoints_EmitsWarningAndNextStep_1879 —
// positive empty-shape: a library package (core/) has no main() /
// Test*() / init(), so is_entry_point fires for zero symbols and
// entry_points_local_to_scope = []. Pre-#1879 this came back silent
// — empty list, no annotation, no orientation hint. The fix emits
// a warnings_v2.no_entry_points_in_scope entry plus a next_step
// pointing at the most-consumed exported symbol (the real "where
// do I start reading" answer for a library package).
func TestOnboardModule_NoEntryPoints_EmitsWarningAndNextStep_1879(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupOnboardTestServer(t)

	res, err := srv.handleOnboardModule(context.Background(), makeReq(map[string]any{
		"directory": "core/",
		"project":   projectID,
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	body := decode(t, res)

	// Sanity: entry_points is empty (precondition for the warning).
	entryPoints, _ := body["entry_points_local_to_scope"].([]any)
	if len(entryPoints) != 0 {
		t.Fatalf("precondition: expected entry_points_local_to_scope=[]; got %v", entryPoints)
	}

	meta, _ := body["_meta"].(map[string]any)
	if meta == nil {
		t.Fatal("missing _meta")
	}
	warnings, _ := meta["warnings_v2"].([]any)
	foundWarning := false
	for _, w := range warnings {
		m, _ := w.(map[string]any)
		if m["code"] == "no_entry_points_in_scope" {
			foundWarning = true
			if sev, _ := m["severity"].(string); sev != "info" {
				t.Errorf("warning severity = %q; want info", sev)
			}
			break
		}
	}
	if !foundWarning {
		t.Errorf("expected warnings_v2[].code = no_entry_points_in_scope; got %v", warnings)
	}

	// A next_step pointing at the most-consumed exported symbol
	// (core.Foo, called by consumer.UseCore in the fixture).
	nextSteps, _ := meta["next_steps"].([]any)
	foundFooHint := false
	for _, ns := range nextSteps {
		m, _ := ns.(map[string]any)
		if m["tool"] == "context" && strings.Contains(m["why"].(string), "Foo") {
			foundFooHint = true
			break
		}
	}
	if !foundFooHint {
		t.Errorf("expected next_steps entry pointing at core.Foo (most-consumed exported); got %v", nextSteps)
	}
}

// TestOnboardModule_IsRegistered — gate: tool is registered and
// the description mentions orientation intent.
func TestOnboardModule_IsRegistered(t *testing.T) {
	srv, _, _ := newRichTestServer(t)
	tool, ok := srv.tools["onboard_module"]
	if !ok {
		t.Fatal("onboard_module not registered in srv.tools")
	}
	desc := strings.ToLower(tool.Description)
	for _, want := range []string{"orient", "module", "scope"} {
		if !strings.Contains(desc, want) {
			t.Errorf("description should mention %q; got %q", want, tool.Description)
		}
	}
}

// TestSQLLikeEscape — pure unit: LIKE metacharacters get escaped
// so an unsanitised input like `cmd/[pinch]/` doesn't unexpectedly
// match more than intended.
func TestSQLLikeEscape(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"core/", "core/"},
		{"100%/", `100\%/`},
		{"snake_case/", `snake\_case/`},
		{`back\slash/`, `back\\slash/`},
		{"", ""},
	}
	for _, c := range cases {
		if got := sqlLikeEscape(c.in); got != c.want {
			t.Errorf("sqlLikeEscape(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}
