// SPDX-License-Identifier: MIT

// Production dispatcher for Swift extraction (ADR-0008, Phase 2): route to the
// real-tree-sitter AST tier when the file parses cleanly, else fall back to the
// regex tier. Thread-safe via a bounded, lazily-initialized pool.

package ast

import (
	"context"
	"os"
	"sync"
)

func swiftASTEnabled() bool {
	return os.Getenv("PINCHER_DISABLE_SWIFT_AST") != "1"
}

var (
	swiftTSOnce sync.Once
	swiftTSPool chan *swiftTSExtractor
)

func initSwiftTSPool() {
	cap := rustTSPoolCap()
	pool := make(chan *swiftTSExtractor, cap)
	for i := 0; i < cap; i++ {
		x, err := newSwiftTSExtractor(context.Background())
		if err != nil {
			if i == 0 {
				return
			}
			break
		}
		pool <- x
	}
	swiftTSPool = pool
}

func extractSwiftTreeSitter(source []byte, relPath string) (*FileResult, bool) {
	swiftTSOnce.Do(initSwiftTSPool)
	if swiftTSPool == nil {
		return nil, false
	}
	x := <-swiftTSPool
	defer func() { swiftTSPool <- x }()
	return x.extractChecked(source, relPath)
}
