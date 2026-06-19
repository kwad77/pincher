// SPDX-License-Identifier: MIT

// Production dispatcher for TypeScript / TSX extraction (ADR-0008, #1958):
// route to the real-tree-sitter AST tier when the file parses cleanly, else
// fall back to the regex tier. Two pools — .ts uses the typescript grammar,
// .tsx uses the tsx grammar — selected by the language name the registry
// passes. Mirrors the Rust/Java/C# dispatchers.

package ast

import (
	"context"
	"os"
	"sync"
)

// tsASTEnabled gates the tree-sitter TS/TSX path. Default-on (the ADR-0008
// production tier); PINCHER_DISABLE_TS_AST=1 reverts to the regex tier as an
// escape hatch, mirroring the other PINCHER_DISABLE_<LANG>_AST flags.
func tsASTEnabled() bool {
	return os.Getenv("PINCHER_DISABLE_TS_AST") != "1"
}

// TypeScriptASTEnabled reports whether the TS/TSX dispatcher will attempt the
// tree-sitter path for the next file. Exported for health/parser identity, where
// the registered adapter confidence remains 0.85 to describe the regex fallback
// tier but the live default route is AST.
func TypeScriptASTEnabled() bool {
	return tsASTEnabled()
}

var (
	tsTSOnce  sync.Once
	tsTSPool  chan *tsTSExtractor // typescript grammar (.ts)
	tsxTSOnce sync.Once
	tsxTSPool chan *tsTSExtractor // tsx grammar (.tsx)
)

func initTSPool(tsx bool) chan *tsTSExtractor {
	cap := rustTSPoolCap()
	pool := make(chan *tsTSExtractor, cap)
	for i := 0; i < cap; i++ {
		ex, err := newTSTSExtractor(context.Background(), tsx)
		if err != nil {
			if i == 0 {
				return nil
			}
			break
		}
		pool <- ex
	}
	return pool
}

// extractTypeScriptTreeSitter is the production entry. lang is the registry's
// language name ("TypeScript" → .ts grammar, "TSX" → .tsx grammar); anything
// else routes to the .ts grammar. Returns (result, true) on a clean parse, or
// (nil, false) so the caller uses the regex tier.
func extractTypeScriptTreeSitter(source []byte, relPath, lang string) (*FileResult, bool) {
	var pool chan *tsTSExtractor
	if lang == "TSX" {
		tsxTSOnce.Do(func() { tsxTSPool = initTSPool(true) })
		pool = tsxTSPool
	} else {
		tsTSOnce.Do(func() { tsTSPool = initTSPool(false) })
		pool = tsTSPool
	}
	if pool == nil {
		return nil, false
	}
	ex := <-pool
	defer func() { pool <- ex }()
	return ex.extractChecked(source, relPath)
}
