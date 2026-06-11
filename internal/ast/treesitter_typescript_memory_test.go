// SPDX-License-Identifier: MIT

package ast

import (
	"context"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

const tsMemSrc = `import { z } from "zod";

export interface Step { step(): number; }

export enum State { Idle, Running, Done }

export class Engine implements Step {
	private cache = new Map<string, number>();
	process(n: number): number {
		const acc: Acc = new Acc();
		for (let i = 0; i < n; i++) { acc.add(this.score(i)); }
		return acc.total();
	}
	score(x: number): number { return x * 2; }
	step(): number { return 0; }
}

export function helper(): number {
	function inner() { const r = fetch("/x"); return r; }
	return 1;
}

export const make = (n: number): Engine => new Engine();
`

// TestTSExtractor_NoLeakUnderReuse mirrors the Rust/Java/C# no-leak check.
func TestTSExtractor_NoLeakUnderReuse(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("RSS probe is Linux-only")
	}
	x, err := newTSTSExtractor(context.Background(), false)
	if err != nil {
		t.Fatalf("newTSTSExtractor: %v", err)
	}
	src := []byte(tsMemSrc)
	for i := 0; i < 50; i++ {
		_, _ = x.extractChecked(src, "warm.ts")
	}
	runtime.GC()
	debug.FreeOSMemory()
	before := vmRSSKB(t)
	if before < 0 {
		t.Skip("no VmRSS")
	}
	const N = 3000
	for i := 0; i < N; i++ {
		fr, ok := x.extractChecked(src, "Engine.ts")
		if !ok || len(fr.Symbols) == 0 {
			t.Fatalf("iter %d: clean parse expected symbols, ok=%v n=%d", i, ok, len(fr.Symbols))
		}
	}
	runtime.GC()
	debug.FreeOSMemory()
	after := vmRSSKB(t)
	growthMB := float64(after-before) / 1024.0
	t.Logf("RSS before=%dMB after=%dMB growth=%.1fMB over %d parses", before/1024, after/1024, growthMB, N)
	if growthMB > 60 {
		t.Errorf("RSS grew %.1fMB over %d parses — WASM memory likely leaking", growthMB, N)
	}
}

// FuzzTSExtract fuzzes the WASM<->Go marshaling boundary on the typescript
// grammar: extractChecked must never panic and always return a non-nil result.
// Run: go test -run x -fuzz FuzzTSExtract ./internal/ast
func FuzzTSExtract(f *testing.F) {
	for _, seed := range []string{
		"", "class", "interface I<", tsMemSrc,
		"const f = (", "enum ", "import x from",
		"namespace N { function f() { \xff\xfe } }",
		strings.Repeat("function f(){", 200),
		"type T = ", "//\x00\n class G {}",
	} {
		f.Add([]byte(seed))
	}
	x, err := newTSTSExtractor(context.Background(), false)
	if err != nil {
		f.Fatalf("newTSTSExtractor: %v", err)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		fr, _ := x.extractChecked(data, "fuzz.ts")
		if fr == nil {
			t.Fatal("nil FileResult")
		}
	})
}
