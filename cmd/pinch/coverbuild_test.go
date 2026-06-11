// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

var cachedPincherBinary struct {
	once sync.Once
	dir  string
	path string
	err  error
}

func TestMain(m *testing.M) {
	code := m.Run()
	if cachedPincherBinary.dir != "" {
		_ = os.RemoveAll(cachedPincherBinary.dir)
	}
	os.Exit(code)
}

// buildPincherBinary compiles a pincher binary once per package test
// process and returns its absolute path.
//
// When the GOCOVERDIR environment variable is set, the binary is built
// with `-cover` instrumentation so any subprocess invocation that runs
// it (and propagates GOCOVERDIR) will write coverage data into that
// directory. After tests run, `go tool covdata textfmt -i=$GOCOVERDIR`
// converts those binary counters into a coverage profile that can be
// merged with the main `go test -coverprofile` output.
//
// This is the workaround for #185: integration-style tests that exercise
// the runXxxCLI dispatch wrappers via exec.Cmd otherwise leave those
// functions at 0% coverage even when their behaviour is fully covered.
//
// Caller is responsible for setting GOCOVERDIR in the spawned binary's
// environment via pincherCoverEnv() — without it the instrumentation is
// a no-op (Go's runtime coverage system silently drops counters when
// GOCOVERDIR is unset).
func buildPincherBinary(t *testing.T) string {
	t.Helper()
	cachedPincherBinary.once.Do(func() {
		cachedPincherBinary.path, cachedPincherBinary.err = buildPincherBinaryOnce()
	})
	if cachedPincherBinary.err != nil {
		t.Fatalf("build pincher binary: %v", cachedPincherBinary.err)
	}
	return cachedPincherBinary.path
}

func buildPincherBinaryOnce() (string, error) {
	dir, err := os.MkdirTemp("", "pincher-test-bin-*")
	if err != nil {
		return "", err
	}
	cachedPincherBinary.dir = dir
	bin := filepath.Join(dir, pincherBinaryName())

	args := []string{"build"}
	// Only instrument when the caller has wired up a GOCOVERDIR. Adding
	// `-cover` unconditionally would inflate every binary-test build
	// time (~3x on this codebase) for no benefit when not collecting
	// coverage. The CI Coverage job sets GOCOVERDIR; local `go test`
	// runs typically do not.
	if os.Getenv("GOCOVERDIR") != "" {
		// Instrument all packages EXCEPT the vendored WASM binding
		// (internal/tsbridge). Under the full-repo coverage shard
		// (`go test ./...`), the subprocess pinch binary's covmeta must
		// match the package set the run is collecting; including tsbridge
		// here (it links into pinch via the Rust AST dispatcher, #1957)
		// makes the subprocess covmeta diverge and the runner silently
		// drops the folded dispatch-wrapper counters (all runXxxCLI → 0%).
		// We don't gate coverage on the vendored binding anyway.
		cp, err := pinchCoverPkg()
		if err != nil {
			return "", err
		}
		args = append(args, "-cover", "-coverpkg="+cp)
	}
	args = append(args, "-o", bin, ".")

	cmd := exec.Command("go", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", execBuildError{args: args, err: err, out: out}
	}
	return bin, nil
}

// pinchCoverPkg lists every module package except the vendored WASM binding,
// for the subprocess pinch binary's -coverpkg (see buildPincherBinaryOnce).
func pinchCoverPkg() (string, error) {
	out, err := exec.Command("go", "list", "./...").Output()
	if err != nil {
		return "", err
	}
	var keep []string
	for _, p := range strings.Fields(string(out)) {
		if strings.HasSuffix(p, "/internal/tsbridge") {
			continue
		}
		keep = append(keep, p)
	}
	return strings.Join(keep, ","), nil
}

type execBuildError struct {
	args []string
	err  error
	out  []byte
}

func (e execBuildError) Error() string {
	return "build (" + strings.Join(e.args, " ") + "): " + e.err.Error() + "\n" + string(e.out)
}

// pincherCoverEnv returns os.Environ() with GOCOVERDIR set to the
// parent process's GOCOVERDIR (when present) so a spawned pincher
// subprocess writes its coverage counters to the same directory the
// test runner is collecting from.
//
// When the parent has no GOCOVERDIR, this returns os.Environ()
// unchanged and the subprocess's coverage instrumentation (if any) is
// a no-op — the test still runs to completion and asserts behaviour,
// just without contributing coverage.
func pincherCoverEnv() []string {
	env := os.Environ()
	if dir := os.Getenv("GOCOVERDIR"); dir != "" {
		// Already in the env from os.Environ(); explicit re-set is a
		// belt-and-suspenders defence against tests that otherwise
		// scrub the environment before exec.
		env = append(env, "GOCOVERDIR="+dir)
	}
	return env
}

// runtimeOSGuard is a placeholder so `runtime` import survives even when
// future refactors split pincherBinaryName() out of this file's neighbour.
// pincherBinaryName itself is defined in rebuild_fts_test.go for
// historical reasons; keeping it where it is for blame-history continuity.
var _ = runtime.GOOS
