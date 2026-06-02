package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kwad77/pincher/internal/db"
	"github.com/kwad77/pincher/internal/index"
)

// runSelfTestCLI implements `pincher self-test [--data-dir DIR] [--verbose]`.
//
// Smoke-tests the install end-to-end:
//  1. Open a fresh DB in a temp dir (or the user-supplied --data-dir).
//  2. Create a tiny synthetic Go project on disk.
//  3. Index it via the real Indexer.
//  4. Search for a known symbol via the real db Search path.
//  5. Fetch the symbol via the real byte-offset retrieval.
//
// Reports each step's pass/fail with a one-line summary, and exits 0 on
// all-green or 1 if any step fails.
//
// Why a separate subcommand vs. just `go test`: this verifies the SHIPPED
// binary against a real filesystem, not the test harness. After upgrading
// (Homebrew, plugin, manual install), `pincher self-test` confirms the
// binary works against your real OS / Go version / FS — which the test
// suite can't do post-build.
func runSelfTestCLI(args []string) {
	log.SetOutput(io.Discard)
	out := io.Writer(os.Stderr)
	if selfTestArgsWantJSON(args) {
		out = os.Stdout
	}
	if exitCode := runSelfTest(args, out); exitCode != 0 {
		os.Exit(exitCode)
	}
}

func selfTestArgsWantJSON(args []string) bool {
	for _, arg := range args {
		if arg == "--json" || arg == "-json" {
			return true
		}
		for _, prefix := range []string{"--json=", "-json="} {
			if strings.HasPrefix(arg, prefix) {
				v, err := strconv.ParseBool(strings.TrimPrefix(arg, prefix))
				return err == nil && v
			}
		}
	}
	return false
}

