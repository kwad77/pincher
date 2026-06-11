// SPDX-License-Identifier: MIT

package ast

import (
	"context"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

const kotlinMemSrc = `package com.engine

import kotlin.collections.Map
import com.acc.Acc

interface Stepper { fun step(): Int }

data class State(val v: Int)

class Engine : Stepper {
    private val cache: MutableMap<String, Int> = mutableMapOf()
    override fun step(): Int = 0
    fun run(n: Int): Int {
        val acc = Acc()
        for (i in 0 until n) { acc.add(score(i)) }
        return acc.total()
    }
    private fun score(x: Int): Int = x * 2
    companion object { fun build() = Engine() }
}

enum class Mode { ON, OFF; fun toggle() = this == ON }

object Registry { fun lookup(k: String) = cache[k] }

fun helper(): Int = 1
`

func TestKotlinTSExtractor_NoLeakUnderReuse(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("RSS probe is Linux-only")
	}
	x, err := newKotlinTSExtractor(context.Background())
	if err != nil {
		t.Fatalf("newKotlinTSExtractor: %v", err)
	}
	src := []byte(kotlinMemSrc)
	for i := 0; i < 50; i++ {
		_, _ = x.extractChecked(src, "warm.kt")
	}
	runtime.GC()
	debug.FreeOSMemory()
	before := vmRSSKB(t)
	if before < 0 {
		t.Skip("no VmRSS")
	}
	const N = 3000
	for i := 0; i < N; i++ {
		fr, ok := x.extractChecked(src, "Engine.kt")
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

// FuzzKotlinTSExtract fuzzes the marshaling boundary; extractChecked must never
// panic and always return a non-nil result.
// Run: go test -run x -fuzz FuzzKotlinTSExtract ./internal/ast
func FuzzKotlinTSExtract(f *testing.F) {
	for _, seed := range []string{
		"", "class", "class C", kotlinMemSrc,
		"interface I", "object O", "enum class E {",
		"class C { fun m() { \xff\xfe } }",
		strings.Repeat("fun f(){", 100),
		"data class D(", "import a.b",
	} {
		f.Add([]byte(seed))
	}
	x, err := newKotlinTSExtractor(context.Background())
	if err != nil {
		f.Fatalf("newKotlinTSExtractor: %v", err)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		fr, _ := x.extractChecked(data, "fuzz.kt")
		if fr == nil {
			t.Fatal("nil FileResult")
		}
	})
}
