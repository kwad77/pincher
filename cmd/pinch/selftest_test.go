package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// Smoke test for the self-test runtime steps. Mirrors `pincher self-test`
// but runs in-process so we can catch regressions in the harness itself
// before they ship as silent self-test failures.
func TestSelfTestSteps_AllPass(t *testing.T) {
	rt := &selfTestRuntime{dataDir: t.TempDir()}
	t.Cleanup(func() {
		if rt.store != nil {
			_ = rt.store.Close()
		}
		if rt.projectDir != "" {
			_ = os.RemoveAll(rt.projectDir)
		}
	})

	steps := []selfTestStep{
		{name: "open_database", label: "open database", fn: openDB},
		{name: "create_synthetic_project", label: "create synthetic project", fn: createSynthetic},
		{name: "index_project", label: "index the project", fn: indexSynthetic},
		{name: "search_known_symbol", label: "search for known symbol", fn: searchSynthetic},
		{name: "retrieve_symbol_source", label: "retrieve symbol source", fn: retrieveSynthetic},
	}
	for _, step := range steps {
		if err := step.fn(rt); err != nil {
			t.Fatalf("step %q failed: %v", step.label, err)
		}
	}

	// Post-conditions: rt should carry state forward as each step runs.
	if rt.store == nil {
		t.Error("openDB should populate rt.store")
	}
	if rt.indexer == nil {
		t.Error("openDB should populate rt.indexer")
	}
	if rt.projectDir == "" {
		t.Error("createSynthetic should set rt.projectDir")
	}
	if rt.projectID == "" {
		t.Error("indexSynthetic should set rt.projectID")
	}
	if rt.symbolID == "" {
		t.Error("searchSynthetic should set rt.symbolID")
	}
}

// TestRunSelfTest_HappyPath exercises the full runSelfTest entrypoint
// (the part runSelfTestCLI calls), captures stderr, asserts exit=0 and
// the all-OK summary line.
func TestRunSelfTest_HappyPath(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	exitCode := runSelfTest([]string{"--data-dir", dir}, &out)
	if exitCode != 0 {
		t.Errorf("exit code = %d, want 0; output:\n%s", exitCode, out.String())
	}
	if !strings.Contains(out.String(), "self-test: OK") {
		t.Errorf("missing OK summary; output:\n%s", out.String())
	}
	// Each of the 5 step labels should appear with OK.
	for _, label := range []string{"1/5", "2/5", "3/5", "4/5", "5/5"} {
		if !strings.Contains(out.String(), label) {
			t.Errorf("step %s missing from output:\n%s", label, out.String())
		}
	}

	store, err := db.Open(dir)
	if err != nil {
		t.Fatalf("open data dir after self-test: %v", err)
	}
	defer store.Close()
	projects, err := store.ListProjects()
	if err != nil {
		t.Fatalf("ListProjects after self-test: %v", err)
	}
	if len(projects) != 0 {
		t.Fatalf("self-test leaked synthetic project rows: %+v", projects)
	}
}

func TestRunSelfTest_JSONHappyPath(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	exitCode := runSelfTest([]string{"--data-dir", dir, "--json"}, &out)
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; output:\n%s", exitCode, out.String())
	}
	if strings.Contains(out.String(), "self-test: OK") || strings.Contains(out.String(), "1/5  open database  OK") {
		t.Fatalf("--json output should not include prose status lines:\n%s", out.String())
	}
	var report selfTestReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("--json output is not valid selfTestReport JSON: %v\n%s", err, out.String())
	}
	if !report.OK {
		t.Fatalf("report.OK = false, want true: %+v", report)
	}
	if len(report.Steps) != 5 {
		t.Fatalf("len(report.Steps) = %d, want 5: %+v", len(report.Steps), report.Steps)
	}
	for _, step := range report.Steps {
		if step.Name == "" {
			t.Fatalf("step %q missing stable machine-readable name: %+v", step.Label, step)
		}
		if !step.OK {
			t.Fatalf("step %q failed in JSON report: %+v", step.Label, step)
		}
	}

	store, err := db.Open(dir)
	if err != nil {
		t.Fatalf("open data dir after self-test: %v", err)
	}
	defer store.Close()
	projects, err := store.ListProjects()
	if err != nil {
		t.Fatalf("ListProjects after self-test: %v", err)
	}
	if len(projects) != 0 {
		t.Fatalf("self-test --json leaked synthetic project rows: %+v", projects)
	}
}

