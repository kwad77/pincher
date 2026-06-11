// SPDX-License-Identifier: MIT

// Production dispatcher for Kotlin extraction (ADR-0008, Phase 2): route to the
// real-tree-sitter AST tier when the file parses cleanly, else fall back to the
// regex tier. Thread-safe via a bounded pool.

package ast

import (
	"context"
	"os"
	"sync"
)

func kotlinASTEnabled() bool {
	return os.Getenv("PINCHER_DISABLE_KOTLIN_AST") != "1"
}

var (
	kotlinTSOnce sync.Once
	kotlinTSPool chan *kotlinTSExtractor
)

func initKotlinTSPool() {
	cap := rustTSPoolCap()
	pool := make(chan *kotlinTSExtractor, cap)
	for i := 0; i < cap; i++ {
		x, err := newKotlinTSExtractor(context.Background())
		if err != nil {
			if i == 0 {
				return
			}
			break
		}
		pool <- x
	}
	kotlinTSPool = pool
}

func extractKotlinTreeSitter(source []byte, relPath string) (*FileResult, bool) {
	kotlinTSOnce.Do(initKotlinTSPool)
	if kotlinTSPool == nil {
		return nil, false
	}
	x := <-kotlinTSPool
	defer func() { kotlinTSPool <- x }()
	return x.extractChecked(source, relPath)
}
