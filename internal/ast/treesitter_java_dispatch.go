// SPDX-License-Identifier: MIT

// Production dispatcher for Java extraction (ADR-0008, #1958): route to the
// real-tree-sitter AST tier when the file parses cleanly, else fall back to
// the regex tier. Thread-safe via a bounded, lazily-initialized pool of
// tree-sitter instances (the indexer extracts files concurrently and
// tsbridge instances are not concurrency-safe). Mirrors the Rust dispatcher.

package ast

import (
	"context"
	"os"
	"sync"
)

// javaASTEnabled gates the tree-sitter Java path. Default-on (the ADR-0008
// production tier); PINCHER_DISABLE_JAVA_AST=1 reverts to the regex tier as
// an escape hatch, mirroring PINCHER_DISABLE_RUST_AST.
func javaASTEnabled() bool {
	return os.Getenv("PINCHER_DISABLE_JAVA_AST") != "1"
}

var (
	javaTSOnce sync.Once
	javaTSPool chan *javaTSExtractor // nil if init failed → silent regex fallback
)

func initJavaTSPool() {
	cap := rustTSPoolCap() // same RSS-bounding cap as Rust
	pool := make(chan *javaTSExtractor, cap)
	for i := 0; i < cap; i++ {
		x, err := newJavaTSExtractor(context.Background())
		if err != nil {
			if i == 0 {
				return // pool stays nil
			}
			break
		}
		pool <- x
	}
	javaTSPool = pool
}

// extractJavaTreeSitter is the production entry: (result, true) on a clean
// tree-sitter parse, or (nil, false) so the caller uses the regex tier.
// Thread-safe; lazily builds the instance pool on first Java file.
func extractJavaTreeSitter(source []byte, relPath string) (*FileResult, bool) {
	javaTSOnce.Do(initJavaTSPool)
	if javaTSPool == nil {
		return nil, false
	}
	x := <-javaTSPool
	defer func() { javaTSPool <- x }()
	return x.extractChecked(source, relPath)
}
