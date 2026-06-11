// SPDX-License-Identifier: MIT

package ast

import (
	"context"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

const cppMemSrc = `#include <vector>
#include <string>
#include "engine.h"

namespace eng {

class Engine : public Base {
public:
    Engine();
    int run(int n) { return score(n); }
private:
    int score(int x) { return x * 2; }
    std::vector<int> cache;
};

struct Point { int x; int y; };
enum class Mode { On, Off };

}

int Engine::compute(int x) {
    helper();
    return x;
}

void freefn() { obj.go(); run(1); }
`

func TestCppTSExtractor_NoLeakUnderReuse(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("RSS probe is Linux-only")
	}
	x, err := newCppTSExtractor(context.Background())
	if err != nil {
		t.Fatalf("newCppTSExtractor: %v", err)
	}
	src := []byte(cppMemSrc)
	for i := 0; i < 50; i++ {
		_, _ = x.extractChecked(src, "warm.cpp")
	}
	runtime.GC()
	debug.FreeOSMemory()
	before := vmRSSKB(t)
	if before < 0 {
		t.Skip("no VmRSS")
	}
	const N = 3000
	for i := 0; i < N; i++ {
		fr, ok := x.extractChecked(src, "Engine.cpp")
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

func FuzzCppTSExtract(f *testing.F) {
	for _, seed := range []string{
		"", "class", "class C", cppMemSrc,
		"struct S {", "enum class E {", "#include <v>",
		"class C { void m() { \xff\xfe } };",
		strings.Repeat("void f(){", 100),
		"namespace n {", "int C::m()",
	} {
		f.Add([]byte(seed))
	}
	x, err := newCppTSExtractor(context.Background())
	if err != nil {
		f.Fatalf("newCppTSExtractor: %v", err)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		fr, _ := x.extractChecked(data, "fuzz.cpp")
		if fr == nil {
			t.Fatal("nil FileResult")
		}
	})
}
