// SPDX-License-Identifier: MIT

package index

import "sync/atomic"

// PR-4' (loop-substrate): index-pass generation counter.
//
// Generation returns the number of completed index passes this process
// has run. It backs the `_meta.watermark` stamp (gN.cM): equal
// generations between two responses guarantee the symbol graph did not
// change in between, so consumers — loop agents above all — can key
// caches and resume-checkpoints on it instead of re-probing.
func (idx *Indexer) Generation() int64 {
	return atomic.LoadInt64(&idx.generation)
}

func (idx *Indexer) bumpGeneration() {
	atomic.AddInt64(&idx.generation, 1)
}
