// SPDX-License-Identifier: MIT

package ast

import (
	"context"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

const swiftMemSrc = `import Foundation

protocol Stepper { func step() -> Int }

struct Acc { var total: Int; mutating func add(_ x: Int) { total += x } }

class Engine: Stepper {
    private var cache: [String: Int] = [:]
    init() { setup() }
    func run(_ n: Int) -> Int {
        var acc = Acc(total: 0)
        for i in 0..<n { acc.add(self.score(i)) }
        return acc.total
    }
    func score(_ x: Int) -> Int { return x * 2 }
    func step() -> Int { return 0 }
}

enum State { case idle, running, done; func active() -> Bool { return self == .running } }

extension Engine { func reset() { cache.removeAll() } }

func helper() -> Int { return 1 }
`

func TestSwiftTSExtractor_NoLeakUnderReuse(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("RSS probe is Linux-only")
	}
	x, err := newSwiftTSExtractor(context.Background())
	if err != nil {
		t.Fatalf("newSwiftTSExtractor: %v", err)
	}
	src := []byte(swiftMemSrc)
	for i := 0; i < 50; i++ {
		_, _ = x.extractChecked(src, "warm.swift")
	}
	runtime.GC()
	debug.FreeOSMemory()
	before := vmRSSKB(t)
	if before < 0 {
		t.Skip("no VmRSS")
	}
	const N = 3000
	for i := 0; i < N; i++ {
		fr, ok := x.extractChecked(src, "Engine.swift")
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

// FuzzSwiftTSExtract fuzzes the marshaling boundary; extractChecked must never
// panic and always return a non-nil result.
// Run: go test -run x -fuzz FuzzSwiftTSExtract ./internal/ast
func FuzzSwiftTSExtract(f *testing.F) {
	for _, seed := range []string{
		"", "class", "class C {", swiftMemSrc,
		"protocol P {", "enum E {", "extension X {",
		"class C { func m() { \xff\xfe } }",
		strings.Repeat("func f(){", 100),
		"struct S {", "import Foo\nfunc g(){}",
	} {
		f.Add([]byte(seed))
	}
	x, err := newSwiftTSExtractor(context.Background())
	if err != nil {
		f.Fatalf("newSwiftTSExtractor: %v", err)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		fr, _ := x.extractChecked(data, "fuzz.swift")
		if fr == nil {
			t.Fatal("nil FileResult")
		}
	})
}