// runSelfTest is the testable entrypoint: parses args, runs the steps,
// writes output to `out`, returns the exit code (0 = OK, 1 = failure).
// Pulled out of runSelfTestCLI so tests don't need to fork a process or
// override os.Exit. The CLI wrapper is a one-liner that os.Exits on
// non-zero return.
func runSelfTest(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("self-test", flag.ExitOnError)
	dataDirFlag := fs.String("data-dir", "", "Override data directory (default: platform tmp)")
	jsonOut := fs.Bool("json", false, "Emit machine-readable JSON")
	verbose := fs.Bool("verbose", false, "Print step timings + intermediate state")
	fs.Usage = func() {
		fmt.Fprintln(out, "usage: pincher self-test [--data-dir DIR] [--json] [--verbose]")
		fmt.Fprintln(out, "  Smoke-tests the install: index a synthetic project, search, retrieve.")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	steps := []selfTestStep{
		{name: "open_database", label: "1/5  open database", fn: openDB},
		{name: "create_synthetic_project", label: "2/5  create synthetic project", fn: createSynthetic},
		{name: "index_project", label: "3/5  index the project", fn: indexSynthetic},
		{name: "search_known_symbol", label: "4/5  search for known symbol", fn: searchSynthetic},
		{name: "retrieve_symbol_source", label: "5/5  retrieve symbol source via byte offsets", fn: retrieveSynthetic},
	}

	rt := &selfTestRuntime{dataDir: *dataDirFlag, verbose: *verbose}
	var cleanup func()
	if rt.dataDir == "" {
		tmp, err := os.MkdirTemp("", "pincher-selftest-*")
		if err != nil {
			if *jsonOut {
				writeSelfTestJSON(out, selfTestReport{OK: false, Error: "setup tmp dir: " + err.Error()})
				return 1
			}
			fmt.Fprintf(out, "FAIL: setup tmp dir: %v\n", err)
			return 1
		}
		cleanup = func() { _ = os.RemoveAll(tmp) }
		rt.dataDir = filepath.Join(tmp, "data")
	}
	defer func() {
		if cleanup != nil {
			cleanup()
		}
	}()
	defer cleanupSelfTestRuntime(rt)

	allOK := true
	report := selfTestReport{OK: true, Steps: []selfTestStepResult{}}
	for _, step := range steps {
		t0 := time.Now()
		err := step.fn(rt)
		dur := time.Since(t0)
		stepResult := selfTestStepResult{
			Name:       step.name,
			Label:      step.label,
			OK:         err == nil,
			DurationMS: dur.Milliseconds(),
		}
		if err != nil {
			stepResult.Error = err.Error()
			allOK = false
			report.OK = false
			report.Steps = append(report.Steps, stepResult)
			if !*jsonOut {
				fmt.Fprintf(out, "%s  FAIL  (%dms)  %v\n", step.label, dur.Milliseconds(), err)
			}
			break // bail on first failure — later steps depend on earlier ones
		}
		report.Steps = append(report.Steps, stepResult)
		if !*jsonOut {
			if rt.verbose {
				fmt.Fprintf(out, "%s  OK    (%dms)\n", step.label, dur.Milliseconds())
			} else {
				fmt.Fprintf(out, "%s  OK\n", step.label)
			}
		}
	}

	if *jsonOut {
		writeSelfTestJSON(out, report)
		if !allOK {
			return 1
		}
		return 0
	}
	if !allOK {
		fmt.Fprintln(out, "\nself-test: FAIL")
		return 1
	}
	fmt.Fprintln(out, "\nself-test: OK — pincher is healthy on this install")
	return 0
}

// selfTestRuntime carries state across steps so each step is a pure fn
// taking the runtime.
type selfTestRuntime struct {
	dataDir    string
	verbose    bool
	store      *db.Store
	indexer    *index.Indexer
	projectDir string
	projectID  string
	symbolID   string // captured by step 4, used by step 5
}

type selfTestStep struct {
	name  string
	label string
	fn    func(*selfTestRuntime) error
}

type selfTestStepResult struct {
	Name       string `json:"name"`
	Label      string `json:"label"`
	OK         bool   `json:"ok"`
	DurationMS int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
}

type selfTestReport struct {
	OK    bool                 `json:"ok"`
	Steps []selfTestStepResult `json:"steps"`
	Error string               `json:"error,omitempty"`
}

func writeSelfTestJSON(out io.Writer, report selfTestReport) {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	_ = enc.Encode(report)
}

func cleanupSelfTestRuntime(rt *selfTestRuntime) {
	if rt == nil {
		return
	}
	if rt.store != nil {
		if rt.projectID != "" {
			_ = rt.store.DeleteProject(rt.projectID)
		}
		_ = rt.store.Close()
		rt.store = nil
	}
	if rt.projectDir != "" {
		_ = os.RemoveAll(rt.projectDir)
		rt.projectDir = ""
	}
}

func openDB(rt *selfTestRuntime) error {
	if err := os.MkdirAll(rt.dataDir, 0o755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	store, err := db.Open(rt.dataDir)
	if err != nil {
		return err
	}
	rt.store = store
	rt.indexer = index.New(store)
	return nil
}

func createSynthetic(rt *selfTestRuntime) error {
	tmp, err := os.MkdirTemp("", "pincher-selftest-proj-*")
	if err != nil {
		return err
	}
	rt.projectDir = tmp
	src := []byte(`package selftest

// SelfTestProbe is the symbol the self-test searches for and retrieves.
// Renaming this breaks the self-test contract — see cmd/pinch/selftest.go.
func SelfTestProbe(x int) int {
	return x + 1
}
`)
	return os.WriteFile(filepath.Join(tmp, "main.go"), src, 0o644)
}

func indexSynthetic(rt *selfTestRuntime) error {
	result, err := rt.indexer.Index(context.Background(), rt.projectDir, false)
	if err != nil {
		return err
	}
	if result.Symbols == 0 {
		return fmt.Errorf("indexer reported 0 symbols on a project with one Go function")
	}
	rt.projectID = db.ProjectIDFromPath(rt.projectDir)
	return nil
}

func searchSynthetic(rt *selfTestRuntime) error {
	results, err := rt.store.SearchSymbolsByCorpus(rt.projectID, "SelfTestProbe", "", "", "code", 5)
	if err != nil {
		return err
	}
	if len(results) == 0 {
		return fmt.Errorf("search for SelfTestProbe returned 0 results — FTS5 trigger may not be firing")
	}
	rt.symbolID = results[0].Symbol.ID
	return nil
}

func retrieveSynthetic(rt *selfTestRuntime) error {
	sym, err := rt.store.GetSymbol(rt.symbolID)
	if err != nil {
		return err
	}
	if sym == nil {
		return fmt.Errorf("GetSymbol returned nil for ID %q surfaced by search — symbol_moves drift?", rt.symbolID)
	}
	src, err := index.ReadSymbolSource(rt.projectDir, *sym)
	if err != nil {
		return fmt.Errorf("byte-offset retrieval failed: %w", err)
	}
	if src == "" {
		return fmt.Errorf("byte-offset retrieval returned empty string for non-empty symbol")
	}
	return nil
}
