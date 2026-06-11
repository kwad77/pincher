// SPDX-License-Identifier: MIT

package ast

import (
	"context"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

const phpMemSrc = `<?php
namespace App\Engine;

use App\Contracts\Step;
use App\Support\Acc;

interface Stepper { public function step(): int; }

enum State { case Idle; case Running; case Done; }

class Engine implements Stepper {
    private array $cache = [];
    public function __construct() { $this->init(); }
    public function run(int $n): int {
        $acc = new Acc();
        for ($i = 0; $i < $n; $i++) { $acc->add($this->score($i)); }
        return $acc->total();
    }
    private function score(int $x): int { return $x * 2; }
    public function step(): int { return 0; }
}

trait Loggable { public function log(string $m): void { record($m); } }

function helper(): int { return 1; }
`

func TestPHPTSExtractor_NoLeakUnderReuse(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("RSS probe is Linux-only")
	}
	x, err := newPHPTSExtractor(context.Background())
	if err != nil {
		t.Fatalf("newPHPTSExtractor: %v", err)
	}
	src := []byte(phpMemSrc)
	for i := 0; i < 50; i++ {
		_, _ = x.extractChecked(src, "warm.php")
	}
	runtime.GC()
	debug.FreeOSMemory()
	before := vmRSSKB(t)
	if before < 0 {
		t.Skip("no VmRSS")
	}
	const N = 3000
	for i := 0; i < N; i++ {
		fr, ok := x.extractChecked(src, "Engine.php")
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

// FuzzPHPTSExtract fuzzes the WASM<->Go marshaling boundary; extractChecked
// must never panic and always return a non-nil result.
// Run: go test -run x -fuzz FuzzPHPTSExtract ./internal/ast
func FuzzPHPTSExtract(f *testing.F) {
	for _, seed := range []string{
		"", "<?php", "<?php class C {", phpMemSrc,
		"<?php interface I", "<?php trait T {", "<?php use A\\B;",
		"<?php class C { function m() { \xff\xfe } }",
		strings.Repeat("<?php function f(){", 100),
		"<?php enum E {", "<?php namespace N;\n function g(){}",
	} {
		f.Add([]byte(seed))
	}
	x, err := newPHPTSExtractor(context.Background())
	if err != nil {
		f.Fatalf("newPHPTSExtractor: %v", err)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		fr, _ := x.extractChecked(data, "fuzz.php")
		if fr == nil {
			t.Fatal("nil FileResult")
		}
	})
}
