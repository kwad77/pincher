// SPDX-License-Identifier: MIT

package server

import (
	"flag"
	"io"
	"log/slog"
	"os"
	"testing"
)

// TestMain silences pincher's slog output during benchmarks so the
// captured `go test -bench` text isn't interleaved with `INFO pincher.indexed`
// lines emitted by the indexer goroutine the server harness creates.
// See internal/index/bench_setup_test.go for the same pattern (#50).
func TestMain(m *testing.M) {
	flag.Parse()
	if flag.Lookup("test.bench").Value.String() != "" {
		slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	}
	// Router-loop B5: New() runs the pincher-router detection ladder,
	// which in the default `auto` mode consults the REAL machine
	// (~/.config/pincher-router/workers.yaml, $PATH, a healthz probe on
	// 127.0.0.1:7878). Test outcomes — goldens, advertisement contracts,
	// schema-weight totals — must not depend on what the host happens
	// to have installed, so the whole package defaults to the absent
	// state. Detection-state tests opt in per-test with
	// t.Setenv("PINCHER_ROUTER", "on") (forces detected, zero network —
	// the override item B4 shipped exactly for this); the ladder tests
	// in router_detect_test.go pass cfg.mode directly and are
	// unaffected by this default.
	os.Setenv("PINCHER_ROUTER", "off")
	os.Exit(m.Run())
}
