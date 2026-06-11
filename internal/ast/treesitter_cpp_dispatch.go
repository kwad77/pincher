// SPDX-License-Identifier: MIT

// Production dispatcher for C++ extraction (ADR-0008, Phase 2): route to the
// real-tree-sitter AST tier when the file parses cleanly, else fall back to the
// regex tier. Only C++ files (language "C++": .cpp/.cxx/.cc/.hpp/.hh) route
// here; C files (.c/.h) stay on the regex tier. Thread-safe via a bounded pool.

package ast

import (
	"context"
	"os"
	"sync"
)

func cppASTEnabled() bool {
	return os.Getenv("PINCHER_DISABLE_CPP_AST") != "1"
}

var (
	cppTSOnce sync.Once
	cppTSPool chan *cppTSExtractor
)

func initCppTSPool() {
	cap := rustTSPoolCap()
	pool := make(chan *cppTSExtractor, cap)
	for i := 0; i < cap; i++ {
		x, err := newCppTSExtractor(context.Background())
		if err != nil {
			if i == 0 {
				return
			}
			break
		}
		pool <- x
	}
	cppTSPool = pool
}

func extractCppTreeSitter(source []byte, relPath string) (*FileResult, bool) {
	cppTSOnce.Do(initCppTSPool)
	if cppTSPool == nil {
		return nil, false
	}
	x := <-cppTSPool
	defer func() { cppTSPool <- x }()
	return x.extractChecked(source, relPath)
}
