// SPDX-License-Identifier: MIT

package ast

import (
	"context"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

const rubyMemSrc = `require "set"
require_relative "acc"

module Engine
  class Runner < Base
    def initialize
      @cache = {}
      setup()
    end

    def self.build
      Runner.new
    end

    def run(n)
      acc = Acc.new
      n.times { |i| acc.add(self.score(i)) }
      acc.total
    end

    def score(x)
      x * 2
    end
  end
end

def helper
  1
end
`

func TestRubyTSExtractor_NoLeakUnderReuse(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("RSS probe is Linux-only")
	}
	x, err := newRubyTSExtractor(context.Background())
	if err != nil {
		t.Fatalf("newRubyTSExtractor: %v", err)
	}
	src := []byte(rubyMemSrc)
	for i := 0; i < 50; i++ {
		_, _ = x.extractChecked(src, "warm.rb")
	}
	runtime.GC()
	debug.FreeOSMemory()
	before := vmRSSKB(t)
	if before < 0 {
		t.Skip("no VmRSS")
	}
	const N = 3000
	for i := 0; i < N; i++ {
		fr, ok := x.extractChecked(src, "Engine.rb")
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

// FuzzRubyTSExtract fuzzes the marshaling boundary; extractChecked must never
// panic and always return a non-nil result.
// Run: go test -run x -fuzz FuzzRubyTSExtract ./internal/ast
func FuzzRubyTSExtract(f *testing.F) {
	for _, seed := range []string{
		"", "class", "class C", rubyMemSrc,
		"module M", "def f", "def self.g",
		"class C\n def m\n \xff\xfe\n end\nend",
		strings.Repeat("def f\n", 100),
		"require \"x\"", "class C < ",
	} {
		f.Add([]byte(seed))
	}
	x, err := newRubyTSExtractor(context.Background())
	if err != nil {
		f.Fatalf("newRubyTSExtractor: %v", err)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		fr, _ := x.extractChecked(data, "fuzz.rb")
		if fr == nil {
			t.Fatal("nil FileResult")
		}
	})
}
