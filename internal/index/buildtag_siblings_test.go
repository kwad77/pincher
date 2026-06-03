// SPDX-License-Identifier: MIT

package index

import (
	"context"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// #566: build-tag duplicate-implementation siblings (web_windows.go /
// web_unix.go pattern). Pre-fix the resolver picked one of the
// platform variants via lex-order pickCanonical and the others
// false-positived in dead_code as "no inbound CALLS." The cheap
// filename-based heuristic detects siblings and fans out the edge
// to ALL variants.

// TestIsBuildTagSibling exercises the filename pattern matcher in
// isolation. Table-driven so new platform suffixes are easy to add.
func TestIsBuildTagSibling(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		// Real siblings.
		{"cmd/pinch/web_windows.go", "cmd/pinch/web_unix.go", true},
		{"cmd/pinch/web_windows.go", "cmd/pinch/web_linux.go", true},
		{"cmd/pinch/web_windows.go", "cmd/pinch/web_darwin.go", true},
		{"runtime/syscall_amd64.go", "runtime/syscall_arm64.go", true},
		{"net/sock_freebsd.go", "net/sock_openbsd.go", true},
		// NOT siblings — different stem.
		{"cmd/pinch/web_windows.go", "cmd/pinch/other.go", false},
		// NOT siblings — different dir.
		{"cmd/pinch/web_windows.go", "cmd/other/web_unix.go", false},
		// NOT siblings — same file.
		{"cmd/pinch/web_windows.go", "cmd/pinch/web_windows.go", false},
		// NOT siblings — test file is a deliberate carve-out.
		{"cmd/pinch/web.go", "cmd/pinch/web_test.go", false},
		// NOT siblings — non-Go.
		{"cmd/pinch/web_windows.go", "cmd/pinch/web_unix.txt", false},
		// Empty inputs.
		{"", "cmd/pinch/web_unix.go", false},
		{"cmd/pinch/web_unix.go", "", false},
		// Backslash separator (Windows path) — should still detect dir match.
		{"cmd\\pinch\\web_windows.go", "cmd\\pinch\\web_unix.go", true},
	}
	for _, c := range cases {
		got := isBuildTagSibling(c.a, c.b)
		if got != c.want {
			t.Errorf("isBuildTagSibling(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// TestResolveCalls_BuildTagSiblings_BothGetEdge is the integration
// repro: a cross-platform caller calls into a function that has
// `_windows.go` / `_unix.go` siblings. Both variants must surface
// as inbound callers (not just lex-smallest pickCanonical).
func TestResolveCalls_BuildTagSiblings_BothGetEdge(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	// Three files mirroring the cmd/pinch/web pattern:
	// - web.go: cross-platform caller
	// - web_windows.go: Windows implementation
	// - web_unix.go: Unix implementation
	writeFile(t, dir, "web/web.go", `package web

func launch() error {
	return platformPIDAlive(123)
}
`)
	writeFile(t, dir, "web/web_windows.go", `//go:build windows

package web

func platformPIDAlive(pid int) error { return nil }
`)
	writeFile(t, dir, "web/web_unix.go", `//go:build !windows

package web

func platformPIDAlive(pid int) error { return nil }
`)

	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("Index: %v", err)
	}
	pid := db.ProjectIDFromPath(dir)

	// Both platformPIDAlive variants must be inbound-reachable from
	// web.go::launch. Pre-fix only the lex-smallest (web_unix.go)
	// would have an inbound edge.
	syms, err := store.GetSymbolsByName(pid, "platformPIDAlive", 5)
	if err != nil {
		t.Fatalf("GetSymbolsByName: %v", err)
	}
	if len(syms) != 2 {
		t.Fatalf("expected 2 platformPIDAlive symbols (windows + unix); got %d", len(syms))
	}

	// Each variant must have ≥1 inbound CALLS edge from web.go::launch.
	for _, s := range syms {
		results, err := store.TraceViaCTEScoped(pid, s.ID, "inbound", []string{"CALLS"}, 3)
		if err != nil {
			t.Fatalf("TraceViaCTEScoped %s: %v", s.FilePath, err)
		}
		var sawLaunch bool
		for _, r := range results {
			caller, _ := store.GetSymbol(r.SymbolID)
			if caller != nil && caller.Name == "launch" {
				sawLaunch = true
				break
			}
		}
		if !sawLaunch {
			t.Errorf("platformPIDAlive in %s has no inbound CALLS from launch — #566 sibling fan-out missed",
				s.FilePath)
		}
	}
}

// TestDeadCode_BuildTagSiblings_NotFlagged is the dead_code surface:
// neither sibling should appear after the fan-out lands.
func TestDeadCode_BuildTagSiblings_NotFlagged(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "web/web.go", `package web

func launch() error {
	return platformPIDAlive(123)
}
`)
	writeFile(t, dir, "web/web_windows.go", `//go:build windows

package web

func platformPIDAlive(pid int) error { return nil }
`)
	writeFile(t, dir, "web/web_unix.go", `//go:build !windows

package web

func platformPIDAlive(pid int) error { return nil }
`)
	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("Index: %v", err)
	}
	pid := db.ProjectIDFromPath(dir)
	dead, err := store.GetDeadCode(pid, []string{"Function"}, "Go", 1.0, 100)
	if err != nil {
		t.Fatalf("GetDeadCode: %v", err)
	}
	for _, s := range dead {
		if s.Name == "platformPIDAlive" {
			t.Errorf("platformPIDAlive in %s flagged dead — build-tag sibling fan-out regression (#566)", s.FilePath)
		}
	}
}
