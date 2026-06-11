// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/kwad77/pincher/internal/db"
	"github.com/kwad77/pincher/internal/index"
	"github.com/kwad77/pincher/internal/server"
)

// test_impacted.go — `pincher test-impacted` closes the loop the MCP
// `changes` tool opens: `changes` computes a ranked tests_to_run list
// from the diff's blast radius, and until now the agent copied those
// names into `go test -run ...` commands by hand. This subcommand runs
// the SAME analysis (server.AnalyzeChanges — shared with handleChanges,
// not forked) and executes exactly the implicated Go tests, returning
// conclusions: one line per package, detail only on failure.

// impactedPackage is one per-package `go test` invocation derived from
// the impacted-test set.
type impactedPackage struct {
	Package    string   `json:"package"`
	Tests      []string `json:"tests"`
	RunRegex   string   `json:"run_regex"`
	Command    string   `json:"command"`
	Status     string   `json:"status,omitempty"` // ok | FAIL | skipped
	DurationS  float64  `json:"duration_s,omitempty"`
	Failing    []string `json:"failing,omitempty"`
	OutputTail string   `json:"output_tail,omitempty"`
}

// notRunnableTest is an impacted test this command can't execute (not a
// Go `Test*` function in a `_test.go` file — e.g. a *.spec.ts suite or
// a Go benchmark). Listed honestly instead of silently dropped.
type notRunnableTest struct {
	Name     string `json:"name"`
	FilePath string `json:"file_path"`
}

// testImpactedDepth mirrors the MCP `changes` tool's default BFS depth
// so both surfaces implicate the same test set for the same diff.
const testImpactedDepth = 3

// testImpactedTailLines bounds the per-package failure output to its
// conclusion-bearing tail.
const testImpactedTailLines = 30

func runTestImpactedCLI(args []string) {
	os.Exit(testImpactedCLI(args, os.Stdout, os.Stderr))
}

