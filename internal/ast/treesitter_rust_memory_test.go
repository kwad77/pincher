// SPDX-License-Identifier: MIT

package ast

import (
	"context"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

const rustMemSrc = `use std::collections::{HashMap, HashSet, BTreeMap};
use std::fmt;

pub struct Engine { cache: HashMap<String, i64>, seen: HashSet<u64> }
pub enum State { Idle, Running, Done(i64) }
pub trait Step { fn step(&mut self) -> State; }

impl Engine {
    pub fn new() -> Self { Engine { cache: HashMap::new(), seen: HashSet::new() } }
    pub fn run(&mut self, n: i64) -> i64 {
        let mut total = 0;
        for i in 0..n { total += self.score(i); helper(i); }
        total
    }
    fn score(&self, x: i64) -> i64 { x * 2 }
}

impl Step for Engine { fn step(&mut self) -> State { State::Running } }
impl fmt::Debug for Engine { fn fmt(&self, f: &mut fmt::Formatter) -> fmt::Result { write!(f, "Engine") } }

fn helper(n: i64) -> i64 { n + 1 }
macro_rules! twice { ($x:expr) => { $x + $x }; }
`

func vmRSSKB(t *testing.T) int64 {
	t.Helper()
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return -1
	}
	for _, ln := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(ln, "VmRSS:") {
			var kb int64
			_, _ = fmtSscan(strings.Fields(ln)[1], &kb)
			return kb
		}
	}
	return -1
}

func fmtSscan(s string, out *int64) (int, error) {
	var n int64
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			break
		}
		n = n*10 + int64(s[i]-'0')
	}
	*out = n
	return 1, nil
}

// TestRustTSExtractor_NoLeakUnderReuse is the load-bearing check for the
// WASM free lifecycle (#1957): reusing one extractor instance across many
// files must not grow the WASM heap / process RSS unbounded. Without the
// per-extraction tree + node frees, parsing thousands of files leaks
// hundreds of MB; with them, RSS stays flat.
func TestRustTSExtractor_NoLeakUnderReuse(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("RSS probe is Linux-only")
	}
	x, err := newRustTSExtractor(context.Background())
	if err != nil {
		t.Fatalf("newRustTSExtractor: %v", err)
	}
	src := []byte(rustMemSrc)

	// Warm up (first parse grows the arena to this file's high-water mark).
	for i := 0; i < 50; i++ {
		_, _ = x.extractChecked(src, "warm.rs")
	}
	runtime.GC()
	debug.FreeOSMemory()
	before := vmRSSKB(t)
	if before < 0 {
		t.Skip("no VmRSS")
	}

	const N = 3000
	for i := 0; i < N; i++ {
		fr, ok := x.extractChecked(src, "engine.rs")
		if !ok || len(fr.Symbols) == 0 {
			t.Fatalf("iter %d: clean parse expected symbols, ok=%v n=%d", i, ok, len(fr.Symbols))
		}
	}
	runtime.GC()
	debug.FreeOSMemory()
	after := vmRSSKB(t)

	growthMB := float64(after-before) / 1024.0
	t.Logf("RSS before=%dMB after=%dMB growth=%.1fMB over %d parses",
		before/1024, after/1024, growthMB, N)
	// A leaking binding grows ~hundreds of MB over 3000 parses of a
	// multi-symbol file. Allow generous slack for Go heap churn / arena
	// high-water; a real leak blows way past this.
	if growthMB > 60 {
		t.Errorf("RSS grew %.1fMB over %d parses — WASM memory likely leaking", growthMB, N)
	}
}

// FuzzRustTSExtract fuzzes the WASM<->Go marshaling boundary with arbitrary
// bytes. The contract: extractChecked never panics and always returns,
// regardless of how malformed the input is (the review's "fuzz the
// marshaler" gate). Run: go test -run x -fuzz FuzzRustTSExtract ./internal/ast
func FuzzRustTSExtract(f *testing.F) {
	for _, seed := range []string{
		"", "fn", "fn f(", rustMemSrc,
		"struct S<", "impl ", "use a::{",
		"fn f() { \xff\xfe }", strings.Repeat("mod m{", 200),
		"trait T{fn x();}", "//\x00\n fn g(){}",
	} {
		f.Add([]byte(seed))
	}
	x, err := newRustTSExtractor(context.Background())
	if err != nil {
		f.Fatalf("newRustTSExtractor: %v", err)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		// Must not panic; ok may be true or false.
		fr, _ := x.extractChecked(data, "fuzz.rs")
		if fr == nil {
			t.Fatal("nil FileResult")
		}
	})
}
