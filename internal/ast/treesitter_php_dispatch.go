// SPDX-License-Identifier: MIT

// Production dispatcher for PHP extraction (ADR-0008, Phase 2): route to the
// real-tree-sitter AST tier when the file parses cleanly, else fall back to
// the regex tier. Thread-safe via a bounded, lazily-initialized pool. Mirrors
// the Rust/Java/C#/TS dispatchers.

package ast

import (
	"context"
	"os"
	"sync"
)

// phpASTEnabled gates the tree-sitter PHP path. Default-on; PINCHER_DISABLE_PHP_AST=1
// reverts to the regex tier as an escape hatch.
func phpASTEnabled() bool {
	return os.Getenv("PINCHER_DISABLE_PHP_AST") != "1"
}

var (
	phpTSOnce sync.Once
	phpTSPool chan *phpTSExtractor // nil if init failed → silent regex fallback
)

func initPHPTSPool() {
	cap := rustTSPoolCap()
	pool := make(chan *phpTSExtractor, cap)
	for i := 0; i < cap; i++ {
		x, err := newPHPTSExtractor(context.Background())
		if err != nil {
			if i == 0 {
				return
			}
			break
		}
		pool <- x
	}
	phpTSPool = pool
}

func extractPHPTreeSitter(source []byte, relPath string) (*FileResult, bool) {
	phpTSOnce.Do(initPHPTSPool)
	if phpTSPool == nil {
		return nil, false
	}
	x := <-phpTSPool
	defer func() { phpTSPool <- x }()
	return x.extractChecked(source, relPath)
}