func TestSelfTestArgsWantJSON(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "absent", args: []string{"--data-dir", "/tmp/pincher"}, want: false},
		{name: "long", args: []string{"--json"}, want: true},
		{name: "short", args: []string{"-json"}, want: true},
		{name: "long true", args: []string{"--json=true"}, want: true},
		{name: "short true", args: []string{"-json=t"}, want: true},
		{name: "long false", args: []string{"--json=false"}, want: false},
		{name: "short false", args: []string{"-json=0"}, want: false},
		{name: "invalid value", args: []string{"--json=maybe"}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := selfTestArgsWantJSON(tt.args); got != tt.want {
				t.Fatalf("selfTestArgsWantJSON(%v) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}

// TestRunSelfTest_FailPathReturnsNonZero forces the open-database step
// to fail (parent path is a regular file, not a directory) and asserts
// runSelfTest reports it loudly + exits non-zero.
func TestRunSelfTest_FailPathReturnsNonZero(t *testing.T) {
	// Setup: create a file, then point --data-dir at a path UNDER it.
	parent := t.TempDir()
	notADir := parent + string(os.PathSeparator) + "i-am-a-file"
	if err := os.WriteFile(notADir, []byte("file"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	bogus := notADir + string(os.PathSeparator) + "child"

	var out bytes.Buffer
	exitCode := runSelfTest([]string{"--data-dir", bogus}, &out)
	if exitCode != 1 {
		t.Errorf("expected exit=1 on bad data-dir, got %d; output:\n%s", exitCode, out.String())
	}
	if !strings.Contains(out.String(), "FAIL") {
		t.Errorf("output should contain FAIL marker; got:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "self-test: FAIL") {
		t.Errorf("output should contain final summary line; got:\n%s", out.String())
	}
}

func TestRunSelfTest_JSONFailPathReturnsNonZero(t *testing.T) {
	parent := t.TempDir()
	notADir := parent + string(os.PathSeparator) + "i-am-a-file"
	if err := os.WriteFile(notADir, []byte("file"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	bogus := notADir + string(os.PathSeparator) + "child"

	var out bytes.Buffer
	exitCode := runSelfTest([]string{"--data-dir", bogus, "--json"}, &out)
	if exitCode != 1 {
		t.Fatalf("expected exit=1 on bad data-dir, got %d; output:\n%s", exitCode, out.String())
	}
	if strings.Contains(out.String(), "self-test: FAIL") {
		t.Fatalf("--json failure output should not include prose summary:\n%s", out.String())
	}
	var report selfTestReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("--json failure output is not valid selfTestReport JSON: %v\n%s", err, out.String())
	}
	if report.OK {
		t.Fatalf("report.OK = true, want false: %+v", report)
	}
	if len(report.Steps) != 1 {
		t.Fatalf("len(report.Steps) = %d, want 1: %+v", len(report.Steps), report.Steps)
	}
	if report.Steps[0].Name != "open_database" {
		t.Fatalf("failure step name = %q, want open_database: %+v", report.Steps[0].Name, report.Steps[0])
	}
	if report.Steps[0].OK || report.Steps[0].Error == "" {
		t.Fatalf("failure step should carry ok=false and error: %+v", report.Steps[0])
	}
}

func TestRunSelfTest_VerboseShowsTimings(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	exitCode := runSelfTest([]string{"--data-dir", dir, "--verbose"}, &out)
	if exitCode != 0 {
		t.Fatalf("exit = %d; output:\n%s", exitCode, out.String())
	}
	// Verbose mode should print the (Nms) timing column.
	if !strings.Contains(out.String(), "ms)") {
		t.Errorf("verbose mode should include timings; output:\n%s", out.String())
	}
}

// TestSelfTestStep_SearchFailsOnEmptyIndex ensures the search step fails
// loudly when there's nothing to find — catches a future indexer regression
// that silently produces 0 symbols (the symptom self-test exists to surface).
func TestSelfTestStep_SearchFailsOnEmptyIndex(t *testing.T) {
	rt := &selfTestRuntime{dataDir: t.TempDir()}
	t.Cleanup(func() {
		if rt.store != nil {
			_ = rt.store.Close()
		}
	})
	if err := openDB(rt); err != nil {
		t.Fatalf("openDB: %v", err)
	}
	rt.projectID = "nonexistent-project"

	if err := searchSynthetic(rt); err == nil {
		t.Error("search should fail when project has no indexed symbols")
	}
}

// TestSelfTestCLI_Binary exercises the runSelfTestCLI dispatch wrapper
// end-to-end via `pincher self-test`. The in-process tests above cover
// runSelfTest's logic but not the CLI entrypoint that calls os.Exit.
//
// With GOCOVERDIR set in the parent (CI Coverage job), the
// instrumented binary's coverage is folded into the merged profile.
func TestSelfTestCLI_Binary(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping CLI binary build in -short mode")
	}

	bin := buildPincherBinary(t)
	dataDir := t.TempDir()

	cmd := exec.Command(bin, "self-test", "--data-dir", dataDir)
	cmd.Env = pincherCoverEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("pincher self-test: %v\n%s", err, out)
	}
	got := string(out)
	if !strings.Contains(got, "self-test: OK") {
		t.Errorf("expected 'self-test: OK' in output:\n%s", got)
	}
	// Each step should report OK individually too.
	for _, step := range []string{"create synthetic project", "index the project", "search for known symbol", "retrieve symbol source"} {
		if !strings.Contains(got, step) {
			t.Errorf("missing step %q in self-test output:\n%s", step, got)
		}
	}
}

func TestSelfTestCLI_Binary_JSONWritesStdout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping CLI binary build in -short mode")
	}

	bin := buildPincherBinary(t)
	dataDir := t.TempDir()

	cmd := exec.Command(bin, "self-test", "--data-dir", dataDir, "--json")
	cmd.Env = pincherCoverEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("pincher self-test --json: %v\nstdout:\n%s\nstderr:\n%s", err, out, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("self-test --json should not write success output to stderr:\n%s", stderr.String())
	}
	var report selfTestReport
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatalf("stdout is not valid self-test JSON: %v\n%s", err, out)
	}
	if !report.OK || len(report.Steps) != 5 {
		t.Fatalf("unexpected JSON report: %+v", report)
	}
	if report.Steps[0].Name != "open_database" {
		t.Fatalf("first step name = %q, want open_database", report.Steps[0].Name)
	}
}
