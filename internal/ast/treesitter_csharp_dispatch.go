// SPDX-License-Identifier: MIT

// Production dispatcher for C# extraction (ADR-0008, #1958): route to the
// real-tree-sitter AST tier when the file parses cleanly, else fall back to
// the regex tier. Thread-safe via a bounded, lazily-initialized pool of
// tree-sitter instances. Mirrors the Rust/Java dispatchers.

package ast

import (
	"context"
	"os"
	"sync"
)

// csharpASTEnabled gates the tree-sitter C# path. Default-on (the ADR-0008
// production tier); PINCHER_DISABLE_CSHARP_AST=1 reverts to the regex tier as
// an escape hatch, mirroring PINCHER_DISABLE_RUST_AST / _JAVA_AST.
func csharpASTEnabled() bool {
	return os.Getenv("PINCHER_DISABLE_CSHARP_AST") != "1"
}

var (
	csharpTSOnce sync.Once
	csharpTSPool chan *csharpTSExtractor // nil if init failed → silent regex fallback
)

func initCSharpTSPool() {
	cap := rustTSPoolCap() // shared RSS-bounding cap
	pool := make(chan *csharpTSExtractor, cap)
	for i := 0; i < cap; i++ {
		x, err := newCSharpTSExtractor(context.Background())
		if err != nil {
			if i == 0 {
				return // pool stays nil
			}
			break
		}
		pool <- x
	}
	csharpTSPool = pool
}

// extractCSharpTreeSitter is the production entry: (result, true) on a clean
// tree-sitter parse, or (nil, false) so the caller uses the regex tier.
// Thread-safe; lazily builds the instance pool on first C# file.
func extractCSharpTreeSitter(source []byte, relPath string) (*FileResult, bool) {
	csharpTSOnce.Do(initCSharpTSPool)
	if csharpTSPool == nil {
		return nil, false
	}
	x := <-csharpTSPool
	defer func() { csharpTSPool <- x }()
	return x.extractChecked(source, relPath)
}