// testImpactedCLI is the testable core of runTestImpactedCLI: it writes
// to the supplied streams and returns the process exit code instead of
// calling os.Exit. Exit codes: 0 = all impacted tests passed (or there
// was nothing to run), 1 = a test failed or the command itself errored.
func testImpactedCLI(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("test-impacted", flag.ExitOnError)
	fs.SetOutput(stderr)
	scope := fs.String("scope", "unstaged", "Diff scope: unstaged | staged | all | base:<branch>")
	dryRun := fs.Bool("dry-run", false, "Print the per-package 'go test -run' commands without executing them")
	timeout := fs.Duration("timeout", 10*time.Minute, "Total budget for executing the impacted tests")
	maxTests := fs.Int("max-tests", 0, "Run only the N highest-overlap impacted tests (0 = all impacted)")
	jsonOut := fs.Bool("json", false, "Emit machine-readable JSON instead of text")
	dataDir := fs.String("data-dir", "", "Override data directory")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: pincher test-impacted [--scope unstaged|staged|all|base:<branch>] [--dry-run] [--timeout 10m] [--max-tests N] [--json] [--data-dir DIR]")
		fmt.Fprintln(stderr, "  Computes the current diff's blast radius (the same analysis as the `changes` MCP")
		fmt.Fprintln(stderr, "  tool) and runs exactly the Go tests it implicates, grouped per package:")
		fmt.Fprintln(stderr, "    go test ./pkg -run '^(TestA|TestB)$' -count=1")
		fmt.Fprintln(stderr, "  Exit code 0 when every impacted test passes, 1 on any failure.")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	store, _, err := openProjectStoreReadOnly(*dataDir)
	if err != nil {
		fmt.Fprintf(stderr, "pincher test-impacted: %v\n", err)
		return 1
	}
	defer store.Close()

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "pincher test-impacted: cwd: %v\n", err)
		return 1
	}
	proj, err := store.GetProject(db.ProjectIDFromPath(cwd))
	if err != nil {
		fmt.Fprintf(stderr, "pincher test-impacted: lookup project for %s: %v\n", cwd, err)
		return 1
	}
	if proj == nil {
		fmt.Fprintf(stderr, "pincher test-impacted: no indexed project for %s — test selection needs the call graph.\n", cwd)
		fmt.Fprintln(stderr, "  Run `pincher index .` from the repo root first, then re-run this command.")
		return 1
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	analysis, err := server.AnalyzeChanges(ctx, store, index.New(store), proj.ID, proj.Path, *scope, testImpactedDepth)
	if err != nil {
		fmt.Fprintf(stderr, "pincher test-impacted: %v\n", err)
		fmt.Fprintln(stderr, "  Valid scopes: unstaged (default), staged, all, base:<branch>.")
		return 1
	}

	if len(analysis.ChangedFiles) == 0 {
		return emitNothingToRun(stdout, *jsonOut, *scope,
			fmt.Sprintf("no changes detected (scope=%s) — nothing to test", *scope))
	}
	if len(analysis.TestsToRun) == 0 {
		return emitNothingToRun(stdout, *jsonOut, *scope,
			fmt.Sprintf("diff touches %d file(s) / %d symbol(s) but no indexed test reaches the changed symbols — run the `changes` MCP tool to inspect the blast radius (the impacted code may simply be untested)",
				len(analysis.ChangedFiles), len(analysis.ChangedSymbols)))
	}

	tests := analysis.TestsToRun
	if *maxTests > 0 && len(tests) > *maxTests {
		// TestsToRun is already sorted overlap-descending, so trimming
		// keeps the highest-signal tests.
		tests = tests[:*maxTests]
	}

	pkgs, notRunnable, totalRunnable := partitionImpactedTests(tests)

	if *dryRun {
		if *jsonOut {
			emitTestImpactedJSON(stdout, *scope, true, pkgs, notRunnable, totalRunnable, "DRY-RUN")
		} else {
			for _, p := range pkgs {
				fmt.Fprintln(stdout, p.Command)
			}
			printNotRunnable(stdout, notRunnable)
			fmt.Fprintf(stdout, "IMPACTED: %d packages, %d tests — DRY RUN (nothing executed)\n", len(pkgs), totalRunnable)
		}
		return 0
	}

	runCtx, cancelRun := context.WithTimeout(ctx, *timeout)
	defer cancelRun()
	allPass := true
	for i := range pkgs {
		p := &pkgs[i]
		if runCtx.Err() != nil {
			// Budget exhausted by an earlier package — report honestly
			// instead of letting every remaining package "fail" at 0s.
			p.Status = "skipped"
			allPass = false
			continue
		}
		cmd := exec.CommandContext(runCtx, "go", "test", p.Package, "-run", p.RunRegex, "-count=1")
		cmd.Dir = proj.Path
		started := time.Now()
		out, runErr := cmd.CombinedOutput()
		p.DurationS = time.Since(started).Seconds()
		if runErr == nil {
			p.Status = "ok"
			continue
		}
		p.Status = "FAIL"
		allPass = false
		p.Failing = parseFailingTests(string(out))
		p.OutputTail = tailLines(string(out), testImpactedTailLines)
		if runCtx.Err() != nil {
			p.OutputTail += fmt.Sprintf("\n(killed: --timeout %s budget exhausted)", *timeout)
		}
	}

	status := "PASS"
	exitCode := 0
	if !allPass {
		status = "FAIL"
		exitCode = 1
	}

	if *jsonOut {
		emitTestImpactedJSON(stdout, *scope, false, pkgs, notRunnable, totalRunnable, status)
		return exitCode
	}

	for _, p := range pkgs {
		switch p.Status {
		case "ok":
			fmt.Fprintf(stdout, "ok   %s (%d tests, %.1fs)\n", p.Package, len(p.Tests), p.DurationS)
		case "skipped":
			fmt.Fprintf(stdout, "SKIP %s (%d tests) — --timeout budget exhausted before this package ran\n", p.Package, len(p.Tests))
		default:
			fmt.Fprintf(stdout, "FAIL %s (%d tests, %.1fs)\n", p.Package, len(p.Tests), p.DurationS)
			if len(p.Failing) > 0 {
				fmt.Fprintf(stdout, "  failing: %s\n", strings.Join(p.Failing, ", "))
			}
			for _, line := range strings.Split(p.OutputTail, "\n") {
				fmt.Fprintf(stdout, "  %s\n", line)
			}
		}
	}
	printNotRunnable(stdout, notRunnable)
	fmt.Fprintf(stdout, "IMPACTED: %d packages, %d tests — %s\n", len(pkgs), totalRunnable, status)
	return exitCode
}

