// SPDX-License-Identifier: MIT

// Production dispatcher for Rust extraction (ADR-0008, #1957): route to the
// real-tree-sitter AST tier when the file parses cleanly, else fall back to
// the regex tier. Thread-safe via a bounded, lazily-initialized pool of
// tree-sitter instances (the indexer extracts files concurrently and
// tsbridge instances are not concurrency-safe).

package ast

import (
	"context"
	"os"
	"runtime"
	"sync"
)

// rustASTEnabled gates the tree-sitter Rust path. Default-on (the ADR-0008
// production tier); PINCHER_DISABLE_RUST_AST=1 reverts to the regex tier for
// one release cycle as an escape hatch, mirroring PINCHER_DISABLE_JS_AST.
func rustASTEnabled() bool {
	return os.Getenv("PINCHER_DISABLE_RUST_AST") != "1"
}

// rustTSPoolCap bounds the pool. tree-sitter parse is fast; the cap exists to
// bound RSS (each wazero instance holds a linear-memory arena that grows to
// the largest file it parses), not throughput. Capped well below GOMAXPROCS
// on big machines.
func rustTSPoolCap() int {
	n := runtime.GOMAXPROCS(0)
	if n > 4 {
		n = 4
	}
	if n < 1 {
		n = 1
	}
	return n
}

var (
	rustTSOnce sync.Once
	rustTSPool chan *rustTSExtractor // nil if init failed → silent regex fallback
)

func initRustTSPool() {
	cap := rustTSPoolCap()
	pool := make(chan *rustTSExtractor, cap)
	for i := 0; i < cap; i++ {
		x, err := newRustTSExtractor(context.Background())
		if err != nil {
			// Partial or total init failure: hand back what we built (if
			// any) and leave the rest; extractRustTreeSitter degrades to
			// regex when the pool can't serve.
			if i == 0 {
				return // pool stays nil
			}
			break
		}
		pool <- x
	}
	rustTSPool = pool
}

// extractRustTreeSitter is the production entry: (result, true) on a clean
// tree-sitter parse, or (nil, false) so the caller uses the regex tier.
// Thread-safe; lazily builds the instance pool on first Rust file.
func extractRustTreeSitter(source []byte, relPath string) (*FileResult, bool) {
	rustTSOnce.Do(initRustTSPool)
	if rustTSPool == nil {
		return nil, false
	}
	x := <-rustTSPool
	defer func() { rustTSPool <- x }()
	return x.extractChecked(source, relPath)
}
