// SPDX-License-Identifier: MIT

package ast

import (
	"context"
	"runtime"
	"strings"
	"testing"
)

const javaMemSrc = `package com.example.engine;

import java.util.HashMap;
import java.util.Map;
import java.util.List;

public class Engine implements Step {
    private Map<String, Long> cache = new HashMap<>();

    public Engine() { init(); }

    public long run(int n) {
        long total = 0;
        for (int i = 0; i < n; i++) { total += score(i); helper(i); }
        return total;
    }

    private long score(int x) { return x * 2L; }
}

interface Step { int step(); }

enum State { IDLE, RUNNING, DONE; public boolean active() { return this == RUNNING; } }

record Pair(int a, int b) {}
`

// TestJavaTSExtractor_NoLeakUnderReuse mirrors the Rust no-leak check (#1958):
// reusing one extractor instance across thousands of files must not grow the
// WASM heap / process RSS unbounded — the load-bearing test for the
// per-extraction tree + node free lifecycle.
func TestJavaTSExtractor_NoLeakUnderReuse(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("RSS probe is Linux-only")
	}
	x, err := newJavaTSExtractor(context.Background())
	if err != nil {
		t.Fatalf("newJavaTSExtractor: %v", err)
	}
	src := []byte(javaMemSrc)

	for i := 0; i < 50; i++ {
		_, _ = x.extractChecked(src, "warm.java")
	}
	runtime.GC()
	before := vmRSSKB(t)
	if before < 0 {
		t.Skip("no VmRSS")
	}

	const N = 3000
	for i := 0; i < N; i++ {
		fr, ok := x.extractChecked(src, "Engine.java")
		if !ok || len(fr.Symbols) == 0 {
			t.Fatalf("iter %d: clean parse expected symbols, ok=%v n=%d", i, ok, len(fr.Symbols))
		}
	}
	runtime.GC()
	after := vmRSSKB(t)

	growthMB := float64(after-before) / 1024.0
	t.Logf("RSS before=%dMB after=%dMB growth=%.1fMB over %d parses",
		before/1024, after/1024, growthMB, N)
	if growthMB > 60 {
		t.Errorf("RSS grew %.1fMB over %d parses — WASM memory likely leaking", growthMB, N)
	}
}

// FuzzJavaTSExtract fuzzes the WASM<->Go marshaling boundary with arbitrary
// bytes. The contract: extractChecked never panics and always returns a
// non-nil result, regardless of how malformed the input is.
// Run: go test -run x -fuzz FuzzJavaTSExtract ./internal/ast
func FuzzJavaTSExtract(f *testing.F) {
	for _, seed := range []string{
		"", "class", "class C {", javaMemSrc,
		"interface I<", "enum ", "import a.b.",
		"class C { void m() { \xff\xfe } }", strings.Repeat("class C{", 200),
		"record R(", "//\x00\n class G {}",
	} {
		f.Add([]byte(seed))
	}
	x, err := newJavaTSExtractor(context.Background())
	if err != nil {
		f.Fatalf("newJavaTSExtractor: %v", err)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		fr, _ := x.extractChecked(data, "fuzz.java")
		if fr == nil {
			t.Fatal("nil FileResult")
		}
	})
}