// partitionImpactedTests splits the impacted-test list into per-package
// `go test` invocations (Go `Test*` functions in `_test.go` files) and
// the honest leftovers this command cannot run (*.spec.ts suites, Go
// benchmarks/fuzz targets, suite methods). Packages and test names are
// sorted for deterministic output; duplicate names within a package
// (build-tag variants) collapse to one -run entry.
func partitionImpactedTests(tests []server.ImpactedTest) (pkgs []impactedPackage, notRunnable []notRunnableTest, totalRunnable int) {
	pkgs = []impactedPackage{}
	notRunnable = []notRunnableTest{}
	byPkg := map[string]map[string]bool{}
	for _, tr := range tests {
		fp := filepath.ToSlash(tr.FilePath)
		if !strings.HasSuffix(fp, "_test.go") || !strings.HasPrefix(tr.Name, "Test") {
			notRunnable = append(notRunnable, notRunnableTest{Name: tr.Name, FilePath: tr.FilePath})
			continue
		}
		pkg := "./"
		if dir := path.Dir(fp); dir != "." {
			pkg = "./" + dir
		}
		if byPkg[pkg] == nil {
			byPkg[pkg] = map[string]bool{}
		}
		byPkg[pkg][tr.Name] = true
	}

	pkgNames := make([]string, 0, len(byPkg))
	for pkg := range byPkg {
		pkgNames = append(pkgNames, pkg)
	}
	sort.Strings(pkgNames)
	for _, pkg := range pkgNames {
		names := make([]string, 0, len(byPkg[pkg]))
		for name := range byPkg[pkg] {
			names = append(names, name)
		}
		sort.Strings(names)
		quoted := make([]string, len(names))
		for i, n := range names {
			quoted[i] = regexp.QuoteMeta(n)
		}
		re := "^(" + strings.Join(quoted, "|") + ")$"
		pkgs = append(pkgs, impactedPackage{
			Package:  pkg,
			Tests:    names,
			RunRegex: re,
			Command:  fmt.Sprintf("go test %s -run '%s' -count=1", pkg, re),
		})
		totalRunnable += len(names)
	}
	return pkgs, notRunnable, totalRunnable
}

// emitNothingToRun reports the two zero-work outcomes (clean diff, or a
// diff that implicates no tests). Both exit 0 — there is no failure to
// signal, and a non-zero exit would break `test-impacted && git commit`
// chains on clean trees.
func emitNothingToRun(stdout io.Writer, jsonOut bool, scope, message string) int {
	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(map[string]any{
			"scope":        scope,
			"message":      message,
			"packages":     []impactedPackage{},
			"not_runnable": []notRunnableTest{},
			"summary":      map[string]any{"packages": 0, "tests": 0, "status": "PASS"},
		})
	} else {
		fmt.Fprintln(stdout, message)
	}
	return 0
}

func emitTestImpactedJSON(stdout io.Writer, scope string, dryRun bool, pkgs []impactedPackage, notRunnable []notRunnableTest, totalRunnable int, status string) {
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(map[string]any{
		"scope":        scope,
		"dry_run":      dryRun,
		"packages":     pkgs,
		"not_runnable": notRunnable,
		"summary": map[string]any{
			"packages": len(pkgs),
			"tests":    totalRunnable,
			"status":   status,
		},
	})
}

func printNotRunnable(stdout io.Writer, notRunnable []notRunnableTest) {
	if len(notRunnable) == 0 {
		return
	}
	fmt.Fprintf(stdout, "not runnable by this command (%d):\n", len(notRunnable))
	for _, nr := range notRunnable {
		fmt.Fprintf(stdout, "  %s (%s)\n", nr.Name, nr.FilePath)
	}
}

// parseFailingTests extracts the failed test names from `go test`
// output (`--- FAIL: TestName (0.00s)` lines, including subtests).
// Deduplicated, in first-seen order.
func parseFailingTests(out string) []string {
	seen := map[string]bool{}
	var names []string
	for _, line := range strings.Split(out, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "--- FAIL: ")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 || seen[fields[0]] {
			continue
		}
		seen[fields[0]] = true
		names = append(names, fields[0])
	}
	return names
}

// tailLines returns the last n lines of s (trailing newline stripped).
func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
