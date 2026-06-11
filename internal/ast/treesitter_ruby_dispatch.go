// SPDX-License-Identifier: MIT

// Production dispatcher for Ruby extraction (ADR-0008, Phase 2): route to the
// real-tree-sitter AST tier when the file parses cleanly, else fall back to the
// regex tier. Ruby's regex tier is the 0.70 "approximate" tier, so a clean
// tree-sitter parse is a 0.70 → 1.0 lift. Thread-safe via a bounded pool.

package ast

import (
	"context"
	"os"
	"sync"
)

func rubyASTEnabled() bool {
	return os.Getenv("PINCHER_DISABLE_RUBY_AST") != "1"
}

var (
	rubyTSOnce sync.Once
	rubyTSPool chan *rubyTSExtractor
)

func initRubyTSPool() {
	cap := rustTSPoolCap()
	pool := make(chan *rubyTSExtractor, cap)
	for i := 0; i < cap; i++ {
		x, err := newRubyTSExtractor(context.Background())
		if err != nil {
			if i == 0 {
				return
			}
			break
		}
		pool <- x
	}
	rubyTSPool = pool
}

func extractRubyTreeSitter(source []byte, relPath string) (*FileResult, bool) {
	rubyTSOnce.Do(initRubyTSPool)
	if rubyTSPool == nil {
		return nil, false
	}
	x := <-rubyTSPool
	defer func() { rubyTSPool <- x }()
	return x.extractChecked(source, relPath)
}
