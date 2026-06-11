// SPDX-License-Identifier: MIT

package ast

import (
	"context"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

const csharpMemSrc = `using System;
using System.Collections.Generic;
using System.Threading.Tasks;

namespace Acme.Engine
{
    public interface IStep { int Step(); }

    public enum State { Idle, Running, Done }

    public class Engine : IStep
    {
        private Dictionary<string, long> cache = new Dictionary<string, long>();

        public Engine() { Init(); }

        public async Task<long> Run(int n)
        {
            long total = 0;
            for (int i = 0; i < n; i++) { total += Score(i); Helper(i); }
            return total;
        }

        private long Score(int x) { return x * 2L; }
        public int Step() { return 0; }
    }

    public record Pair(int A, int B);
}
`

// TestCSharpTSExtractor_NoLeakUnderReuse mirrors the Rust/Java no-leak check:
// reusing one extractor across thousands of files must not grow process RSS
// unbounded — the load-bearing test for the per-extraction free lifecycle.
func TestCSharpTSExtractor_NoLeakUnderReuse(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("RSS probe is Linux-only")
	}
	x, err := newCSharpTSExtractor(context.Background())
	if err != nil {
		t.Fatalf("newCSharpTSExtractor: %v", err)
	}
	src := []byte(csharpMemSrc)

	for i := 0; i < 50; i++ {
		_, _ = x.extractChecked(src, "warm.cs")
	}
	runtime.GC()
	debug.FreeOSMemory()
	before := vmRSSKB(t)
	if before < 0 {
		t.Skip("no VmRSS")
	}

	const N = 3000
	for i := 0; i < N; i++ {
		fr, ok := x.extractChecked(src, "Engine.cs")
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
	if growthMB > 60 {
		t.Errorf("RSS grew %.1fMB over %d parses — WASM memory likely leaking", growthMB, N)
	}
}

// FuzzCSharpTSExtract fuzzes the WASM<->Go marshaling boundary: extractChecked
// must never panic and always return a non-nil result.
// Run: go test -run x -fuzz FuzzCSharpTSExtract ./internal/ast
func FuzzCSharpTSExtract(f *testing.F) {
	for _, seed := range []string{
		"", "class", "class C {", csharpMemSrc,
		"interface I<", "enum ", "using a.b.",
		"namespace N { class C { void M() { \xff\xfe } } }",
		strings.Repeat("class C{", 200),
		"record R(", "//\x00\n class G {}",
	} {
		f.Add([]byte(seed))
	}
	x, err := newCSharpTSExtractor(context.Background())
	if err != nil {
		f.Fatalf("newCSharpTSExtractor: %v", err)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		fr, _ := x.extractChecked(data, "fuzz.cs")
		if fr == nil {
			t.Fatal("nil FileResult")
		}
	})
}
